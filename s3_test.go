package s3

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"
)

func resetTestStore() {
	buckets = make(map[string]map[string][]byte)
}

// CreateBucket 测试策略：
//   - 长度：0、1、2–62、63、超过 63；字符：合法、非法。
//   - 首尾：合法、开头为连字符、结尾为连字符。
//   - 桶名：不存在、已存在。
//
// 检查成功时返回桶名且新桶为空，失败时返回指定错误。
func TestCreateBucket(t *testing.T) {
	resetTestStore()

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
				name, err := CreateBucket(tc.name)
				if err != nil || name != tc.name {
					t.Fatalf("CreateBucket(%q) = (%q, %v)，期望 (%q, nil)", tc.name, name, err, tc.name)
				}
				keys, err := ListObjects(tc.name)
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
				name, err := CreateBucket(tc.name)
				if name != "" || !errors.Is(err, InvalidBucketNameErr) {
					t.Fatalf("CreateBucket(%q) = (%q, %v)，期望 (空字符串, InvalidBucketNameErr)", tc.name, name, err)
				}
			})
		}
	})

	t.Run("重复创建", func(t *testing.T) {
		const bucket = "existing-bucket"
		if _, err := CreateBucket(bucket); err != nil {
			t.Fatalf("准备已有桶失败：%v", err)
		}
		name, err := CreateBucket(bucket)
		if name != "" || !errors.Is(err, BucketNameExistErr) {
			t.Fatalf("重复创建 %q = (%q, %v)，期望 (空字符串, BucketNameExistErr)", bucket, name, err)
		}
	})
}

// ListBuckets 测试策略：空存储与多个桶；检查结果非 nil、无重复，且不改变状态。
func TestListBuckets(t *testing.T) {
	resetTestStore()
	names, err := ListBuckets()
	if err != nil || names == nil || len(names) != 0 {
		t.Fatalf("空存储 ListBuckets = (%v, %v)", names, err)
	}
	for _, name := range []string{"photos", "documents"} {
		if _, err := CreateBucket(name); err != nil {
			t.Fatal(err)
		}
	}
	names, err = ListBuckets()
	if err != nil || len(names) != 2 || !slices.Contains(names, "photos") || !slices.Contains(names, "documents") {
		t.Fatalf("ListBuckets = (%v, %v)", names, err)
	}
}

// DeleteBucket 测试策略：桶不存在、空桶和非空桶。
// 检查成功删除后桶不可访问；非空时保留桶和对象。
func TestDeleteBucket(t *testing.T) {
	resetTestStore()
	if err := DeleteBucket("missing"); !errors.Is(err, BucketNotExistErr) {
		t.Fatalf("删除不存在的桶 = %v", err)
	}
	if _, err := CreateBucket("empty"); err != nil {
		t.Fatal(err)
	}
	if err := DeleteBucket("empty"); err != nil {
		t.Fatalf("删除空桶 = %v", err)
	}
	if _, err := ListObjects("empty"); !errors.Is(err, BucketNotExistErr) {
		t.Fatalf("删除后 ListObjects = %v", err)
	}
	if _, err := CreateBucket("full"); err != nil {
		t.Fatal(err)
	}
	if err := PutObject("full", "a.txt", []byte("a")); err != nil {
		t.Fatal(err)
	}
	if err := DeleteBucket("full"); !errors.Is(err, BucketNotEmptyErr) {
		t.Fatalf("删除非空桶 = %v", err)
	}
	content, err := GetObject("full", "a.txt")
	if err != nil || string(content) != "a" {
		t.Fatalf("删除失败后对象 = (%q, %v)", content, err)
	}
	if err := DeleteObject("full", "a.txt"); err != nil {
		t.Fatal(err)
	}
	if err := DeleteBucket("full"); err != nil {
		t.Fatalf("清空后删除桶 = %v", err)
	}
	names, err := ListBuckets()
	if err != nil || len(names) != 0 {
		t.Fatalf("删除后桶列表 = (%v, %v)", names, err)
	}
}

