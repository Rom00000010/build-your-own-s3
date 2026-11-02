package objectstorage

import (
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
)

var (
	InvalidBucketNameErr       = errors.New("invalid bucket name")
	BucketNameExistErr         = errors.New("bucket already exists")
	BucketNotExistErr          = errors.New("bucket does not exist")
	BucketNotEmptyErr          = errors.New("bucket is not empty")
	InvalidObjectKeyErr        = errors.New("invalid object key")
	ObjectNotExistErr          = errors.New("object does not exist")
	InvalidMultipartRequestErr = errors.New("invalid multipart request")
	InvalidPartErr             = errors.New("invalid part")
	UploadNotExistErr          = errors.New("upload does not exist")
)

type PartInfo struct {
	PartNumber int
	Size       int64
}

// Service 表示一份已打开的对象存储。关闭后不能继续使用该实例。
type Service struct {
	db          *objectDB
	files       *objectStorage
	objectLocks sync.Map // map[[2]string]*sync.Mutex
}

// Open 打开当前工作目录下 data 中的存储；首次打开时创建空存储。
func Open() (*Service, error) {
	root, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	dataDir := filepath.Join(root, "data")
	files, err := openObjectStorage(filepath.Join(dataDir, "objects"))
	if err != nil {
		return nil, err
	}
	db, err := openObjectDB(filepath.Join(dataDir, "metadata.db"))
	if err != nil {
		_ = files.close()
		return nil, err
	}
	return &Service{db: db, files: files}, nil
}

// Close 关闭存储。成功关闭后可再次调用 Open 读取已有数据。
func (s *Service) Close() error {
	if s.db == nil {
		return errors.New("store is not open")
	}
	err := errors.Join(s.db.close(), s.files.close())
	s.db = nil
	return err
}

// CreateBucket 创建空桶，成功时返回桶名；名称不合法或已存在时返回对应错误。
func (s *Service) CreateBucket(name string) (string, error) {
	if !validBucketName(name) {
		return "", InvalidBucketNameErr
	}
	created, err := s.db.createBucket(name)
	if err != nil {
		return "", err
	}
	if !created {
		return "", BucketNameExistErr
	}
	return name, nil
}

// ListBuckets 返回全部桶名，顺序不作保证。
func (s *Service) ListBuckets() ([]string, error) {
	return s.db.listBuckets()
}

// DeleteBucket 删除空桶；桶不存在或非空时返回对应错误。
func (s *Service) DeleteBucket(name string) error {
	deleted, err := s.db.deleteEmptyBucket(name)
	if err != nil {
		return err
	}
	if deleted {
		return nil
	}
	exists, err := s.db.bucketExists(name)
	if err != nil {
		return err
	}
	if !exists {
		return BucketNotExistErr
	}
	return BucketNotEmptyErr
}

// PutObject 上传对象；同一 key 已存在时，完整替换原内容。
func (s *Service) PutObject(bucket string, key string, object io.Reader) error {
	mu := s.objectMutex(bucket, key)
	mu.Lock()
	defer mu.Unlock()

	exists, err := s.db.bucketExists(bucket)
	if err != nil {
		return err
	}
	if !exists {
		return BucketNotExistErr
	}
	if !validObjectKey(key) {
		return InvalidObjectKeyErr
	}
	oldFiles, _, _, err := s.db.getObject(bucket, key)
	if err != nil {
		return err
	}
	// 先写新文件，再切换元数据；文件写入失败时，原对象仍可读取。
	newName, size, err := s.files.write(object)
	if err != nil {
		return err
	}
	if err := s.db.setObject(bucket, key, []objectFile{{Name: newName, Size: size}}, size); err != nil {
		_ = s.files.remove(newName)
		return err
	}
	// 元数据已指向新文件；旧文件清理失败不影响这次上传的结果。
	_ = s.files.removeFiles(oldFiles)
	return nil
}

// GetObject 返回对象读取流及大小；调用方负责关闭读取流。
func (s *Service) GetObject(bucket string, key string) (io.ReadCloser, int64, error) {
	mu := s.objectMutex(bucket, key)
	mu.Lock()
	defer mu.Unlock()

	exists, err := s.db.bucketExists(bucket)
	if err != nil {
		return nil, 0, err
	}
	if !exists {
		return nil, 0, BucketNotExistErr
	}
	if !validObjectKey(key) {
		return nil, 0, InvalidObjectKeyErr
	}
	files, size, exists, err := s.db.getObject(bucket, key)
	if err != nil {
		return nil, 0, err
	}
	if !exists {
		return nil, 0, ObjectNotExistErr
	}
	stream, err := s.files.read(files)
	if err != nil {
		return nil, 0, err
	}
	return stream, size, nil
}

