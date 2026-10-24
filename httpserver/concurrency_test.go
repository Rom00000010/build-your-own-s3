package httpserver

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"testing"
	"time"
)

// 并发测试策略（按发生交错的资源和操作分组）：
//   - 不同 key：暂停一个 PUT 的请求体，检查另一个 key 的 GET 和 PUT 能先完成。
//   - 同一 key：覆盖 PUT 只送入部分内容时发 GET；只能读到完整旧版或新版，
//     PUT 成功返回后发起的 GET 必须读到新版。
//   - GET 与 DELETE：收到 GET 响应但尚未读完时发 DELETE；检查下载内容完整，
//     DELETE 完成后新发起的 GET 返回对象不存在。
//   - 桶与对象：暂停向空桶上传，同时删桶；检查两次响应和最终状态只能对应
//     “上传先成功，删桶报非空”或“删桶先成功，上传报桶不存在”。

type concurrentResponse struct {
	status int
	body   []byte
	err    error
}

func completeRequest(client *http.Client, req *http.Request) concurrentResponse {
	resp, err := client.Do(req)
	if err != nil {
		return concurrentResponse{err: err}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return concurrentResponse{status: resp.StatusCode, body: body, err: err}
}

func pausedUpload(t *testing.T, client *http.Client, url string) (*io.PipeWriter, <-chan concurrentResponse) {
	t.Helper()
	reader, writer := io.Pipe()
	t.Cleanup(func() { _ = writer.Close() })
	req, err := http.NewRequest(http.MethodPut, url, reader)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	req.Header.Set("Expect", "100-continue")
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{
		Got100Continue: func() { close(started) },
	}))
	result := make(chan concurrentResponse, 1)
	go func() { result <- completeRequest(client, req) }()
	select {
	case <-started:
		return writer, result
	case got := <-result:
		t.Fatalf("上传尚未读取请求体就已结束：%+v", got)
	case <-time.After(5 * time.Second):
		t.Fatal("上传未开始读取请求体")
	}
	return nil, nil
}

func finishUpload(t *testing.T, writer *io.PipeWriter, result <-chan concurrentResponse) concurrentResponse {
	t.Helper()
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-result:
		if got.err != nil {
			t.Fatalf("上传请求失败：%v", got.err)
		}
		return got
	case <-time.After(5 * time.Second):
		t.Fatal("上传请求未结束")
	}
	return concurrentResponse{}
}

func concurrentClient(server *httptest.Server) *http.Client {
	client := server.Client()
	client.Timeout = 5 * time.Second
	return client
}

func makeRequest(t *testing.T, method, url string, body io.Reader) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func requireConcurrentResponse(t *testing.T, got concurrentResponse, status int, body string) {
	t.Helper()
	if got.err != nil || got.status != status || string(got.body) != body {
		t.Fatalf("响应 = (%d, %q, %v)，期望 (%d, %q, nil)", got.status, got.body, got.err, status, body)
	}
}

func TestHTTPDifferentKeysDuringUpload(t *testing.T) {
	server := testHTTPServer(t)
	client := concurrentClient(server)
	requireConcurrentResponse(t, completeRequest(client, makeRequest(t, http.MethodPut, server.URL+"/photos", nil)), http.StatusCreated, "")
	requireConcurrentResponse(t, completeRequest(client, makeRequest(t, http.MethodPut, server.URL+"/photos/existing", strings.NewReader("old"))), http.StatusNoContent, "")

	writer, upload := pausedUpload(t, client, server.URL+"/photos/slow")
	// slow 的请求体仍未结束时，无关 key 的读取和上传应先完成。
	requireConcurrentResponse(t, completeRequest(client, makeRequest(t, http.MethodGet, server.URL+"/photos/existing", nil)), http.StatusOK, "old")
	requireConcurrentResponse(t, completeRequest(client, makeRequest(t, http.MethodPut, server.URL+"/photos/fast", strings.NewReader("fast"))), http.StatusNoContent, "")
	requireConcurrentResponse(t, completeRequest(client, makeRequest(t, http.MethodGet, server.URL+"/photos/fast", nil)), http.StatusOK, "fast")
	if _, err := writer.Write([]byte("slow")); err != nil {
		t.Fatal(err)
	}
	requireConcurrentResponse(t, finishUpload(t, writer, upload), http.StatusNoContent, "")
}

