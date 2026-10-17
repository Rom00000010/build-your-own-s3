package s3

import (
	"crypto/rand"
	"encoding/hex"
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
		BasePath:     dir,
		CacheSizeMax: 16 << 20,
	})}, nil
}

func (storage *objectStorage) write(content []byte) (string, error) {
	// diskv 会覆盖同名 key；生成新的内部名称，避免覆盖现有内容文件。
	for {
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			return "", err
		}
		name := hex.EncodeToString(id[:])
		if storage.disk.Has(name) {
			continue
		}
		if err := storage.disk.Write(name, content); err != nil {
			return "", err
		}
		return name, nil
	}
}

func (storage *objectStorage) read(name string) ([]byte, error) {
	return storage.disk.Read(name)
}

func (storage *objectStorage) remove(name string) error {
	err := storage.disk.Erase(name)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
