package objectstorage

import (
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"
)

// 不同对象测试策略：同桶不同 key、不同桶相同 key。
// 暂停一个对象的上传，检查另一个对象仍能完成普通上传、分片发布、读取和删除。
func TestServiceDifferentObjectsProgressIndependently(t *testing.T) {
	for _, tc := range []struct {
		name, bucket, key string
	}{
		{"同桶不同key", "photos", "other.txt"},
		{"不同桶相同key", "documents", "note.txt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := setupTestStore(t)
			for _, bucket := range []string{"photos", "documents"} {
				if _, err := s.CreateBucket(bucket); err != nil {
					t.Fatal(err)
				}
			}
			id := createTestUpload(t, s, tc.bucket, tc.key, 1)
			uploadTestPart(t, s, tc.bucket, tc.key, id, 1, []byte("other"))
			release, upload := pauseServiceUpload(t, s, "photos", "note.txt", "", 0, "first")
			other := make(chan error, 1)
			var stream io.ReadCloser
			var size int64
			t.Cleanup(func() {
				release()
				waitResult(t, other)
				if stream != nil {
					_ = stream.Close()
				}
			})
			go func() {
				defer close(other)
				if err := s.PutObject(tc.bucket, tc.key, strings.NewReader("other")); err != nil {
					other <- err
					return
				}
				if err := s.CompleteMultipartUpload(tc.bucket, tc.key, id); err != nil {
					other <- err
					return
				}
				var err error
				stream, size, err = s.GetObject(tc.bucket, tc.key)
				if err == nil {
					err = s.DeleteObject(tc.bucket, tc.key)
				}
				other <- err
			}()
			waitResult(t, other)
			requireStreamContent(t, stream, size, []byte("other"))
			requireServiceObjectMissing(t, s, tc.bucket, tc.key)
			release()
			waitResult(t, upload)
			requireObjectContent(t, s, "photos", "note.txt", []byte("first"))
		})
	}
}

// 同一对象测试策略：上传执行中，分别再调用上传、读取、删除和完成分片上传。
// 用输入流暂停前一次上传，检查后一次调用等待，且完成后的结果符合串行执行。
func TestServiceSameObjectCallsWait(t *testing.T) {
	for _, operation := range []string{"PutObject", "GetObject", "DeleteObject", "CompleteMultipartUpload"} {
		t.Run(operation, func(t *testing.T) {
			s := setupTestStore(t)
			if _, err := s.CreateBucket("photos"); err != nil {
				t.Fatal(err)
			}
			if err := s.PutObject("photos", "note.txt", strings.NewReader("old")); err != nil {
				t.Fatal(err)
			}
			var id string
			if operation == "CompleteMultipartUpload" {
				id = createTestUpload(t, s, "photos", "note.txt", 1)
				uploadTestPart(t, s, "photos", "note.txt", id, 1, []byte("second"))
			}
			release, upload := pauseServiceUpload(t, s, "photos", "note.txt", "", 0, "first")
			started := make(chan struct{})
			next := make(chan error, 1)
			var stream io.ReadCloser
			var size int64
			t.Cleanup(func() {
				release()
				waitResult(t, next)
				if stream != nil {
					_ = stream.Close()
				}
			})
			go func() {
				defer close(next)
				close(started)
				switch operation {
				case "PutObject":
					next <- s.PutObject("photos", "note.txt", strings.NewReader("second"))
				case "GetObject":
					var err error
					stream, size, err = s.GetObject("photos", "note.txt")
					next <- err
				case "CompleteMultipartUpload":
					next <- s.CompleteMultipartUpload("photos", "note.txt", id)
				default:
					next <- s.DeleteObject("photos", "note.txt")
				}
			}()
			<-started
			select {
			case err := <-next:
				t.Errorf("%s 在前一次上传结束前返回：%v", operation, err)
			case <-time.After(100 * time.Millisecond):
			}
			release()
			waitResult(t, upload)
			waitResult(t, next)
			switch operation {
			case "PutObject", "CompleteMultipartUpload":
				requireObjectContent(t, s, "photos", "note.txt", []byte("second"))
			case "GetObject":
				requireStreamContent(t, stream, size, []byte("first"))
			case "DeleteObject":
				requireServiceObjectMissing(t, s, "photos", "note.txt")
			}
		})
	}
}

