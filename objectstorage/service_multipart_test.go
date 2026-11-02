package objectstorage

import (
	"bytes"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"testing/iotest"
)

// CreateMultipartUpload 测试策略：新 key 和已有 key；桶不存在、非法 key、总片数为零各取代表。
// 检查唯一 ID、空任务和对象可见状态；名称长度等边界由普通对象测试覆盖。
func TestCreateMultipartUpload(t *testing.T) {
	s := setupTestStore(t)
	if _, err := s.CreateBucket("photos"); err != nil {
		t.Fatal(err)
	}
	if err := s.PutObject("photos", "old.txt", strings.NewReader("old")); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, tc := range []struct {
		key   string
		count int
	}{{"new.txt", 1}, {"old.txt", 2}} {
		id := createTestUpload(t, s, "photos", tc.key, tc.count)
		if slices.Contains(ids, id) {
			t.Fatalf("任务 ID 重复：%q", id)
		}
		ids = append(ids, id)
		requireParts(t, s, "photos", tc.key, id, nil)
	}
	for _, tc := range []struct {
		name, bucket, key string
		count             int
		want              error
	}{
		{"桶不存在", "missing", "new.txt", 1, BucketNotExistErr},
		{"非法key", "photos", "a/b", 1, InvalidObjectKeyErr},
		{"总片数为零", "photos", "new.txt", 0, InvalidMultipartRequestErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, err := s.CreateMultipartUpload(tc.bucket, tc.key, tc.count)
			if id != "" || !errors.Is(err, tc.want) {
				t.Fatalf("CreateMultipartUpload = (%q, %v)，期望 (空字符串, %v)", id, err, tc.want)
			}
		})
	}
	requireParts(t, s, "photos", "new.txt", ids[0], nil)
	requireParts(t, s, "photos", "old.txt", ids[1], nil)
	requireObjectContent(t, s, "photos", "old.txt", []byte("old"))
	keys, err := s.ListObjects("photos")
	if err != nil || !slices.Equal(keys, []string{"old.txt"}) {
		t.Fatalf("创建任务后的对象列表 = (%v, %v)，期望 [old.txt]", keys, err)
	}
}

// UploadPart 测试策略：编号首尾和中间、乱序上传、不同大小与二进制内容、同编号替换。
// 用查询检查编号和实际大小，用完成后的内容检查替换仅影响目标分片。
func TestUploadPart(t *testing.T) {
	s := setupTestStore(t)
	if _, err := s.CreateBucket("photos"); err != nil {
		t.Fatal(err)
	}
	if err := s.PutObject("photos", "note.txt", strings.NewReader("old")); err != nil {
		t.Fatal(err)
	}
	id := createTestUpload(t, s, "photos", "note.txt", 3)
	uploadTestPart(t, s, "photos", "note.txt", id, 3, []byte("old-tail"))
	uploadTestPart(t, s, "photos", "note.txt", id, 1, []byte{0, 0xff})
	uploadTestPart(t, s, "photos", "note.txt", id, 2, []byte("middle"))
	uploadTestPart(t, s, "photos", "note.txt", id, 3, []byte("!"))
	requireParts(t, s, "photos", "note.txt", id, []PartInfo{{1, 2}, {2, 6}, {3, 1}})
	requireObjectContent(t, s, "photos", "note.txt", []byte("old"))
	if err := s.CompleteMultipartUpload("photos", "note.txt", id); err != nil {
		t.Fatal(err)
	}
	requireObjectContent(t, s, "photos", "note.txt", append([]byte{0, 0xff}, []byte("middle!")...))
}

// 分片上传失败测试策略：编号越界、空内容、替换时读取中途失败各取代表。
// 检查零值返回、分片列表不变，完成后仍使用原分片的字节。
func TestUploadPartFailure(t *testing.T) {
	broken := io.MultiReader(strings.NewReader("partial"), iotest.ErrReader(errors.New("read failed")))
	for _, tc := range []struct {
		name    string
		number  int
		content io.Reader
		want    error
	}{
		{"零编号", 0, strings.NewReader("bad"), InvalidPartErr},
		{"超过总片数", 3, strings.NewReader("bad"), InvalidPartErr},
		{"空分片", 1, strings.NewReader(""), InvalidPartErr},
		{"读取失败", 1, broken, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := setupTestStore(t)
			if _, err := s.CreateBucket("photos"); err != nil {
				t.Fatal(err)
			}
			id := createTestUpload(t, s, "photos", "note.txt", 2)
			uploadTestPart(t, s, "photos", "note.txt", id, 1, []byte("old"))
			part, err := s.UploadPart("photos", "note.txt", id, tc.number, tc.content)
			if part != (PartInfo{}) || err == nil || tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("UploadPart = (%v, %v)，期望零值和错误 %v", part, err, tc.want)
			}
			requireParts(t, s, "photos", "note.txt", id, []PartInfo{{1, 3}})
			uploadTestPart(t, s, "photos", "note.txt", id, 2, []byte("tail"))
			if err := s.CompleteMultipartUpload("photos", "note.txt", id); err != nil {
				t.Fatal(err)
			}
			requireObjectContent(t, s, "photos", "note.txt", []byte("oldtail"))
		})
	}
}

