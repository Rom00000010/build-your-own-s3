# 对象存储规格

### CreateBucket

```go
// CreateBucket 创建名为 name 的空桶。
// 桶名长 1–63 个字符，仅含小写英文字母、数字和连字符，
// 且首尾必须是字母或数字。
// 若桶名不合法，返回空字符串和 InvalidBucketNameErr。
// 若桶名已存在，返回空字符串和 BucketNameExistErr。
// 成功时，系统新增该空桶，返回 name 和 nil，其他桶不变。
// 失败时，系统状态不变。
func CreateBucket(name string) (string, error)
```

### ListBuckets

```go
// ListBuckets 列出所有桶名。
// 成功时返回包含全部桶名的非 nil 切片和 nil；没有桶时返回空切片。
// 每个桶名只出现一次，顺序不作保证；存储状态不变。
func ListBuckets() ([]string, error)
```

### DeleteBucket

```go
// DeleteBucket 删除名为 name 的空桶。
// 若桶不存在，返回 BucketNotExistErr；若桶内有对象，返回 BucketNotEmptyErr。
// 成功时删除该桶并返回 nil，其他桶及对象不变。
// 失败时存储状态不变。
func DeleteBucket(name string) error
```

### PutObject

```go
// PutObject 向名为 bucket 的桶中上传对象，以 key 标识。
// key 长 1–255 个字符，仅含英文字母、数字、点、下划线和连字符，
// 且首字符必须是英文字母或数字。
// 若桶不存在，返回 BucketNotExistErr。
// 若 key 不合法，返回 InvalidObjectKeyErr。
// 若 object 长度为 0（包括 nil 切片），返回 EmptyObjectErr。
// 成功时，若 key 不存在则新增对象，否则用 object 的内容完整替换原对象，
// 返回 nil，其他对象和桶不变。
// 失败时，系统状态不变。
func PutObject(bucket string, key string, object []byte) error
```

### GetObject

```go
// GetObject 获取名为 bucket 的桶中 key 对应的对象内容。
// key 的合法性规则与 PutObject 相同。
// 若桶不存在，返回 nil 和 BucketNotExistErr。
// 若 key 不合法，返回 nil 和 InvalidObjectKeyErr。
// 若对象不存在，返回 nil 和 ObjectNotExistErr。
// 成功时，返回对象的完整字节内容和 nil。
// 无论成功或失败，系统状态不变。
func GetObject(bucket string, key string) ([]byte, error)
```

### DeleteObject

```go
// DeleteObject 删除名为 bucket 的桶中 key 对应的对象。
// key 的合法性规则与 PutObject 相同。
// 若桶不存在，返回 BucketNotExistErr。
// 若 key 不合法，返回 InvalidObjectKeyErr。
// 若对象存在，删除该对象并返回 nil，其他对象和桶不变。
// 若对象不存在，返回 nil，系统状态不变。
// 失败时，系统状态不变。
func DeleteObject(bucket string, key string) error
```

### ListObjects

```go
// ListObjects 列出名为 bucket 的桶中所有对象的 key。
// 若桶不存在，返回 nil 和 BucketNotExistErr。
// 成功时，返回包含全部 key 的切片和 nil，每个 key 只出现一次，顺序不作保证。
// 若桶为空，返回非 nil 的空切片和 nil。
// 无论成功或失败，系统状态不变。
func ListObjects(bucket string) ([]string, error)
```
