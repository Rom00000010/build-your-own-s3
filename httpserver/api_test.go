package httpserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"build_your_own_s3/objectstorage"
)

// HTTP 测试策略（按客户端 specification 划分）：
//   - 方法与路径：七种已定义操作，以及空桶名、空 key、含斜杠的 key。
//   - 资源状态：桶不存在/新建/已存在；对象不存在/新建/覆盖/删除。
//   - 输入：桶名与 key 的合法边界和非法形式；对象内容为空/非空。
//   - 响应：各操作的成功状态、响应头和内容；各业务错误与未预期错误的状态码和 JSON 错误名。
// 各测试只组合覆盖这些分区的代表情况，不枚举所有组合。

func testHTTPServer(t *testing.T) *httptest.Server {
	t.Helper()
	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldDir); err != nil {
			t.Errorf("恢复工作目录失败：%v", err)
		}
	})
	service, err := objectstorage.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Errorf("关闭存储失败：%v", err)
		}
	})
	server := httptest.NewServer(NewHandler(service))
	t.Cleanup(server.Close)
	return server
}

func httpRequest(t *testing.T, server *httptest.Server, method, path string, body io.Reader) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, server.URL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	content, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("%s %s 读取响应失败: %v", method, path, err)
	}
	return resp, content
}

func requireStatus(t *testing.T, resp *http.Response, content []byte, status int) {
	t.Helper()
	if resp.StatusCode != status {
		t.Fatalf("HTTP %d，期望 %d；响应体 %q", resp.StatusCode, status, content)
	}
}

func requireNoBody(t *testing.T, resp *http.Response, content []byte, status int) {
	t.Helper()
	requireStatus(t, resp, content, status)
	if len(content) != 0 {
		t.Fatalf("HTTP %d 应无响应体，得到 %q", status, content)
	}
}

func requireMediaType(t *testing.T, resp *http.Response, want string) {
	t.Helper()
	got, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || got != want {
		t.Fatalf("Content-Type = %q，期望 %q", resp.Header.Get("Content-Type"), want)
	}
}

func requireHTTPError(t *testing.T, resp *http.Response, content []byte, status int, name string) {
	t.Helper()
	requireStatus(t, resp, content, status)
	requireMediaType(t, resp, "application/json")
	var got struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(content, &got); err != nil || got.Error != name {
		t.Fatalf("错误响应 = %q，期望 error=%q (%v)", content, name, err)
	}
}

// 桶操作测试策略：无桶和多个桶、空桶和非空桶、缺失桶。
// 检查列表格式、删除生效及非空时的冲突响应。
func TestHTTPBucketOperations(t *testing.T) {
	server := testHTTPServer(t)
	checkBuckets := func(want ...string) {
		t.Helper()
		resp, body := httpRequest(t, server, http.MethodGet, "/", nil)
		requireStatus(t, resp, body, http.StatusOK)
		requireMediaType(t, resp, "application/json")
		var got struct {
			Buckets []string `json:"buckets"`
		}
		if err := json.Unmarshal(body, &got); err != nil || got.Buckets == nil || len(got.Buckets) != len(want) {
			t.Fatalf("桶列表 = %q (%v)，期望 %v", body, err, want)
		}
		for _, name := range want {
			if !slices.Contains(got.Buckets, name) {
				t.Fatalf("桶列表 = %v，缺少 %q", got.Buckets, name)
			}
		}
	}
	checkBuckets()
	for _, name := range []string{"photos", "documents"} {
		resp, body := httpRequest(t, server, http.MethodPut, "/"+name, nil)
		requireNoBody(t, resp, body, http.StatusCreated)
	}
	checkBuckets("photos", "documents")
	resp, body := httpRequest(t, server, http.MethodPut, "/photos/a.jpg", strings.NewReader("image"))
	requireNoBody(t, resp, body, http.StatusNoContent)
	resp, body = httpRequest(t, server, http.MethodDelete, "/photos", nil)
	requireHTTPError(t, resp, body, http.StatusConflict, "BucketNotEmpty")
	checkBuckets("photos", "documents")
	resp, body = httpRequest(t, server, http.MethodDelete, "/photos/a.jpg", nil)
	requireNoBody(t, resp, body, http.StatusNoContent)
	resp, body = httpRequest(t, server, http.MethodDelete, "/photos", nil)
	requireNoBody(t, resp, body, http.StatusNoContent)
	checkBuckets("documents")
	resp, body = httpRequest(t, server, http.MethodDelete, "/photos", nil)
	requireHTTPError(t, resp, body, http.StatusNotFound, "BucketNotExist")
}