// 读取流测试策略：单文件和多文件对象，读取一部分后覆盖或删除同一对象。
// 检查后续方法无需等待流关闭，原流仍返回原版本的剩余内容和大小。
func TestServiceOpenStreamKeepsVersion(t *testing.T) {
	for _, tc := range []struct {
		name, operation string
		contents        []string
	}{
		{"单文件覆盖", "覆盖", []string{"old-content"}},
		{"单文件删除", "删除", []string{"old-content"}},
		{"多文件覆盖", "覆盖", []string{"old-", "content"}},
		{"多文件删除", "删除", []string{"old-", "content"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := setupTestStore(t)
			if _, err := s.CreateBucket("photos"); err != nil {
				t.Fatal(err)
			}
			original := strings.Join(tc.contents, "")
			if len(tc.contents) == 1 {
				if err := s.PutObject("photos", "note.txt", strings.NewReader(original)); err != nil {
					t.Fatal(err)
				}
			} else {
				var files []objectFile
				for _, content := range tc.contents {
					name, size, err := s.files.write(strings.NewReader(content))
					if err != nil {
						t.Fatal(err)
					}
					files = append(files, objectFile{Name: name, Size: size})
				}
				if err := s.db.setObject("photos", "note.txt", files, int64(len(original))); err != nil {
					t.Fatal(err)
				}
			}
			stream, size, err := s.GetObject("photos", "note.txt")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = stream.Close() })
			prefix := make([]byte, 3)
			if _, err := io.ReadFull(stream, prefix); err != nil {
				t.Fatal(err)
			}
			change := make(chan error, 1)
			t.Cleanup(func() {
				_ = stream.Close()
				waitResult(t, change)
			})
			go func() {
				defer close(change)
				if tc.operation == "覆盖" {
					change <- s.PutObject("photos", "note.txt", strings.NewReader("new"))
				} else {
					change <- s.DeleteObject("photos", "note.txt")
				}
			}()
			waitResult(t, change)
			rest, err := io.ReadAll(stream)
			if err != nil || string(prefix)+string(rest) != original || size != int64(len(original)) {
				t.Fatalf("原流 = (%q, %d, %v)，期望 (%q, %d, nil)", string(prefix)+string(rest), size, err, original, len(original))
			}
			if tc.operation == "覆盖" {
				requireObjectContent(t, s, "photos", "note.txt", []byte("new"))
			} else {
				requireServiceObjectMissing(t, s, "photos", "note.txt")
			}
		})
	}
}

// 列表并发测试策略：暂停新对象的上传，检查列表无需等待上传，且仅包含已有 key；
// 上传返回后再次查询，检查列表包含全部 key、无重复，不断言排列顺序。
func TestServiceListObjectsDuringUpload(t *testing.T) {
	s := setupTestStore(t)
	if _, err := s.CreateBucket("photos"); err != nil {
		t.Fatal(err)
	}
	if err := s.PutObject("photos", "keep.txt", strings.NewReader("keep")); err != nil {
		t.Fatal(err)
	}
	release, upload := pauseServiceUpload(t, s, "photos", "new.txt", "", 0, "new")
	var keys []string
	list := make(chan error, 1)
	t.Cleanup(func() {
		release()
		waitResult(t, list)
	})
	go func() {
		defer close(list)
		var err error
		keys, err = s.ListObjects("photos")
		list <- err
	}()
	waitResult(t, list)
	if !slices.Equal(keys, []string{"keep.txt"}) {
		t.Fatalf("上传执行中的列表 = %v，期望 [keep.txt]", keys)
	}
	release()
	waitResult(t, upload)
	keys, err := s.ListObjects("photos")
	slices.Sort(keys)
	if err != nil || !slices.Equal(keys, []string{"keep.txt", "new.txt"}) {
		t.Fatalf("上传完成后的列表 = (%v, %v)，期望 [keep.txt new.txt]", keys, err)
	}
}