// ListParts 测试策略：空任务、乱序保存后的部分分片和目标不匹配。
// 两个任务依次完成，检查有序列表和实际内容，验证同一 key 的任务隔离。
func TestListParts(t *testing.T) {
	s := setupTestStore(t)
	if _, err := s.CreateBucket("photos"); err != nil {
		t.Fatal(err)
	}
	id := createTestUpload(t, s, "photos", "note.txt", 3)
	other := createTestUpload(t, s, "photos", "note.txt", 1)
	requireParts(t, s, "photos", "note.txt", id, nil)
	uploadTestPart(t, s, "photos", "note.txt", id, 3, []byte("tail"))
	uploadTestPart(t, s, "photos", "note.txt", id, 1, []byte("a"))
	uploadTestPart(t, s, "photos", "note.txt", other, 1, []byte("other"))
	requireParts(t, s, "photos", "note.txt", id, []PartInfo{{1, 1}, {3, 4}})
	requireParts(t, s, "photos", "note.txt", other, []PartInfo{{1, 5}})
	stream, size, err := s.GetObject("photos", "note.txt")
	keys, listErr := s.ListObjects("photos")
	if stream != nil || size != 0 || !errors.Is(err, ObjectNotExistErr) ||
		listErr != nil || keys == nil || len(keys) != 0 {
		t.Fatalf("未完成对象 GetObject = (%v, %d, %v)，ListObjects = (%v, %v)",
			stream, size, err, keys, listErr)
	}
	parts, err := s.ListParts("photos", "other.txt", id)
	if parts != nil || !errors.Is(err, UploadNotExistErr) {
		t.Fatalf("目标不匹配时 ListParts = (%v, %v)", parts, err)
	}
	if err := s.CompleteMultipartUpload("photos", "note.txt", other); err != nil {
		t.Fatal(err)
	}
	requireObjectContent(t, s, "photos", "note.txt", []byte("other"))
	uploadTestPart(t, s, "photos", "note.txt", id, 2, []byte("b"))
	if err := s.CompleteMultipartUpload("photos", "note.txt", id); err != nil {
		t.Fatal(err)
	}
	requireObjectContent(t, s, "photos", "note.txt", []byte("abtail"))
}

// CompleteMultipartUpload 测试策略：已有对象上先缺尾片，补齐后完成。
// 检查缺片时保留任务和原对象，完成后按序覆盖、更新列表并使任务失效。
func TestCompleteMultipartUpload(t *testing.T) {
	s := setupTestStore(t)
	if _, err := s.CreateBucket("photos"); err != nil {
		t.Fatal(err)
	}
	if err := s.PutObject("photos", "note.txt", strings.NewReader("old")); err != nil {
		t.Fatal(err)
	}
	id := createTestUpload(t, s, "photos", "note.txt", 2)
	uploadTestPart(t, s, "photos", "note.txt", id, 1, []byte("new"))
	if err := s.CompleteMultipartUpload("photos", "note.txt", id); !errors.Is(err, IncompleteUploadErr) {
		t.Fatalf("缺尾片时完成返回 %v，期望 IncompleteUploadErr", err)
	}
	requireParts(t, s, "photos", "note.txt", id, []PartInfo{{1, 3}})
	requireObjectContent(t, s, "photos", "note.txt", []byte("old"))
	uploadTestPart(t, s, "photos", "note.txt", id, 2, []byte("content"))
	if err := s.CompleteMultipartUpload("photos", "note.txt", id); err != nil {
		t.Fatal(err)
	}
	requireObjectContent(t, s, "photos", "note.txt", []byte("newcontent"))
	keys, err := s.ListObjects("photos")
	if err != nil || !slices.Equal(keys, []string{"note.txt"}) {
		t.Fatalf("完成后 ListObjects = (%v, %v)", keys, err)
	}
	requireUploadUnavailable(t, s, "photos", "note.txt", id)
}

// AbortMultipartUpload 测试策略：取消已有分片的任务，检查任务失效，原对象和另一个任务保留。
func TestAbortMultipartUpload(t *testing.T) {
	s := setupTestStore(t)
	if _, err := s.CreateBucket("photos"); err != nil {
		t.Fatal(err)
	}
	if err := s.PutObject("photos", "note.txt", strings.NewReader("old")); err != nil {
		t.Fatal(err)
	}
	id := createTestUpload(t, s, "photos", "note.txt", 1)
	other := createTestUpload(t, s, "photos", "note.txt", 1)
	uploadTestPart(t, s, "photos", "note.txt", id, 1, []byte("discard"))
	if err := s.AbortMultipartUpload("photos", "note.txt", id); err != nil {
		t.Fatal(err)
	}
	requireUploadUnavailable(t, s, "photos", "note.txt", id)
	requireParts(t, s, "photos", "note.txt", other, nil)
	requireObjectContent(t, s, "photos", "note.txt", []byte("old"))
}

