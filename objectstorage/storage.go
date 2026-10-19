package objectstorage

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"os"

	"github.com/peterbourgon/diskv/v3"
)

type objectStorage struct {
	disk *diskv.Diskv
}

func openObjectStorage(dir string) (*objectStorage, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	return &objectStorage{disk: diskv.New(diskv.Options{
		BasePath: dir,
	})}, nil
}

type countingReader struct {
	io.Reader
	size int64
}

func (reader *countingReader) Read(p []byte) (int, error) {
	n, err := reader.Reader.Read(p)
	reader.size += int64(n)
	return n, err
}

func (storage *objectStorage) write(content io.Reader) (string, int64, error) {
	// diskv 会覆盖同名 key；生成新的内部名称，避免覆盖现有内容文件。
	for {
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			return "", 0, err
		}
		name := hex.EncodeToString(id[:])
		if storage.disk.Has(name) {
			continue
		}
		reader := &countingReader{Reader: content}
		if err := storage.disk.WriteStream(name, reader, false); err != nil {
			return "", 0, err
		}
		return name, reader.size, nil
	}
}

func (storage *objectStorage) read(name string) (io.ReadCloser, error) {
	return storage.disk.ReadStream(name, true)
}

func (storage *objectStorage) remove(name string) error {
	err := storage.disk.Erase(name)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