// DeleteObject 删除对象；key 不存在时也返回 nil。
func (s *Service) DeleteObject(bucket string, key string) error {
	mu := s.objectMutex(bucket, key)
	mu.Lock()
	defer mu.Unlock()

	exists, err := s.db.bucketExists(bucket)
	if err != nil {
		return err
	}
	if !exists {
		return BucketNotExistErr
	}
	if !validObjectKey(key) {
		return InvalidObjectKeyErr
	}
	files, _, exists, err := s.db.getObject(bucket, key)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if err := s.db.deleteObject(bucket, key); err != nil {
		return err
	}
	// 元数据已删除；文件清理失败不影响对象对调用方的可见状态。
	_ = s.files.removeFiles(files)
	return nil
}

// ListObjects 返回桶内全部 key，顺序不作保证；空桶返回非 nil 的空切片。
func (s *Service) ListObjects(bucket string) ([]string, error) {
	exists, err := s.db.bucketExists(bucket)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, BucketNotExistErr
	}
	return s.db.listObjects(bucket)
}

// CreateMultipartUpload 创建空任务，返回唯一的任务 ID；totalParts 须为正整数。
func (s *Service) CreateMultipartUpload(bucket, key string, totalParts int) (string, error) {
	exists, err := s.db.bucketExists(bucket)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", BucketNotExistErr
	}
	if !validObjectKey(key) {
		return "", InvalidObjectKeyErr
	}
	if totalParts <= 0 {
		return "", InvalidMultipartRequestErr
	}
	uploadID := rand.Text()
	if err := s.db.createMultipartUpload(bucket, key, uploadID, totalParts); err != nil {
		return "", err
	}
	return uploadID, nil
}

// UploadPart 保存非空分片，替换同编号的旧片；读取或保存失败时保留原分片。
func (s *Service) UploadPart(bucket, key, uploadID string, partNumber int, content io.Reader) (PartInfo, error) {
	totalParts, err := s.getMultipartUpload(bucket, key, uploadID)
	if err != nil {
		return PartInfo{}, err
	}
	if partNumber < 1 || partNumber > totalParts {
		return PartInfo{}, InvalidPartErr
	}
	oldFile, exists, err := s.db.getPart(uploadID, partNumber)
	if err != nil {
		return PartInfo{}, err
	}
	name, size, err := s.files.write(content)
	if err != nil {
		return PartInfo{}, err
	}
	if size == 0 {
		_ = s.files.remove(name)
		return PartInfo{}, InvalidPartErr
	}
	if err := s.db.setPart(uploadID, partNumber, objectFile{Name: name, Size: size}); err != nil {
		_ = s.files.remove(name)
		return PartInfo{}, err
	}
	if exists {
		_ = s.files.remove(oldFile.Name)
	}
	return PartInfo{PartNumber: partNumber, Size: size}, nil
}

// ListParts 按编号升序返回任务中已保存分片的编号和实际大小。
func (s *Service) ListParts(bucket, key, uploadID string) ([]PartInfo, error) {
	if _, err := s.getMultipartUpload(bucket, key, uploadID); err != nil {
		return nil, err
	}
	parts, err := s.db.listParts(uploadID)
	if err != nil {
		return nil, err
	}
	infos := make([]PartInfo, len(parts))
	for i, part := range parts {
		infos[i] = PartInfo{PartNumber: part.PartNumber, Size: part.File.Size}
	}
	return infos, nil
}

func (s *Service) getMultipartUpload(bucket, key, uploadID string) (int, error) {
	exists, err := s.db.bucketExists(bucket)
	if err != nil {
		return 0, err
	}
	if !exists {
		return 0, BucketNotExistErr
	}
	if !validObjectKey(key) {
		return 0, InvalidObjectKeyErr
	}
	totalParts, exists, err := s.db.getMultipartUpload(bucket, key, uploadID)
	if err != nil {
		return 0, err
	}
	if !exists {
		return 0, UploadNotExistErr
	}
	return totalParts, nil
}

// objectMutex 返回同一对象共用的互斥锁，锁项随 Service 实例保留。
func (s *Service) objectMutex(bucket, key string) *sync.Mutex {
	mu, _ := s.objectLocks.LoadOrStore([2]string{bucket, key}, &sync.Mutex{})
	return mu.(*sync.Mutex)
}

func validBucketName(name string) bool {
	if len(name) < 1 || len(name) > 63 || !lowerOrDigit(name[0]) || !lowerOrDigit(name[len(name)-1]) {
		return false
	}
	for i := 1; i < len(name)-1; i++ {
		if !lowerOrDigit(name[i]) && name[i] != '-' {
			return false
		}
	}
	return true
}

func validObjectKey(key string) bool {
	if len(key) < 1 || len(key) > 255 || !letterOrDigit(key[0]) {
		return false
	}
	for i := 1; i < len(key); i++ {
		if !letterOrDigit(key[i]) && key[i] != '.' && key[i] != '_' && key[i] != '-' {
			return false
		}
	}
	return true
}

func lowerOrDigit(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= '0' && c <= '9'
}

func letterOrDigit(c byte) bool {
	return lowerOrDigit(c) || c >= 'A' && c <= 'Z'
}
