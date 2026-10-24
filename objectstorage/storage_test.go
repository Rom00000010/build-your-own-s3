package objectstorage

import (
	"bytes"
	"io"
	"testing"
)

// 删除测试策略：先打开读取流，再删除文件；已打开的流仍应读到完整内容。
func TestObjectStorageReadAfterRemove(t *testing.T) {
	storage, err := openObjectStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	want := bytes.Repeat([]byte("object"), 1<<17)
	name, size, err := storage.write(bytes.NewReader(want))
	if err != nil || size != int64(len(want)) {
		t.Fatalf("write = (%q, %d, %v)", name, size, err)
	}
	stream, err := storage.read(name)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if err := storage.remove(name); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(stream)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("已打开的流在删除后读取了 %d 字节，期望 %d 字节，错误：%v", len(got), len(want), err)
	}
}
