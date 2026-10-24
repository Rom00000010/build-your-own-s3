package objectstorage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"os"

	"gocloud.dev/blob"
	"gocloud.dev/blob/fileblob"
	"gocloud.dev/gcerrors"
)

type objectStorage struct {
	bucket *blob.Bucket
}

func openObjectStorage(dir string) (*objectStorage, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	bucket, err := fileblob.OpenBucket(dir, &fileblob.Options{
		NoTempDir: true,
		Metadata:  fileblob.MetadataDontWrite,
	})
	if err != nil {
		return nil, err
	}
	return &objectStorage{bucket: bucket}, nil
}

func (storage *objectStorage) write(content io.Reader) (string, int64, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", 0, err
	}
	name := hex.EncodeToString(id[:])
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	writer, err := storage.bucket.NewWriter(ctx, name, nil)
	if err != nil {
		return "", 0, err
	}
	size, copyErr := io.Copy(writer, content)
	if copyErr != nil {
		cancel()
		_ = writer.Close()
		return "", 0, copyErr
	}
	if err := writer.Close(); err != nil {
		_ = storage.remove(name)
		return "", 0, err
	}
	return name, size, nil
}

func (storage *objectStorage) read(name string) (io.ReadCloser, error) {
	return storage.bucket.NewReader(context.Background(), name, nil)
}

func (storage *objectStorage) remove(name string) error {
	err := storage.bucket.Delete(context.Background(), name)
	if gcerrors.Code(err) == gcerrors.NotFound {
		return nil
	}
	return err
}

func (storage *objectStorage) close() error {
	return storage.bucket.Close()
}
