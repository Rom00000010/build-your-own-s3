package objectstorage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
	"time"
)

// Open 失败测试策略：让工作目录中的 data 路径无法作为目录使用，
// 检查返回 nil 实例和非 nil 错误。
func TestOpenFailure(t *testing.T) {
	useTempWorkingDir(t)
	if err := os.WriteFile("data", []byte("occupied"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Open()
	if s != nil || err == nil {
		t.Fatalf("Open = (%v, %v)，期望 (nil, 非 nil 错误)", s, err)
	}
}

// CreateBucket 测试策略：
//   - 长度：0、1、2–62、63、超过 63；字符：合法、非法。
//   - 首尾：合法、开头为连字符、结尾为连字符。
//   - 桶名：不存在、已存在。
//
// 检查成功时返回桶名且新桶为空，失败时返回指定错误。
func TestCreateBucket(t *testing.T) {
	s := setupTestStore(t)

	t.Run("合法名称创建空桶", func(t *testing.T) {
		cases := []struct {
			label string
			name  string
		}{
			{"单个字母", "a"},
			{"单个数字", "0"},
			{"普通长度且包含内部连字符", "photos-2026"},
			{"数字开头字母结尾", "1backup"},
			{"最大长度", strings.Repeat("b", 63)},
		}
		for _, tc := range cases {
			t.Run(tc.label, func(t *testing.T) {
				name, err := s.CreateBucket(tc.name)
				if err != nil || name != tc.name {
					t.Fatalf("CreateBucket(%q) = (%q, %v)，期望 (%q, nil)", tc.name, name, err, tc.name)
				}
				keys, err := s.ListObjects(tc.name)
				if err != nil || keys == nil || len(keys) != 0 {
					t.Fatalf("新桶应存在且为空，ListObjects = (%v, %v)", keys, err)
				}
			})
		}
	})

	t.Run("非法名称", func(t *testing.T) {
		cases := []struct {
			label string
			name  string
		}{
			{"空名称", ""},
			{"超过最大长度", strings.Repeat("c", 64)},
			{"大写字母", "Photos"},
			{"中文", "照片"},
			{"空格", "my photos"},
			{"下划线", "my_photos"},
			{"点", "my.photos"},
			{"开头连字符", "-photos"},
			{"结尾连字符", "photos-"},
		}
		for _, tc := range cases {
			t.Run(tc.label, func(t *testing.T) {
				name, err := s.CreateBucket(tc.name)
				if name != "" || !errors.Is(err, InvalidBucketNameErr) {
					t.Fatalf("CreateBucket(%q) = (%q, %v)，期望 (空字符串, InvalidBucketNameErr)", tc.name, name, err)
				}
			})
		}
	})

	t.Run("重复创建", func(t *testing.T) {
		const bucket = "existing-bucket"
		if _, err := s.CreateBucket(bucket); err != nil {
			t.Fatalf("准备已有桶失败：%v", err)
		}
		name, err := s.CreateBucket(bucket)
		if name != "" || !errors.Is(err, BucketNameExistErr) {
			t.Fatalf("重复创建 %q = (%q, %v)，期望 (空字符串, BucketNameExistErr)", bucket, name, err)
		}
	})
}

// ListBuckets 测试策略：空存储与多个桶；检查结果非 nil、无重复，且不改变状态。
func TestListBuckets(t *testing.T) {
	s := setupTestStore(t)
	names, err := s.ListBuckets()
	if err != nil || names == nil || len(names) != 0 {
		t.Fatalf("空存储 ListBuckets = (%v, %v)", names, err)
	}
	for _, name := range []string{"photos", "documents"} {
		if _, err := s.CreateBucket(name); err != nil {
			t.Fatal(err)
		}
	}
	names, err = s.ListBuckets()
	if err != nil || len(names) != 2 || !slices.Contains(names, "photos") || !slices.Contains(names, "documents") {
		t.Fatalf("ListBuckets = (%v, %v)", names, err)
	}
}

// DeleteBucket 测试策略：桶不存在、空桶和非空桶。
// 检查成功删除后桶不可访问；非空时保留桶和对象。
func TestDeleteBucket(t *testing.T) {
	s := setupTestStore(t)
	if err := s.DeleteBucket("missing"); !errors.Is(err, BucketNotExistErr) {
		t.Fatalf("删除不存在的桶 = %v", err)
	}
	if _, err := s.CreateBucket("empty"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteBucket("empty"); err != nil {
		t.Fatalf("删除空桶 = %v", err)
	}
	if _, err := s.ListObjects("empty"); !errors.Is(err, BucketNotExistErr) {
		t.Fatalf("删除后 ListObjects = %v", err)
	}
	if _, err := s.CreateBucket("full"); err != nil {
		t.Fatal(err)
	}
	if err := s.PutObject("full", "a.txt", strings.NewReader("a")); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteBucket("full"); !errors.Is(err, BucketNotEmptyErr) {
		t.Fatalf("删除非空桶 = %v", err)
	}
	requireObjectContent(t, s, "full", "a.txt", []byte("a"))
	if err := s.DeleteObject("full", "a.txt"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteBucket("full"); err != nil {
		t.Fatalf("清空后删除桶 = %v", err)
	}
	names, err := s.ListBuckets()
	if err != nil || len(names) != 0 {
		t.Fatalf("删除后桶列表 = (%v, %v)", names, err)
	}
}

// PutObject 测试策略：
//   - 桶：存在、不存在；key：合法、空、首字符非法、过长。
//   - 内容：非空、空 Reader、读取中途失败；对象：新 key、已有 key。
//
// 成功时用 GetObject 检查内容与大小；失败时检查原对象仍可读取。
func TestPutObject(t *testing.T) {
	s := setupTestStore(t)

	const bucket = "put-object-test"
	if _, err := s.CreateBucket(bucket); err != nil {
		t.Fatalf("准备桶失败：%v", err)
	}

	t.Run("新 key 上传", func(t *testing.T) {
		for _, key := range []string{"a", "note.txt", strings.Repeat("b", 255)} {
			content := []byte("hello")
			if err := s.PutObject(bucket, key, bytes.NewReader(content)); err != nil {
				t.Fatalf("PutObject(%q) 返回错误：%v", key, err)
			}
			requireObjectContent(t, s, bucket, key, content)
		}
	})

	t.Run("同 key 覆盖", func(t *testing.T) {
		const key = "overwrite.txt"
		if err := s.PutObject(bucket, key, strings.NewReader("old-long-content")); err != nil {
			t.Fatalf("准备原对象失败：%v", err)
		}
		if err := s.PutObject(bucket, key, strings.NewReader("new")); err != nil {
			t.Fatalf("覆盖对象失败：%v", err)
		}
		requireObjectContent(t, s, bucket, key, []byte("new"))
	})

	t.Run("读取失败不覆盖原对象", func(t *testing.T) {
		const key = "failed-overwrite.txt"
		if err := s.PutObject(bucket, key, strings.NewReader("old")); err != nil {
			t.Fatalf("准备原对象失败：%v", err)
		}
		broken := io.MultiReader(strings.NewReader("partial"), iotest.ErrReader(errors.New("read failed")))
		if err := s.PutObject(bucket, key, broken); err == nil {
			t.Fatal("读取失败时 PutObject 应返回错误")
		}
		requireObjectContent(t, s, bucket, key, []byte("old"))
	})

	t.Run("桶不存在", func(t *testing.T) {
		if err := s.PutObject("missing-put-bucket", "note.txt", strings.NewReader("hello")); !errors.Is(err, BucketNotExistErr) {
			t.Fatalf("桶不存在时返回 %v，期望 BucketNotExistErr", err)
		}
	})

	t.Run("非法 key", func(t *testing.T) {
		for _, key := range []string{"", ".hidden", "a/b", strings.Repeat("c", 256)} {
			if err := s.PutObject(bucket, key, strings.NewReader("hello")); !errors.Is(err, InvalidObjectKeyErr) {
				t.Fatalf("PutObject(%q) 返回 %v，期望 InvalidObjectKeyErr", key, err)
			}
		}
	})

	t.Run("空内容", func(t *testing.T) {
		const key = "empty-content"
		for _, overwrite := range []bool{false, true} {
			if overwrite {
				if err := s.PutObject(bucket, key, strings.NewReader("old")); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.PutObject(bucket, key, bytes.NewReader(nil)); err != nil {
				t.Fatalf("上传空内容失败：%v", err)
			}
			requireObjectContent(t, s, bucket, key, nil)
		}
	})
}

// 异步清理测试策略：覆盖一个已有对象，阻塞旧文件删除，检查请求已返回且新内容可读；
// 放行删除后检查旧文件消失，新对象仍可读取。
func TestServiceOverwriteCleansOldFileAsynchronously(t *testing.T) {
	s := setupTestStore(t)
	if _, err := s.CreateBucket("photos"); err != nil {
		t.Fatal(err)
	}
	if err := s.PutObject("photos", "note.txt", strings.NewReader("old")); err != nil {
		t.Fatal(err)
	}
	files, _, _, err := s.db.getObject("photos", "note.txt")
	if err != nil || len(files) != 1 {
		t.Fatalf("原对象文件 = (%v, %v)，期望一个文件", files, err)
	}
	oldName := files[0].Name
	started := make(chan struct{})
	released := make(chan struct{})
	cleaned := make(chan error, 1)
	release := sync.OnceFunc(func() { close(released) })
	remove := s.gc.remove
	s.gc.remove = func(name string) error {
		if name != oldName {
			return remove(name)
		}
		close(started)
		<-released
		err := remove(name)
		cleaned <- err
		return err
	}

	overwrite := make(chan error, 1)
	t.Cleanup(func() {
		release()
		waitResult(t, overwrite)
	})
	go func() {
		defer close(overwrite)
		overwrite <- s.PutObject("photos", "note.txt", strings.NewReader("new"))
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("后台未开始清理旧文件")
	}
	waitResult(t, overwrite)
	requireObjectContent(t, s, "photos", "note.txt", []byte("new"))
	if exists, err := s.files.bucket.Exists(context.Background(), oldName); err != nil || !exists {
		t.Fatalf("放行前旧文件存在 = (%t, %v)，期望 (true, nil)", exists, err)
	}

	release()
	waitResult(t, cleaned)
	if exists, err := s.files.bucket.Exists(context.Background(), oldName); err != nil || exists {
		t.Fatalf("清理后旧文件存在 = (%t, %v)，期望 (false, nil)", exists, err)
	}
	requireObjectContent(t, s, "photos", "note.txt", []byte("new"))
}

// GetObject 测试策略：桶存在或不存在；key 合法、非法或不存在。
// 成功时检查读取流、对象大小和内容，失败时检查 nil、0 和对应错误。
func TestGetObject(t *testing.T) {
	s := setupTestStore(t)

	const bucket = "get-object-test"
	const key = "note.txt"
	content := []byte("hello")
	if _, err := s.CreateBucket(bucket); err != nil {
		t.Fatalf("准备桶失败：%v", err)
	}
	if err := s.PutObject(bucket, key, bytes.NewReader(content)); err != nil {
		t.Fatalf("准备对象失败：%v", err)
	}

	t.Run("对象存在", func(t *testing.T) {
		requireObjectContent(t, s, bucket, key, content)
	})

	for _, tc := range []struct {
		label, bucket, key string
		err                error
	}{
		{"桶不存在", "missing-get-bucket", key, BucketNotExistErr},
		{"非法 key", bucket, ".hidden", InvalidObjectKeyErr},
		{"对象不存在", bucket, "missing.txt", ObjectNotExistErr},
	} {
		t.Run(tc.label, func(t *testing.T) {
			stream, size, err := s.GetObject(tc.bucket, tc.key)
			if stream != nil || size != 0 || !errors.Is(err, tc.err) {
				t.Fatalf("GetObject(%q, %q) = (%v, %d, %v)，期望 (nil, 0, %v)", tc.bucket, tc.key, stream, size, err, tc.err)
			}
		})
	}
}

// DeleteObject 测试策略：桶存在或不存在；key 合法或非法；对象存在或不存在。
// 检查删除后的对象不可获取，以及各情况下的返回值。
func TestDeleteObject(t *testing.T) {
	s := setupTestStore(t)

	const bucket = "delete-object-test"
	const key = "note.txt"
	if _, err := s.CreateBucket(bucket); err != nil {
		t.Fatalf("准备桶失败：%v", err)
	}
	if err := s.PutObject(bucket, key, strings.NewReader("hello")); err != nil {
		t.Fatalf("准备对象失败：%v", err)
	}

	t.Run("删除已有对象", func(t *testing.T) {
		if err := s.DeleteObject(bucket, key); err != nil {
			t.Fatalf("DeleteObject 返回错误：%v", err)
		}
		stream, size, err := s.GetObject(bucket, key)
		if stream != nil || size != 0 || !errors.Is(err, ObjectNotExistErr) {
			t.Fatalf("删除后 GetObject = (%v, %d, %v)，期望 (nil, 0, ObjectNotExistErr)", stream, size, err)
		}
	})

	t.Run("对象不存在", func(t *testing.T) {
		if err := s.DeleteObject(bucket, "missing.txt"); err != nil {
			t.Fatalf("删除不存在的对象返回 %v，期望 nil", err)
		}
	})

	t.Run("桶不存在", func(t *testing.T) {
		if err := s.DeleteObject("missing-delete-bucket", key); !errors.Is(err, BucketNotExistErr) {
			t.Fatalf("桶不存在时返回 %v，期望 BucketNotExistErr", err)
		}
	})

	t.Run("非法 key", func(t *testing.T) {
		if err := s.DeleteObject(bucket, ".hidden"); !errors.Is(err, InvalidObjectKeyErr) {
			t.Fatalf("非法 key 时返回 %v，期望 InvalidObjectKeyErr", err)
		}
	})
}

// ListObjects 测试策略：桶不存在、空桶、含多个对象的桶。
// 检查 key 完整且不重复；规格未规定顺序，因此按集合比较。
func TestListObjects(t *testing.T) {
	s := setupTestStore(t)

	const bucket = "list-objects-test"
	if _, err := s.CreateBucket(bucket); err != nil {
		t.Fatalf("准备桶失败：%v", err)
	}

	t.Run("空桶", func(t *testing.T) {
		keys, err := s.ListObjects(bucket)
		if err != nil || keys == nil || len(keys) != 0 {
			t.Fatalf("空桶 ListObjects = (%v, %v)，期望 (非 nil 空切片, nil)", keys, err)
		}
	})

	t.Run("多个对象", func(t *testing.T) {
		for _, key := range []string{"a.txt", "b.txt"} {
			if err := s.PutObject(bucket, key, strings.NewReader("data")); err != nil {
				t.Fatalf("准备对象 %q 失败：%v", key, err)
			}
		}
		keys, err := s.ListObjects(bucket)
		if err != nil || len(keys) != 2 || !slices.Contains(keys, "a.txt") || !slices.Contains(keys, "b.txt") {
			t.Fatalf("ListObjects = (%v, %v)，期望恰好包含 a.txt 和 b.txt", keys, err)
		}
	})

	t.Run("桶不存在", func(t *testing.T) {
		keys, err := s.ListObjects("missing-list-bucket")
		if keys != nil || !errors.Is(err, BucketNotExistErr) {
			t.Fatalf("ListObjects = (%v, %v)，期望 (nil, BucketNotExistErr)", keys, err)
		}
	})
}

// 持久化测试策略：创建、列出和删除桶，上传、覆盖和删除对象；让文件清理失败后重开，
// 检查业务状态仍正确，旧文件继续清理。
func TestPersistence(t *testing.T) {
	useTempWorkingDir(t)
	s, err := Open()
	if err != nil {
		t.Fatalf("首次 Open 返回错误：%v", err)
	}
	opened := true
	t.Cleanup(func() {
		if opened {
			if err := s.Close(); err != nil {
				t.Errorf("Close 返回错误：%v", err)
			}
		}
	})

	if _, err := s.CreateBucket("empty-bucket"); err != nil {
		t.Fatalf("创建空桶失败：%v", err)
	}
	if _, err := s.CreateBucket("files"); err != nil {
		t.Fatalf("创建对象桶失败：%v", err)
	}
	if _, err := s.CreateBucket("deleted-bucket"); err != nil {
		t.Fatalf("创建待删除桶失败：%v", err)
	}
	if err := s.DeleteBucket("deleted-bucket"); err != nil {
		t.Fatalf("删除待删除桶失败：%v", err)
	}
	if err := s.PutObject("files", "keep.txt", strings.NewReader("old")); err != nil {
		t.Fatalf("上传对象失败：%v", err)
	}
	oldFiles, _, _, err := s.db.getObject("files", "keep.txt")
	if err != nil || len(oldFiles) != 1 {
		t.Fatalf("原对象文件 = (%v, %v)，期望一个文件", oldFiles, err)
	}
	failed := make(chan struct{})
	markFailed := sync.OnceFunc(func() { close(failed) })
	s.gc.remove = func(string) error {
		markFailed()
		return errors.New("文件删除暂时失败")
	}
	if err := s.PutObject("files", "keep.txt", strings.NewReader("new")); err != nil {
		t.Fatalf("覆盖对象失败：%v", err)
	}
	if err := s.PutObject("files", "remove.txt", strings.NewReader("remove")); err != nil {
		t.Fatalf("上传待删除对象失败：%v", err)
	}
	if err := s.DeleteObject("files", "remove.txt"); err != nil {
		t.Fatalf("删除对象失败：%v", err)
	}
	select {
	case <-failed:
	case <-time.After(5 * time.Second):
		t.Fatal("后台未尝试清理文件")
	}
	if exists, err := s.files.bucket.Exists(context.Background(), oldFiles[0].Name); err != nil || !exists {
		t.Fatalf("重开前旧文件存在 = (%t, %v)，期望 (true, nil)", exists, err)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("首次 Close 返回错误：%v", err)
	}
	opened = false
	s, err = Open()
	if err != nil {
		t.Fatalf("再次 Open 返回错误：%v", err)
	}
	opened = true
	names, err := s.ListBuckets()
	if err != nil || len(names) != 2 || !slices.Contains(names, "empty-bucket") || !slices.Contains(names, "files") {
		t.Fatalf("重开后桶列表 = (%v, %v)", names, err)
	}

	if _, err := s.CreateBucket("empty-bucket"); !errors.Is(err, BucketNameExistErr) {
		t.Fatalf("重开后重复创建空桶返回 %v，期望 BucketNameExistErr", err)
	}
	keys, err := s.ListObjects("empty-bucket")
	if err != nil || keys == nil || len(keys) != 0 {
		t.Fatalf("重开后空桶 ListObjects = (%v, %v)，期望非 nil 空切片", keys, err)
	}
	requireObjectContent(t, s, "files", "keep.txt", []byte("new"))
	stream, size, err := s.GetObject("files", "remove.txt")
	if stream != nil || size != 0 || !errors.Is(err, ObjectNotExistErr) {
		t.Fatalf("重开后已删除对象 GetObject = (%v, %d, %v)，期望 (nil, 0, ObjectNotExistErr)", stream, size, err)
	}
	keys, err = s.ListObjects("files")
	if err != nil || len(keys) != 1 || keys[0] != "keep.txt" {
		t.Fatalf("重开后 ListObjects = (%v, %v)，期望仅包含 keep.txt", keys, err)
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.After(5 * time.Second)
	for {
		exists, err := s.files.bucket.Exists(context.Background(), oldFiles[0].Name)
		if err != nil {
			t.Fatal(err)
		}
		if !exists {
			break
		}
		select {
		case <-ticker.C:
		case <-timeout:
			t.Fatal("重开后旧文件未被清理")
		}
	}
	requireObjectContent(t, s, "files", "keep.txt", []byte("new"))
}

func requireObjectContent(t *testing.T, s *Service, bucket, key string, want []byte) {
	t.Helper()
	stream, size, err := s.GetObject(bucket, key)
	if err != nil || stream == nil {
		t.Fatalf("GetObject(%q, %q) = (%v, %d, %v)", bucket, key, stream, size, err)
	}
	requireStreamContent(t, stream, size, want)
}

func requireStreamContent(t *testing.T, stream io.ReadCloser, size int64, want []byte) {
	t.Helper()
	if stream == nil {
		t.Fatal("读取流不能为 nil")
	}
	got, readErr := io.ReadAll(stream)
	closeErr := stream.Close()
	if readErr != nil || closeErr != nil || size != int64(len(want)) || !bytes.Equal(got, want) {
		t.Fatalf("读取结果 = (%q, %d, %v, %v)，期望 (%q, %d)", got, size, readErr, closeErr, want, len(want))
	}
}

func useTempWorkingDir(t *testing.T) {
	t.Helper()
	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("获取当前目录失败：%v", err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatalf("进入测试目录失败：%v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldDir); err != nil {
			t.Errorf("恢复当前目录失败：%v", err)
		}
	})
}

func setupTestStore(t *testing.T) *Service {
	t.Helper()
	useTempWorkingDir(t)
	s, err := Open()
	if err != nil {
		t.Fatalf("Open 返回错误：%v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close 返回错误：%v", err)
		}
	})
	return s
}
