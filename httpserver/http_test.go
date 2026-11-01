package httpserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

	"build_your_own_s3/objectstorage"
)

// stub只记录调用并返回预设结果，不维护桶和对象状态。
// 这里检查请求到 Service 的路由与传参、结果到 HTTP 的转换，以及读取流的关闭。
func TestHTTPRoutes(t *testing.T) {
	for _, tc := range []struct {
		method, path, operation, bucket, key string
		status                               int
	}{
		{"PUT", "/photos", "CreateBucket", "photos", "", 201},
		{"GET", "/", "ListBuckets", "", "", 200},
		{"DELETE", "/photos", "DeleteBucket", "photos", "", 204},
		{"GET", "/photos", "ListObjects", "photos", "", 200},
		{"PUT", "/photos/note.txt", "PutObject", "photos", "note.txt", 204},
		{"GET", "/photos/note.txt", "GetObject", "photos", "note.txt", 200},
		{"DELETE", "/photos/note.txt", "DeleteObject", "photos", "note.txt", 204},
	} {
		t.Run(tc.operation, func(t *testing.T) {
			content := []byte{0, 0xff, '\n'}
			stream := &trackedStream{Reader: bytes.NewReader(content)}
			service := &stubService{names: []string{"one", "two"}, stream: stream, size: 3}
			handler := NewHandler(service)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(tc.method, tc.path, bytes.NewReader(content)))
			if service.calls != 1 || service.operation != tc.operation || service.bucket != tc.bucket || service.key != tc.key {
				t.Fatalf("Service 调用 = (%s, %q, %q)，次数 %d", service.operation, service.bucket, service.key, service.calls)
			}
			if response.Code != tc.status {
				t.Fatalf("HTTP %d，期望 %d", response.Code, tc.status)
			}
			switch tc.operation {
			case "ListBuckets":
				requireResponse(t, response, "application/json", "{\"buckets\":[\"one\",\"two\"]}\n")
			case "ListObjects":
				requireResponse(t, response, "application/json", "{\"keys\":[\"one\",\"two\"]}\n")
			case "GetObject":
				requireResponse(t, response, "application/octet-stream", string(content))
				if response.Header().Get("Content-Length") != "3" || !stream.closed {
					t.Fatal("下载应设置对象大小，并关闭 Service 返回的读取流")
				}
			default:
				if response.Body.Len() != 0 {
					t.Fatalf("成功响应应无响应体，得到 %q", response.Body.String())
				}
			}
			if tc.operation == "PutObject" && !bytes.Equal(service.uploaded, content) {
				t.Fatalf("Service 收到的上传内容 = %v，期望 %v", service.uploaded, content)
			}

			// 每个入口都应在 Service 失败时返回错误响应，而不是继续写成功响应。
			service.err = errors.New("storage failed")
			response = httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(tc.method, tc.path, nil))
			requireHTTPError(t, response, 500, "InternalError")
		})
	}
}

func TestHTTPEmptyResults(t *testing.T) {
	for _, tc := range []struct{ path, field string }{{"/", "buckets"}, {"/photos", "keys"}} {
		t.Run(tc.field, func(t *testing.T) {
			response := httptest.NewRecorder()
			NewHandler(&stubService{names: []string{}}).ServeHTTP(response, httptest.NewRequest("GET", tc.path, nil))
			if response.Code != 200 {
				t.Fatalf("HTTP %d，期望 200", response.Code)
			}
			requireResponse(t, response, "application/json", "{\""+tc.field+"\":[]}\n")
		})
	}

	service := &stubService{stream: &trackedStream{Reader: bytes.NewReader(nil)}}
	handler := NewHandler(service)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("PUT", "/photos/empty", nil))
	if response.Code != 204 || service.operation != "PutObject" || len(service.uploaded) != 0 {
		t.Fatal("空请求体应传给 Service 并返回 204")
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/photos/empty", nil))
	requireResponse(t, response, "application/octet-stream", "")
	if response.Code != 200 || response.Header().Get("Content-Length") != "0" || !service.stream.closed {
		t.Fatal("空下载应返回 200、长度为零并关闭读取流")
	}
}

// 公共错误转换只覆盖一次，不再通过创建桶和对象来制造业务错误。
func TestHTTPErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		name   string
	}{
		{objectstorage.InvalidBucketNameErr, 400, "InvalidBucketName"},
		{objectstorage.InvalidObjectKeyErr, 400, "InvalidObjectKey"},
		{objectstorage.BucketNameExistErr, 409, "BucketNameExist"},
		{objectstorage.BucketNotEmptyErr, 409, "BucketNotEmpty"},
		{objectstorage.BucketNotExistErr, 404, "BucketNotExist"},
		{objectstorage.ObjectNotExistErr, 404, "ObjectNotExist"},
		{errors.New("storage failed"), 500, "InternalError"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &stubService{err: tc.err}
			response := httptest.NewRecorder()
			NewHandler(service).ServeHTTP(response, httptest.NewRequest("PUT", "/photos", nil))
			requireHTTPError(t, response, tc.status, tc.name)
		})
	}
}

