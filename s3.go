package s3

import "errors"

var (
	InvalidBucketNameErr = errors.New("invalid bucket name")
	BucketNameExistErr   = errors.New("bucket already exists")
	BucketNotExistErr    = errors.New("bucket does not exist")
	BucketNotEmptyErr    = errors.New("bucket is not empty")
	InvalidObjectKeyErr  = errors.New("invalid object key")
	ObjectNotExistErr    = errors.New("object does not exist")
)

// 外层 map 按桶名找桶，内层 map 按 key 找对象的字节内容。
// 桶名和 key 均合法；每个桶的内层 map 已初始化。
// 传入和返回的字节切片不与内部数据共享。
var buckets = make(map[string]map[string][]byte)

func CreateBucket(name string) (string, error) {
	if !validBucketName(name) {
		return "", InvalidBucketNameErr
	}
	if _, exists := buckets[name]; exists {
		return "", BucketNameExistErr
	}
	buckets[name] = make(map[string][]byte)
	return name, nil
}

// ListBuckets 返回全部桶名，顺序不作保证。
func ListBuckets() ([]string, error) {
	names := make([]string, 0, len(buckets))
	for name := range buckets {
		names = append(names, name)
	}
	return names, nil
}

// DeleteBucket 删除空桶；桶不存在或非空时返回对应错误。
func DeleteBucket(name string) error {
	objects, exists := buckets[name]
	if !exists {
		return BucketNotExistErr
	}
	if len(objects) != 0 {
		return BucketNotEmptyErr
	}
	delete(buckets, name)
	return nil
}

func PutObject(bucket string, key string, object []byte) error {
	objects, exists := buckets[bucket]
	if !exists {
		return BucketNotExistErr
	}
	if !validObjectKey(key) {
		return InvalidObjectKeyErr
	}
	objects[key] = append([]byte(nil), object...)
	return nil
}

func GetObject(bucket string, key string) ([]byte, error) {
	objects, exists := buckets[bucket]
	if !exists {
		return nil, BucketNotExistErr
	}
	if !validObjectKey(key) {
		return nil, InvalidObjectKeyErr
	}
	object, exists := objects[key]
	if !exists {
		return nil, ObjectNotExistErr
	}
	return append([]byte(nil), object...), nil
}

func DeleteObject(bucket string, key string) error {
	objects, exists := buckets[bucket]
	if !exists {
		return BucketNotExistErr
	}
	if !validObjectKey(key) {
		return InvalidObjectKeyErr
	}
	delete(objects, key)
	return nil
}

func ListObjects(bucket string) ([]string, error) {
	objects, exists := buckets[bucket]
	if !exists {
		return nil, BucketNotExistErr
	}
	keys := make([]string, 0, len(objects))
	for key := range objects {
		keys = append(keys, key)
	}
	return keys, nil
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