func TestHTTPSameKeyUploadAndGet(t *testing.T) {
	server := testHTTPServer(t)
	client := concurrentClient(server)
	requireConcurrentResponse(t, completeRequest(client, makeRequest(t, http.MethodPut, server.URL+"/photos", nil)), http.StatusCreated, "")
	requireConcurrentResponse(t, completeRequest(client, makeRequest(t, http.MethodPut, server.URL+"/photos/a", strings.NewReader("old"))), http.StatusNoContent, "")

	writer, upload := pausedUpload(t, client, server.URL+"/photos/a")
	if _, err := writer.Write([]byte("new")); err != nil {
		t.Fatal(err)
	}
	get := make(chan concurrentResponse, 1)
	sent := make(chan struct{})
	getReq := makeRequest(t, http.MethodGet, server.URL+"/photos/a", nil)
	getReq = getReq.WithContext(httptrace.WithClientTrace(getReq.Context(), &httptrace.ClientTrace{
		WroteRequest: func(httptrace.WroteRequestInfo) { close(sent) },
	}))
	go func() { get <- completeRequest(client, getReq) }()
	select {
	case <-sent:
	case <-time.After(5 * time.Second):
		t.Fatal("重叠 GET 请求未发出")
	}
	// GET 可以等待重叠的 PUT；若在 PUT 完成前返回，就只能读到旧版本。
	select {
	case got := <-get:
		requireConcurrentResponse(t, got, http.StatusOK, "old")
		get = nil
	case <-time.After(100 * time.Millisecond):
	}
	requireConcurrentResponse(t, finishUpload(t, writer, upload), http.StatusNoContent, "")
	if get != nil {
		select {
		case got := <-get:
			if got.err != nil || got.status != http.StatusOK ||
				(string(got.body) != "old" && string(got.body) != "new") {
				t.Fatalf("重叠 GET = (%d, %q, %v)，期望完整旧版或新版", got.status, got.body, got.err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("重叠 GET 未结束")
		}
	}
	requireConcurrentResponse(t, completeRequest(client, makeRequest(t, http.MethodGet, server.URL+"/photos/a", nil)), http.StatusOK, "new")
}

func TestHTTPDownloadAndDelete(t *testing.T) {
	server := testHTTPServer(t)
	client := concurrentClient(server)
	requireConcurrentResponse(t, completeRequest(client, makeRequest(t, http.MethodPut, server.URL+"/photos", nil)), http.StatusCreated, "")
	content := bytes.Repeat([]byte("content"), 1<<17)
	requireConcurrentResponse(t, completeRequest(client, makeRequest(t, http.MethodPut, server.URL+"/photos/a", bytes.NewReader(content))), http.StatusNoContent, "")

	resp, err := client.Do(makeRequest(t, http.MethodGet, server.URL+"/photos/a", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET 状态码 = %d，期望 200", resp.StatusCode)
	}
	deleteResult := make(chan concurrentResponse, 1)
	sent := make(chan struct{})
	deleteReq := makeRequest(t, http.MethodDelete, server.URL+"/photos/a", nil)
	deleteReq = deleteReq.WithContext(httptrace.WithClientTrace(deleteReq.Context(), &httptrace.ClientTrace{
		WroteRequest: func(httptrace.WroteRequestInfo) { close(sent) },
	}))
	go func() { deleteResult <- completeRequest(client, deleteReq) }()
	select {
	case <-sent:
	case <-time.After(5 * time.Second):
		t.Fatal("DELETE 请求未发出")
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("删除期间的下载不完整：读取 %d 字节，期望 %d 字节，错误 %v", len(got), len(content), err)
	}
	select {
	case deleted := <-deleteResult:
		requireConcurrentResponse(t, deleted, http.StatusNoContent, "")
	case <-time.After(5 * time.Second):
		t.Fatal("DELETE 请求未结束")
	}
	missing := completeRequest(client, makeRequest(t, http.MethodGet, server.URL+"/photos/a", nil))
	if missing.err != nil || missing.status != http.StatusNotFound || !bytes.Contains(missing.body, []byte("ObjectNotExist")) {
		t.Fatalf("删除后 GET = (%d, %q, %v)，期望 ObjectNotExist", missing.status, missing.body, missing.err)
	}
}

func TestHTTPUploadAndDeleteBucket(t *testing.T) {
	server := testHTTPServer(t)
	client := concurrentClient(server)
	requireConcurrentResponse(t, completeRequest(client, makeRequest(t, http.MethodPut, server.URL+"/photos", nil)), http.StatusCreated, "")
	writer, upload := pausedUpload(t, client, server.URL+"/photos/a")
	if _, err := writer.Write([]byte("data")); err != nil {
		t.Fatal(err)
	}
	deleteResult := make(chan concurrentResponse, 1)
	sent := make(chan struct{})
	deleteReq := makeRequest(t, http.MethodDelete, server.URL+"/photos", nil)
	deleteReq = deleteReq.WithContext(httptrace.WithClientTrace(deleteReq.Context(), &httptrace.ClientTrace{
		WroteRequest: func(httptrace.WroteRequestInfo) { close(sent) },
	}))
	go func() { deleteResult <- completeRequest(client, deleteReq) }()
	select {
	case <-sent:
	case <-time.After(5 * time.Second):
		t.Fatal("删桶请求未发出")
	}
	put := finishUpload(t, writer, upload)
	var deleted concurrentResponse
	select {
	case deleted = <-deleteResult:
	case <-time.After(5 * time.Second):
		t.Fatal("删桶请求未结束")
	}
	switch {
	case put.err == nil && put.status == http.StatusNoContent && deleted.err == nil &&
		deleted.status == http.StatusConflict && bytes.Contains(deleted.body, []byte("BucketNotEmpty")):
		requireConcurrentResponse(t, completeRequest(client, makeRequest(t, http.MethodGet, server.URL+"/photos/a", nil)), http.StatusOK, "data")
	case put.err == nil && put.status == http.StatusNotFound && bytes.Contains(put.body, []byte("BucketNotExist")) &&
		deleted.err == nil && deleted.status == http.StatusNoContent:
		missing := completeRequest(client, makeRequest(t, http.MethodGet, server.URL+"/photos", nil))
		if missing.err != nil || missing.status != http.StatusNotFound || !bytes.Contains(missing.body, []byte("BucketNotExist")) {
			t.Fatalf("删桶后列表 = (%d, %q, %v)，期望 BucketNotExist", missing.status, missing.body, missing.err)
		}
	default:
		t.Fatalf("上传/删桶 = ((%d, %q, %v), (%d, %q, %v))，不符合任一串行顺序",
			put.status, put.body, put.err, deleted.status, deleted.body, deleted.err)
	}
}