// 正常响应测试策略：五种操作各走一次；检查创建后空桶可列出、
// 上传及覆盖后内容可下载、多对象列表、删除后不可下载和重复删除；
// 同时核对状态码、响应体、Content-Type 和 Content-Length。
func TestHTTPObjectFlow(t *testing.T) {
	server := testHTTPServer(t)
	resp, body := httpRequest(t, server, http.MethodPut, "/photos", nil)
	requireNoBody(t, resp, body, http.StatusCreated)

	resp, body = httpRequest(t, server, http.MethodGet, "/photos", nil)
	requireStatus(t, resp, body, http.StatusOK)
	requireMediaType(t, resp, "application/json")
	var list struct {
		Keys []string `json:"keys"`
	}
	if err := json.Unmarshal(body, &list); err != nil || list.Keys == nil || len(list.Keys) != 0 {
		t.Fatalf("空桶列表 = %q (%v)", body, err)
	}

	original := []byte{0, 0xff, '\n', 'a', 'b'}
	resp, body = httpRequest(t, server, http.MethodPut, "/photos/note.txt", bytes.NewReader(original))
	requireNoBody(t, resp, body, http.StatusNoContent)
	resp, body = httpRequest(t, server, http.MethodGet, "/photos/note.txt", nil)
	requireStatus(t, resp, body, http.StatusOK)
	requireMediaType(t, resp, "application/octet-stream")
	if resp.Header.Get("Content-Length") != "5" || !bytes.Equal(body, original) {
		t.Fatalf("下载 = (长度 %q, 内容 %v)，期望 (5, %v)", resp.Header.Get("Content-Length"), body, original)
	}

	resp, body = httpRequest(t, server, http.MethodPut, "/photos/note.txt", strings.NewReader("new"))
	requireNoBody(t, resp, body, http.StatusNoContent)
	resp, body = httpRequest(t, server, http.MethodGet, "/photos/note.txt", nil)
	requireStatus(t, resp, body, http.StatusOK)
	if resp.Header.Get("Content-Length") != "3" || string(body) != "new" {
		t.Fatalf("覆盖后下载 = (长度 %q, 内容 %q)", resp.Header.Get("Content-Length"), body)
	}
	resp, body = httpRequest(t, server, http.MethodPut, "/photos/second.txt", strings.NewReader("second"))
	requireNoBody(t, resp, body, http.StatusNoContent)
	resp, body = httpRequest(t, server, http.MethodGet, "/photos", nil)
	requireStatus(t, resp, body, http.StatusOK)
	if err := json.Unmarshal(body, &list); err != nil || len(list.Keys) != 2 || !slices.Contains(list.Keys, "note.txt") || !slices.Contains(list.Keys, "second.txt") {
		t.Fatalf("对象列表 = %q (%v)", body, err)
	}

	resp, body = httpRequest(t, server, http.MethodDelete, "/photos/note.txt", nil)
	requireNoBody(t, resp, body, http.StatusNoContent)
	resp, body = httpRequest(t, server, http.MethodDelete, "/photos/note.txt", nil)
	requireNoBody(t, resp, body, http.StatusNoContent)
	resp, body = httpRequest(t, server, http.MethodGet, "/photos/note.txt", nil)
	requireHTTPError(t, resp, body, http.StatusNotFound, "ObjectNotExist")
}

// 业务错误测试策略：按规格错误表分区，覆盖非法桶名、重复桶、缺桶、非法 key、
// 空上传和缺对象。
// 错误响应同时检查状态码、JSON 类型和错误名。
func TestHTTPErrors(t *testing.T) {
	server := testHTTPServer(t)
	for _, tc := range []struct {
		label, method, path string
		content             io.Reader
		status              int
		name                string
	}{
		{"创建桶名非法", http.MethodPut, "/Bad", nil, 400, "InvalidBucketName"},
		{"列出不存在的桶", http.MethodGet, "/missing", nil, 404, "BucketNotExist"},
		{"向不存在的桶上传", http.MethodPut, "/missing/note.txt", strings.NewReader("data"), 404, "BucketNotExist"},
		{"从不存在的桶下载", http.MethodGet, "/missing/note.txt", nil, 404, "BucketNotExist"},
		{"从不存在的桶删除", http.MethodDelete, "/missing/note.txt", nil, 404, "BucketNotExist"},
	} {
		t.Run(tc.label, func(t *testing.T) {
			resp, body := httpRequest(t, server, tc.method, tc.path, tc.content)
			requireHTTPError(t, resp, body, tc.status, tc.name)
		})
	}
	resp, body := httpRequest(t, server, http.MethodPut, "/files", nil)
	requireNoBody(t, resp, body, http.StatusCreated)
	resp, body = httpRequest(t, server, http.MethodPut, "/files", nil)
	requireHTTPError(t, resp, body, http.StatusConflict, "BucketNameExist")

	resp, body = httpRequest(t, server, http.MethodPut, "/files/.hidden", strings.NewReader("data"))
	requireHTTPError(t, resp, body, http.StatusBadRequest, "InvalidObjectKey")
	resp, body = httpRequest(t, server, http.MethodGet, "/files/.hidden", nil)
	requireHTTPError(t, resp, body, http.StatusBadRequest, "InvalidObjectKey")
	resp, body = httpRequest(t, server, http.MethodDelete, "/files/.hidden", nil)
	requireHTTPError(t, resp, body, http.StatusBadRequest, "InvalidObjectKey")
	resp, body = httpRequest(t, server, http.MethodPut, "/files/note.txt", nil)
	requireHTTPError(t, resp, body, http.StatusBadRequest, "EmptyObject")
	resp, body = httpRequest(t, server, http.MethodGet, "/files/absent", nil)
	requireHTTPError(t, resp, body, http.StatusNotFound, "ObjectNotExist")
}