// 不同分片测试策略：暂停同一任务的第一片，检查第二片和列表查询无需等待。
// 暂停期间列表只包含已保存的第二片；全部上传后按编号完成，检查实际内容。
func TestServiceDifferentPartsProgressIndependently(t *testing.T) {
	s := setupTestStore(t)
	if _, err := s.CreateBucket("photos"); err != nil {
		t.Fatal(err)
	}
	id := createTestUpload(t, s, "photos", "note.txt", 2)
	release, upload := pauseServiceUpload(t, s, "photos", "note.txt", id, 1, "head")
	var parts []PartInfo
	other := make(chan error, 1)
	t.Cleanup(func() {
		release()
		waitResult(t, other)
	})
	go func() {
		defer close(other)
		if _, err := s.UploadPart("photos", "note.txt", id, 2, strings.NewReader("tail")); err != nil {
			other <- err
			return
		}
		var err error
		parts, err = s.ListParts("photos", "note.txt", id)
		other <- err
	}()
	waitResult(t, other)
	if !slices.Equal(parts, []PartInfo{{2, 4}}) {
		t.Fatalf("上传执行中的分片列表 = %v，期望 [{2 4}]", parts)
	}
	release()
	waitResult(t, upload)
	requireParts(t, s, "photos", "note.txt", id, []PartInfo{{1, 4}, {2, 4}})
	if err := s.CompleteMultipartUpload("photos", "note.txt", id); err != nil {
		t.Fatal(err)
	}
	requireObjectContent(t, s, "photos", "note.txt", []byte("headtail"))
}

// 同一分片测试策略：暂停一次替换，再查询列表并发起同编号替换。
// 列表仍显示旧片，后一次上传必须等待；全部结束后发布，检查最后一次上传的内容。
func TestServiceSamePartUploadsWait(t *testing.T) {
	s := setupTestStore(t)
	if _, err := s.CreateBucket("photos"); err != nil {
		t.Fatal(err)
	}
	id := createTestUpload(t, s, "photos", "note.txt", 1)
	uploadTestPart(t, s, "photos", "note.txt", id, 1, []byte("old"))
	release, upload := pauseServiceUpload(t, s, "photos", "note.txt", id, 1, "first")
	var parts []PartInfo
	listed := make(chan error, 1)
	next := make(chan error, 1)
	t.Cleanup(func() {
		release()
		waitResult(t, next)
	})
	go func() {
		defer close(next)
		defer close(listed)
		var err error
		parts, err = s.ListParts("photos", "note.txt", id)
		listed <- err
		if err != nil {
			return
		}
		_, err = s.UploadPart("photos", "note.txt", id, 1, strings.NewReader("second"))
		next <- err
	}()
	waitResult(t, listed)
	if !slices.Equal(parts, []PartInfo{{1, 3}}) {
		t.Fatalf("替换执行中的分片列表 = %v，期望 [{1 3}]", parts)
	}
	select {
	case err := <-next:
		t.Errorf("同一分片的后一次上传在前一次结束前返回：%v", err)
	case <-time.After(100 * time.Millisecond):
	}
	release()
	waitResult(t, upload)
	waitResult(t, next)
	if err := s.CompleteMultipartUpload("photos", "note.txt", id); err != nil {
		t.Fatal(err)
	}
	requireObjectContent(t, s, "photos", "note.txt", []byte("second"))
}

func waitResult(t *testing.T, result <-chan error) {
	t.Helper()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("5 秒内未收到任务结果")
	}
}

// 写入一段内容但不关闭管道，让上传停在等待后续输入的位置。
func pauseServiceUpload(t *testing.T, s *Service, bucket, key, uploadID string, partNumber int, content string) (func(), <-chan error) {
	t.Helper()
	reader, writer := io.Pipe()
	upload, written := make(chan error, 1), make(chan error, 1)
	release := func() { _ = writer.Close() }
	t.Cleanup(func() {
		release()
		waitResult(t, written)
		waitResult(t, upload)
	})
	go func() {
		defer close(upload)
		var err error
		if uploadID == "" {
			err = s.PutObject(bucket, key, reader)
		} else {
			_, err = s.UploadPart(bucket, key, uploadID, partNumber, reader)
		}
		_ = reader.CloseWithError(err)
		upload <- err
	}()
	go func() {
		defer close(written)
		_, err := io.WriteString(writer, content)
		written <- err
	}()
	waitResult(t, written)
	return release, upload
}

func requireServiceObjectMissing(t *testing.T, s *Service, bucket, key string) {
	t.Helper()
	stream, size, err := s.GetObject(bucket, key)
	if stream != nil {
		_ = stream.Close()
	}
	if stream != nil || size != 0 || !errors.Is(err, ObjectNotExistErr) {
		t.Fatalf("GetObject = (%v, %d, %v)，期望 (nil, 0, ObjectNotExistErr)", stream, size, err)
	}
}
