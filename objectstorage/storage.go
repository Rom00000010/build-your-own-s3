package objectstorage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"

	"gocloud.dev/blob"
	"gocloud.dev/gcerrors"

	_ "github.com/TuSKan/gocloud-ext/blob/httpblob"
)

type objectStorage struct {
	bucket *blob.Bucket
}

func openObjectStorage(blobURL string) (*objectStorage, error) {
	bucket, err := blob.OpenBucket(context.Background(), blobURL)
	if err != nil {
		return nil, err
	}
	return &objectStorage{bucket: bucket}, nil
}

func (storage *objectStorage) write(content io.Reader) (string, int64, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// 碰撞时生成新的内部名称，避免覆盖现有内容文件。
	for {
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			return "", 0, err
		}
		name := hex.EncodeToString(id[:])
		exists, err := storage.bucket.Exists(ctx, name)
		if err != nil {
			return "", 0, err
		}
		if exists {
			continue
		}
		writer, err := storage.bucket.NewWriter(ctx, name, nil)
		if err != nil {
			return "", 0, err
		}
		size, copyErr := io.Copy(writer, content)
		if copyErr != nil {
			// 取消后再关闭 writer，避免发布未完整读取的内容。
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
}

func (storage *objectStorage) read(files []objectFile, offset, length int64) (io.ReadCloser, error) {
	stream := &objectReader{}
	readers := make([]io.Reader, 0, len(files))
	// 返回前打开全部所需文件，保证后续覆盖或删除不影响读取。
	for _, file := range files {
		if length == 0 {
			break
		}
		if offset >= file.Size {
			offset -= file.Size
			continue
		}
		readLength := min(length, file.Size-offset)
		reader, err := storage.bucket.NewRangeReader(context.Background(), file.Name, offset, readLength, nil)
		if err != nil {
			_ = stream.Close()
			return nil, err
		}
		stream.readers = append(stream.readers, reader)
		readers = append(readers, reader)
		length -= readLength
		offset = 0
	}
	stream.Reader = io.MultiReader(readers...)
	return stream, nil
}

type objectReader struct {
	io.Reader
	readers []io.ReadCloser
}

func (reader *objectReader) Close() error {
	var err error
	for _, stream := range reader.readers {
		err = errors.Join(err, stream.Close())
	}
	return err
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