// 输入测试策略：桶名和 key 各取合法边界、合法字符组合、非法边界与非法字符；
// 路径中的重复斜杠和点段也按非法 key 处理，不由路由器改写路径；
// 对 key 的无效输入分别走上传、下载、删除路径，验证路由与参数错误的映射。
func TestHTTPInputRules(t *testing.T) {
	server := testHTTPServer(t)
	resp, body := httpRequest(t, server, http.MethodPut, "/", nil)
	requireHTTPError(t, resp, body, http.StatusBadRequest, "InvalidBucketName")
	for _, name := range []string{"a", strings.Repeat("a", 63), "a-b"} {
		t.Run("合法桶名/"+name, func(t *testing.T) {
			resp, body := httpRequest(t, server, http.MethodPut, "/"+name, nil)
			requireNoBody(t, resp, body, http.StatusCreated)
		})
	}
	for _, tc := range []struct{ label, name string }{
		{"过长", strings.Repeat("a", 64)},
		{"大写", "Bad"},
		{"非法字符", "a_b"},
		{"首字符为连字符", "-a"},
		{"尾字符为连字符", "a-"},
	} {
		t.Run("非法桶名/"+tc.label, func(t *testing.T) {
			resp, body := httpRequest(t, server, http.MethodPut, "/"+tc.name, nil)
			requireHTTPError(t, resp, body, http.StatusBadRequest, "InvalidBucketName")
		})
	}
	resp, body = httpRequest(t, server, http.MethodPut, "/keys", nil)
	requireNoBody(t, resp, body, http.StatusCreated)
	for _, key := range []string{"a", strings.Repeat("a", 255), "a.z_9-A"} {
		t.Run("合法key/"+key, func(t *testing.T) {
			resp, body := httpRequest(t, server, http.MethodPut, "/keys/"+key, strings.NewReader("x"))
			requireNoBody(t, resp, body, http.StatusNoContent)
			resp, body = httpRequest(t, server, http.MethodGet, "/keys/"+key, nil)
			requireStatus(t, resp, body, http.StatusOK)
			if string(body) != "x" {
				t.Fatalf("下载内容 = %q，期望 x", body)
			}
		})
	}
	for _, tc := range []struct{ label, key string }{
		{"空", ""},
		{"过长", strings.Repeat("a", 256)},
		{"首字符为点", ".hidden"},
		{"非法字符", "a!b"},
		{"包含斜杠", "a/b"},
		{"重复斜杠", "/a"},
		{"点段", "."},
	} {
		t.Run("非法key/"+tc.label, func(t *testing.T) {
			for _, method := range []string{http.MethodPut, http.MethodGet, http.MethodDelete} {
				var content io.Reader
				if method == http.MethodPut {
					content = strings.NewReader("x")
				}
				resp, body := httpRequest(t, server, method, "/keys/"+tc.key, content)
				requireHTTPError(t, resp, body, http.StatusBadRequest, "InvalidObjectKey")
			}
		})
	}
}

// 未预期错误测试策略：让存储返回错误，逐一检查五种 HTTP 操作均映射为 500。
func TestHTTPStorageError(t *testing.T) {
	server := httptest.NewServer(NewHandler(errorService{}))
	t.Cleanup(server.Close)
	for _, tc := range []struct{ method, path string }{
		{http.MethodPut, "/files"},
		{http.MethodGet, "/"},
		{http.MethodDelete, "/files"},
		{http.MethodGet, "/files"},
		{http.MethodPut, "/files/a"},
		{http.MethodGet, "/files/a"},
		{http.MethodDelete, "/files/a"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			var content io.Reader
			if tc.method == http.MethodPut && tc.path == "/files/a" {
				content = strings.NewReader("x")
			}
			resp, body := httpRequest(t, server, tc.method, tc.path, content)
			requireHTTPError(t, resp, body, http.StatusInternalServerError, "InternalError")
		})
	}
}

type errorService struct{}

func (errorService) CreateBucket(string) (string, error) { return "", errors.New("storage failed") }
func (errorService) ListBuckets() ([]string, error)      { return nil, errors.New("storage failed") }
func (errorService) DeleteBucket(string) error           { return errors.New("storage failed") }
func (errorService) PutObject(string, string, io.Reader) error {
	return errors.New("storage failed")
}
func (errorService) GetObject(string, string) (io.ReadCloser, int64, error) {
	return nil, 0, errors.New("storage failed")
}
func (errorService) DeleteObject(string, string) error { return errors.New("storage failed") }
func (errorService) ListObjects(string) ([]string, error) {
	return nil, errors.New("storage failed")
}