// 删桶测试策略：仅有未完成任务的桶可删除，重建同名桶后旧任务仍失效。
func TestDeleteBucketWithMultipartUploads(t *testing.T) {
	s := setupTestStore(t)
	if _, err := s.CreateBucket("photos"); err != nil {
		t.Fatal(err)
	}
	id := createTestUpload(t, s, "photos", "note.txt", 1)
	uploadTestPart(t, s, "photos", "note.txt", id, 1, []byte("part"))
	if err := s.DeleteBucket("photos"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateBucket("photos"); err != nil {
		t.Fatal(err)
	}
	parts, err := s.ListParts("photos", "note.txt", id)
	if parts != nil || !errors.Is(err, UploadNotExistErr) {
		t.Fatalf("重建桶后旧任务 ListParts = (%v, %v)", parts, err)
	}
}

// 分片持久化测试策略：保留部分上传的任务，重开后补齐并完成，再重开检查对象。
func TestMultipartPersistence(t *testing.T) {
	useTempWorkingDir(t)
	var s *Service
	reopen := func() {
		t.Helper()
		if s != nil {
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s = nil
		}
		var err error
		s, err = Open()
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		if s != nil {
			if err := s.Close(); err != nil {
				t.Errorf("Close 返回错误：%v", err)
			}
		}
	})
	reopen()
	if _, err := s.CreateBucket("photos"); err != nil {
		t.Fatal(err)
	}
	id := createTestUpload(t, s, "photos", "note.txt", 2)
	uploadTestPart(t, s, "photos", "note.txt", id, 2, []byte("tail"))
	reopen()
	requireParts(t, s, "photos", "note.txt", id, []PartInfo{{2, 4}})
	uploadTestPart(t, s, "photos", "note.txt", id, 1, []byte("head"))
	if err := s.CompleteMultipartUpload("photos", "note.txt", id); err != nil {
		t.Fatal(err)
	}
	reopen()
	requireObjectContent(t, s, "photos", "note.txt", []byte("headtail"))
	parts, err := s.ListParts("photos", "note.txt", id)
	if parts != nil || !errors.Is(err, UploadNotExistErr) {
		t.Fatalf("重开后已完成任务 ListParts = (%v, %v)", parts, err)
	}
}

func createTestUpload(t *testing.T, s *Service, bucket, key string, count int) string {
	t.Helper()
	id, err := s.CreateMultipartUpload(bucket, key, count)
	if err != nil || id == "" {
		t.Fatalf("CreateMultipartUpload(%q, %q, %d) = (%q, %v)", bucket, key, count, id, err)
	}
	return id
}

func uploadTestPart(t *testing.T, s *Service, bucket, key, id string, number int, content []byte) {
	t.Helper()
	part, err := s.UploadPart(bucket, key, id, number, bytes.NewReader(content))
	want := PartInfo{PartNumber: number, Size: int64(len(content))}
	if err != nil || part != want {
		t.Fatalf("UploadPart(%d) = (%v, %v)，期望 (%v, nil)", number, part, err, want)
	}
}

func requireParts(t *testing.T, s *Service, bucket, key, id string, want []PartInfo) {
	t.Helper()
	parts, err := s.ListParts(bucket, key, id)
	if err != nil || parts == nil || !slices.Equal(parts, want) {
		t.Fatalf("ListParts(%q) = (%v, %v)，期望非 nil 切片 %v", id, parts, err, want)
	}
}

func requireUploadUnavailable(t *testing.T, s *Service, bucket, key, id string) {
	t.Helper()
	part, err := s.UploadPart(bucket, key, id, 1, strings.NewReader("unused"))
	if part != (PartInfo{}) || !errors.Is(err, UploadNotExistErr) {
		t.Fatalf("失效任务 UploadPart = (%v, %v)", part, err)
	}
	parts, err := s.ListParts(bucket, key, id)
	if parts != nil || !errors.Is(err, UploadNotExistErr) {
		t.Fatalf("失效任务 ListParts = (%v, %v)", parts, err)
	}
	if err := s.CompleteMultipartUpload(bucket, key, id); !errors.Is(err, UploadNotExistErr) {
		t.Fatalf("失效任务 CompleteMultipartUpload = %v", err)
	}
	if err := s.AbortMultipartUpload(bucket, key, id); !errors.Is(err, UploadNotExistErr) {
		t.Fatalf("失效任务 AbortMultipartUpload = %v", err)
	}
}