// PutObject 测试策略：
//   - 桶：存在、不存在；key：合法、空、首字符非法、过长。
//   - 内容：非空、空切片、nil；对象：新 key、已有 key。
//
// 成功时用 GetObject 检查内容，失败时检查对应错误。
func TestPutObject(t *testing.T) {
	resetTestStore()

	const bucket = "put-object-test"
	if _, err := CreateBucket(bucket); err != nil {
		t.Fatalf("准备桶失败：%v", err)
	}

	t.Run("新 key 上传", func(t *testing.T) {
		for _, key := range []string{"a", "note.txt", strings.Repeat("b", 255)} {
			content := []byte("hello")
			if err := PutObject(bucket, key, content); err != nil {
				t.Fatalf("PutObject(%q) 返回错误：%v", key, err)
			}
			got, err := GetObject(bucket, key)
			if err != nil || !bytes.Equal(got, content) {
				t.Fatalf("GetObject(%q) = (%q, %v)，期望 (%q, nil)", key, got, err, content)
			}
		}
	})

	t.Run("同 key 覆盖", func(t *testing.T) {
		const key = "overwrite.txt"
		if err := PutObject(bucket, key, []byte("old-long-content")); err != nil {
			t.Fatalf("准备原对象失败：%v", err)
		}
		if err := PutObject(bucket, key, []byte("new")); err != nil {
			t.Fatalf("覆盖对象失败：%v", err)
		}
		got, err := GetObject(bucket, key)
		if err != nil || !bytes.Equal(got, []byte("new")) {
			t.Fatalf("覆盖后 GetObject = (%q, %v)，期望 (new, nil)", got, err)
		}
	})

	t.Run("桶不存在", func(t *testing.T) {
		if err := PutObject("missing-put-bucket", "note.txt", []byte("hello")); !errors.Is(err, BucketNotExistErr) {
			t.Fatalf("桶不存在时返回 %v，期望 BucketNotExistErr", err)
		}
	})

	t.Run("非法 key", func(t *testing.T) {
		for _, key := range []string{"", ".hidden", "a/b", strings.Repeat("c", 256)} {
			if err := PutObject(bucket, key, []byte("hello")); !errors.Is(err, InvalidObjectKeyErr) {
				t.Fatalf("PutObject(%q) 返回 %v，期望 InvalidObjectKeyErr", key, err)
			}
		}
	})

	t.Run("空内容", func(t *testing.T) {
		for _, content := range [][]byte{nil, {}} {
			if err := PutObject(bucket, "empty-content", content); !errors.Is(err, EmptyObjectErr) {
				t.Fatalf("PutObject(%q, %v) 返回 %v，期望 EmptyObjectErr", "empty-content", content, err)
			}
		}
	})
}

// GetObject 测试策略：桶存在或不存在；key 合法、非法或不存在。
// 成功时检查内容，失败时检查 nil 和对应错误。
func TestGetObject(t *testing.T) {
	resetTestStore()

	const bucket = "get-object-test"
	const key = "note.txt"
	content := []byte("hello")
	if _, err := CreateBucket(bucket); err != nil {
		t.Fatalf("准备桶失败：%v", err)
	}
	if err := PutObject(bucket, key, content); err != nil {
		t.Fatalf("准备对象失败：%v", err)
	}

	t.Run("对象存在", func(t *testing.T) {
		got, err := GetObject(bucket, key)
		if err != nil || !bytes.Equal(got, []byte("hello")) {
			t.Fatalf("GetObject = (%q, %v)，期望 (hello, nil)", got, err)
		}
	})

	t.Run("桶不存在", func(t *testing.T) {
		got, err := GetObject("missing-get-bucket", key)
		if got != nil || !errors.Is(err, BucketNotExistErr) {
			t.Fatalf("GetObject = (%v, %v)，期望 (nil, BucketNotExistErr)", got, err)
		}
	})

	t.Run("非法 key", func(t *testing.T) {
		got, err := GetObject(bucket, ".hidden")
		if got != nil || !errors.Is(err, InvalidObjectKeyErr) {
			t.Fatalf("GetObject = (%v, %v)，期望 (nil, InvalidObjectKeyErr)", got, err)
		}
	})

	t.Run("对象不存在", func(t *testing.T) {
		got, err := GetObject(bucket, "missing.txt")
		if got != nil || !errors.Is(err, ObjectNotExistErr) {
			t.Fatalf("GetObject = (%v, %v)，期望 (nil, ObjectNotExistErr)", got, err)
		}
	})
}

