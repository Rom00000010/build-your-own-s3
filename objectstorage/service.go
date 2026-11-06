package objectstorage

import (
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

var (
	InvalidBucketNameErr       = errors.New("invalid bucket name")
	BucketNameExistErr         = errors.New("bucket already exists")
	BucketNotExistErr          = errors.New("bucket does not exist")
	BucketNotEmptyErr          = errors.New("bucket is not empty")
	InvalidObjectKeyErr        = errors.New("invalid object key")
	ObjectNotExistErr          = errors.New("object does not exist")
	InvalidRangeErr            = errors.New("invalid range")
	InvalidMultipartRequestErr = errors.New("invalid multipart request")
	InvalidPartErr             = errors.New("invalid part")
	IncompleteUploadErr        = errors.New("incomplete upload")
	UploadNotExistErr          = errors.New("upload does not exist")
)

type PartInfo struct {
	PartNumber int
	Size       int64
}

// ByteRange 表示 range 下载的字节范围，位置从 0 开始且包含两端。
// 省略 End 表示读至末尾；省略 Start 时，End 表示读取末尾的字节数。
type ByteRange struct {
	Start *int64
	End   *int64
}

// ObjectRead 包含同一版本的读取流、对象大小、ETag 和实际返回范围。
type ObjectRead struct {
	Body  io.ReadCloser
	Size  int64 // 完整对象大小。
	ETag  string
	Range *ByteRange // nil 表示完整下载。
}

// Service 表示一份已打开的对象存储。关闭后不能继续使用该实例。
type Service struct {
	db          *objectDB
	files       *objectStorage
	gc          *garbageCollector
	objectLocks sync.Map // map[[2]string]*sync.Mutex
	partLocks   sync.Map // map[partKey]*sync.Mutex
}

type partKey struct {
	uploadID   string
	partNumber int
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
	return &Service{db: db, files: files, gc: newGC(db, files.remove)}, nil
}

// Close 关闭存储。成功关闭后可再次调用 Open 读取已有数据。
func (s *Service) Close() error {
	if s.db == nil {
		return errors.New("store is not open")
	}
	s.gc.close()
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
	files, err := s.db.listBucketUploadFiles(name)
	if err != nil {
		return err
	}
	deleted, err := s.db.deleteEmptyBucket(name)
	if err != nil {
		return err
	}
	if deleted {
		s.registerGarbageFiles(files...)
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

// PutObject 上传对象并生成新 ETag；同一 key 已存在时，完整替换原内容。
func (s *Service) PutObject(bucket string, key string, content io.Reader) error {
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
	old, _, err := s.db.getObject(bucket, key)
	if err != nil {
		return err
	}
	// 先写新文件，再切换元数据；文件写入失败时，原对象仍可读取。
	newName, size, err := s.files.write(content)
	if err != nil {
		return err
	}
	newFile := objectFile{Name: newName, Size: size}
	metadata := object{Files: []objectFile{newFile}, Size: size, ETag: strconv.Quote(rand.Text())}
	if err := s.db.setObject(bucket, key, metadata); err != nil {
		s.registerGarbageFiles(newFile)
		return err
	}
	s.registerGarbageFiles(old.Files...)
	return nil
}

// GetObject 返回完整或 range 下载结果；ETag 不匹配时返回完整对象。
// 调用方负责关闭 Body；后续覆盖或删除不影响该流的读取。
func (s *Service) GetObject(bucket, key string, byteRange *ByteRange, expectedETag string) (ObjectRead, error) {
	mu := s.objectMutex(bucket, key)
	mu.Lock()
	defer mu.Unlock()

	exists, err := s.db.bucketExists(bucket)
	if err != nil {
		return ObjectRead{}, err
	}
	if !exists {
		return ObjectRead{}, BucketNotExistErr
	}
	if !validObjectKey(key) {
		return ObjectRead{}, InvalidObjectKeyErr
	}
	object, exists, err := s.db.getObject(bucket, key)
	if err != nil {
		return ObjectRead{}, err
	}
	if !exists {
		return ObjectRead{}, ObjectNotExistErr
	}
	if expectedETag != "" && expectedETag != object.ETag {
		byteRange = nil
	}
	actualRange, err := byteRange.resolve(object.Size)
	if err != nil {
		return ObjectRead{}, err
	}
	offset, length := int64(0), object.Size
	if actualRange != nil {
		offset = *actualRange.Start
		length = *actualRange.End - offset + 1
	}
	stream, err := s.files.read(object.Files, offset, length)
	if err != nil {
		return ObjectRead{}, err
	}
	return ObjectRead{Body: stream, Size: object.Size, ETag: object.ETag, Range: actualRange}, nil
}

// resolve 根据对象大小换算并校验实际读取范围；nil 表示完整下载。
func (r *ByteRange) resolve(size int64) (*ByteRange, error) {
	if r == nil {
		return nil, nil
	}
	if size == 0 || r.Start == nil && r.End == nil {
		return nil, InvalidRangeErr
	}
	start, end := int64(0), size-1
	if r.Start == nil {
		if *r.End <= 0 {
			return nil, InvalidRangeErr
		}
		start = max(0, size-*r.End)
	} else {
		start = *r.Start
		if start < 0 || start >= size {
			return nil, InvalidRangeErr
		}
		if r.End != nil {
			if *r.End < start {
				return nil, InvalidRangeErr
			}
			end = min(end, *r.End)
		}
	}
	return &ByteRange{Start: &start, End: &end}, nil
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
	object, exists, err := s.db.getObject(bucket, key)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if err := s.db.deleteObject(bucket, key); err != nil {
		return err
	}
	s.registerGarbageFiles(object.Files...)
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
	mu := s.partMutex(uploadID, partNumber)
	mu.Lock()
	defer mu.Unlock()

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
	newFile := objectFile{Name: name, Size: size}
	if size == 0 {
		s.registerGarbageFiles(newFile)
		return PartInfo{}, InvalidPartErr
	}
	if err := s.db.setPart(uploadID, partNumber, newFile); err != nil {
		s.registerGarbageFiles(newFile)
		return PartInfo{}, err
	}
	if exists {
		s.registerGarbageFiles(oldFile)
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

// CompleteMultipartUpload 将全部分片按编号发布为对象；缺片时保留任务和原对象。
func (s *Service) CompleteMultipartUpload(bucket, key, uploadID string) error {
	mu := s.objectMutex(bucket, key)
	mu.Lock()
	defer mu.Unlock()

	totalParts, err := s.getMultipartUpload(bucket, key, uploadID)
	if err != nil {
		return err
	}
	files, size, err := s.getUploadFiles(uploadID)
	if err != nil {
		return err
	}
	if len(files) != totalParts {
		return IncompleteUploadErr
	}
	old, _, err := s.db.getObject(bucket, key)
	if err != nil {
		return err
	}
	metadata := object{Files: files, Size: size, ETag: strconv.Quote(rand.Text())}
	if err := s.db.completeMultipartUpload(bucket, key, uploadID, metadata); err != nil {
		return err
	}
	s.registerGarbageFiles(old.Files...)
	return nil
}

// AbortMultipartUpload 删除任务及其分片，保留已发布对象。
func (s *Service) AbortMultipartUpload(bucket, key, uploadID string) error {
	if _, err := s.getMultipartUpload(bucket, key, uploadID); err != nil {
		return err
	}
	files, _, err := s.getUploadFiles(uploadID)
	if err != nil {
		return err
	}
	if err := s.db.deleteMultipartUpload(uploadID); err != nil {
		return err
	}
	s.registerGarbageFiles(files...)
	return nil
}

// registerGarbageFiles 尽力登记废弃文件，登记失败不改变业务操作的结果。
func (s *Service) registerGarbageFiles(files ...objectFile) {
	names := make([]string, 0, len(files))
	for _, file := range files {
		names = append(names, file.Name)
	}
	_ = s.db.addGCFiles(names)
}

func (s *Service) getUploadFiles(uploadID string) ([]objectFile, int64, error) {
	parts, err := s.db.listParts(uploadID)
	if err != nil {
		return nil, 0, err
	}
	files := make([]objectFile, len(parts))
	var size int64
	for i, part := range parts {
		files[i] = part.File
		size += part.File.Size
	}
	return files, size, nil
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

// partMutex 返回同一分片共用的互斥锁，锁项随 Service 实例保留。
func (s *Service) partMutex(uploadID string, partNumber int) *sync.Mutex {
	mu, _ := s.partLocks.LoadOrStore(partKey{uploadID, partNumber}, &sync.Mutex{})
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