// HTTP 并发测试策略：让 Service stub 暂停第一个对象请求，
// 检查 HTTP 层仍允许另一个 key 的请求完成；对象数据行为由 Service 测试覆盖。
func TestHTTPAllowsConcurrentObjectRequests(t *testing.T) {
	entered, resume := make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(resume) })
	handler := NewHandler(&stubService{
		putObject: func(bucket, key string, content io.Reader) error {
			if key == "first.txt" {
				close(entered)
				<-resume
			}
			return nil
		},
	})

	startUpload := func(key string) (*httptest.ResponseRecorder, <-chan struct{}) {
		response := httptest.NewRecorder()
		request := httptest.NewRequest("PUT", "/photos/"+key, nil)
		done := make(chan struct{})
		go func() {
			handler.ServeHTTP(response, request)
			close(done)
		}()
		t.Cleanup(func() {
			release()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("清理测试时上传请求仍未结束")
			}
		})
		return response, done
	}
	waitUpload := func(done <-chan struct{}) {
		t.Helper()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("上传请求未能在 5 秒内完成")
		}
	}

	first, firstDone := startUpload("first.txt")
	select {
	case <-entered:
	case <-firstDone:
		t.Fatalf("调用 Service 前上传已返回 HTTP %d", first.Code)
	case <-time.After(5 * time.Second):
		t.Fatal("第一个请求未调用 Service")
	}
	second, secondDone := startUpload("second.txt")
	waitUpload(secondDone)
	if second.Code != 204 || second.Body.Len() != 0 {
		t.Fatalf("另一个 key 的上传响应 = HTTP %d, %q，期望 204、空响应体", second.Code, second.Body.String())
	}
	release()
	waitUpload(firstDone)
	if first.Code != 204 || first.Body.Len() != 0 {
		t.Fatalf("第一个上传响应 = HTTP %d, %q，期望 204、空响应体", first.Code, first.Body.String())
	}
}

func requireResponse(t *testing.T, response *httptest.ResponseRecorder, mediaType, body string) {
	t.Helper()
	if response.Header().Get("Content-Type") != mediaType {
		t.Fatalf("Content-Type = %q，期望 %q", response.Header().Get("Content-Type"), mediaType)
	}
	if mediaType == "application/json" {
		var got, want any
		if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(body), &want); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("JSON 响应 = %q，期望 %q", response.Body.String(), body)
		}
	} else if response.Body.String() != body {
		t.Fatalf("响应体 = %q，期望 %q", response.Body.String(), body)
	}
}

func requireHTTPError(t *testing.T, response *httptest.ResponseRecorder, status int, name string) {
	t.Helper()
	var body struct{ Error string }
	if response.Code != status || response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("错误响应 = HTTP %d, %q", response.Code, response.Header().Get("Content-Type"))
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Error != name {
		t.Fatalf("错误响应体 = %q，期望 error=%q", response.Body.String(), name)
	}
}

type stubService struct {
	operation, bucket, key string
	calls                  int
	uploaded               []byte
	err                    error
	names                  []string
	stream                 *trackedStream
	size                   int64
	putObject              func(string, string, io.Reader) error
}

func (s *stubService) record(operation, bucket, key string) error {
	s.calls++
	s.operation, s.bucket, s.key = operation, bucket, key
	return s.err
}
func (s *stubService) CreateBucket(name string) (string, error) {
	return name, s.record("CreateBucket", name, "")
}
func (s *stubService) ListBuckets() ([]string, error) {
	return s.names, s.record("ListBuckets", "", "")
}
func (s *stubService) DeleteBucket(name string) error {
	return s.record("DeleteBucket", name, "")
}
func (s *stubService) PutObject(bucket, key string, content io.Reader) error {
	if s.putObject != nil {
		return s.putObject(bucket, key, content)
	}
	if err := s.record("PutObject", bucket, key); err != nil {
		return err
	}
	var err error
	s.uploaded, err = io.ReadAll(content)
	return err
}
func (s *stubService) GetObject(bucket, key string) (io.ReadCloser, int64, error) {
	if err := s.record("GetObject", bucket, key); err != nil {
		return nil, 0, err
	}
	return s.stream, s.size, nil
}
func (s *stubService) DeleteObject(bucket, key string) error {
	return s.record("DeleteObject", bucket, key)
}
func (s *stubService) ListObjects(bucket string) ([]string, error) {
	return s.names, s.record("ListObjects", bucket, "")
}

type trackedStream struct {
	*bytes.Reader
	closed bool
}

func (s *trackedStream) Close() error {
	s.closed = true
	return nil
}