// DeleteObject 测试策略：桶存在或不存在；key 合法或非法；对象存在或不存在。
// 检查删除后的对象不可获取，以及各情况下的返回值。
func TestDeleteObject(t *testing.T) {
	resetTestStore()

	const bucket = "delete-object-test"
	const key = "note.txt"
	if _, err := CreateBucket(bucket); err != nil {
		t.Fatalf("准备桶失败：%v", err)
	}
	if err := PutObject(bucket, key, []byte("hello")); err != nil {
		t.Fatalf("准备对象失败：%v", err)
	}

	t.Run("删除已有对象", func(t *testing.T) {
		if err := DeleteObject(bucket, key); err != nil {
			t.Fatalf("DeleteObject 返回错误：%v", err)
		}
		got, err := GetObject(bucket, key)
		if got != nil || !errors.Is(err, ObjectNotExistErr) {
			t.Fatalf("删除后 GetObject = (%v, %v)，期望 (nil, ObjectNotExistErr)", got, err)
		}
	})

	t.Run("对象不存在", func(t *testing.T) {
		if err := DeleteObject(bucket, "missing.txt"); err != nil {
			t.Fatalf("删除不存在的对象返回 %v，期望 nil", err)
		}
	})

	t.Run("桶不存在", func(t *testing.T) {
		if err := DeleteObject("missing-delete-bucket", key); !errors.Is(err, BucketNotExistErr) {
			t.Fatalf("桶不存在时返回 %v，期望 BucketNotExistErr", err)
		}
	})

	t.Run("非法 key", func(t *testing.T) {
		if err := DeleteObject(bucket, ".hidden"); !errors.Is(err, InvalidObjectKeyErr) {
			t.Fatalf("非法 key 时返回 %v，期望 InvalidObjectKeyErr", err)
		}
	})
}

// ListObjects 测试策略：桶不存在、空桶、含多个对象的桶。
// 检查 key 完整且不重复；规格未规定顺序，因此按集合比较。
func TestListObjects(t *testing.T) {
	resetTestStore()

	const bucket = "list-objects-test"
	if _, err := CreateBucket(bucket); err != nil {
		t.Fatalf("准备桶失败：%v", err)
	}

	t.Run("空桶", func(t *testing.T) {
		keys, err := ListObjects(bucket)
		if err != nil || keys == nil || len(keys) != 0 {
			t.Fatalf("空桶 ListObjects = (%v, %v)，期望 (非 nil 空切片, nil)", keys, err)
		}
	})

	t.Run("多个对象", func(t *testing.T) {
		for _, key := range []string{"a.txt", "b.txt"} {
			if err := PutObject(bucket, key, []byte("data")); err != nil {
				t.Fatalf("准备对象 %q 失败：%v", key, err)
			}
		}
		keys, err := ListObjects(bucket)
		if err != nil || len(keys) != 2 || !slices.Contains(keys, "a.txt") || !slices.Contains(keys, "b.txt") {
			t.Fatalf("ListObjects = (%v, %v)，期望恰好包含 a.txt 和 b.txt", keys, err)
		}
	})

	t.Run("桶不存在", func(t *testing.T) {
		keys, err := ListObjects("missing-list-bucket")
		if keys != nil || !errors.Is(err, BucketNotExistErr) {
			t.Fatalf("ListObjects = (%v, %v)，期望 (nil, BucketNotExistErr)", keys, err)
		}
	})
}
