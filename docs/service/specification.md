# 存储服务规格

存储接口由 `build_your_own_s3/objectstorage` 包提供。

### 启动、关闭与持久化

调用方通过 `Open` 获得 `Service` 实例，调用其方法操作存储，并在使用结束后调用 `Close`。

```go
// Open 打开当前工作目录下 data 目录中的存储；若尚无数据，则创建空存储。
// 成功时返回可操作的 Service 实例和 nil；失败时返回 nil 和非 nil 错误。
func Open() (*Service, error)

// Close 关闭该实例。仅在成功调用 Open 后、尚未关闭时调用。
// 成功时返回 nil，之后不可继续使用该实例；可重新调用 Open 获取新实例。
// 关闭失败时返回非 nil 错误。
func (s *Service) Close() error
```

成功的桶和对象操作在正常关闭后再次打开时仍然可见，包括新建或删除的桶、上传或覆盖的对象，以及已删除的对象。

### CreateBucket

```go
// CreateBucket 创建名为 name 的空桶，需已成功调用 Open。
// 桶名长 1–63 个字符，仅含小写英文字母、数字和连字符，
// 且首尾必须是字母或数字。
// 若桶名不合法，返回空字符串和 InvalidBucketNameErr。
// 若桶名已存在，返回空字符串和 BucketNameExistErr。
// 成功时，系统新增该空桶，返回 name 和 nil，其他桶不变。
// 失败时，系统状态不变。
func (s *Service) CreateBucket(name string) (string, error)
```

### ListBuckets

```go
// ListBuckets 列出所有桶名。
// 成功时返回包含全部桶名的非 nil 切片和 nil；没有桶时返回空切片。
// 每个桶名只出现一次，顺序不作保证；存储状态不变。
// 读取存储失败时返回 nil 和非 nil 错误。
// 需已成功调用 Open。
func (s *Service) ListBuckets() ([]string, error)
```

### DeleteBucket

```go
// DeleteBucket 删除名为 name 的空桶。
// 若桶不存在，返回 BucketNotExistErr；若桶内有对象，返回 BucketNotEmptyErr。
// 成功时删除该桶并返回 nil，其他桶及对象不变。
// 失败时存储状态不变。
// 读取或删除存储失败时返回非 nil 错误；需已成功调用 Open。
func (s *Service) DeleteBucket(name string) error
```

### PutObject

```go
// PutObject 从 object 逐段读取内容，上传到名为 bucket 的桶中，以 key 标识。
// 需已成功调用 Open，object 须为非 nil 的 io.Reader。
// key 长 1–255 个字符，仅含英文字母、数字、点、下划线和连字符，
// 且首字符必须是英文字母或数字。
// 若桶不存在，返回 BucketNotExistErr；若 key 不合法，返回 InvalidObjectKeyErr。
// 成功时，若 key 不存在则新增对象，否则用读取的内容完整替换原对象；
// 返回 nil，其他对象和桶不变。
// 读取或保存失败时返回非 nil 错误；失败时桶和对象的可见状态不变。
func (s *Service) PutObject(bucket string, key string, object io.Reader) error
```

### GetObject

```go
// GetObject 获取名为 bucket 的桶中 key 对应对象的读取流和实际大小。
// 需已成功调用 Open；key 的合法性规则与 PutObject 相同。
// 若桶不存在，返回 nil、0 和 BucketNotExistErr。
// 若 key 不合法，返回 nil、0 和 InvalidObjectKeyErr。
// 若对象不存在，返回 nil、0 和 ObjectNotExistErr。
// 成功时返回非 nil 的 io.ReadCloser、对象大小和 nil；调用方负责关闭读取流。
// 无论成功或失败，存储状态不变。
func (s *Service) GetObject(bucket string, key string) (io.ReadCloser, int64, error)
```

### DeleteObject

```go
// DeleteObject 删除名为 bucket 的桶中 key 对应的对象，需已成功调用 Open。
// key 的合法性规则与 PutObject 相同。
// 若桶不存在，返回 BucketNotExistErr。
// 若 key 不合法，返回 InvalidObjectKeyErr。
// 若对象存在，删除该对象并返回 nil，其他对象和桶不变。
// 若对象不存在，返回 nil，系统状态不变。
// 失败时，系统状态不变。
func (s *Service) DeleteObject(bucket string, key string) error
```

### ListObjects

```go
// ListObjects 列出名为 bucket 的桶中所有对象的 key，需已成功调用 Open。
// 若桶不存在，返回 nil 和 BucketNotExistErr。
// 成功时，返回包含全部 key 的切片和 nil，每个 key 只出现一次，顺序不作保证。
// 若桶为空，返回非 nil 的空切片和 nil。
// 无论成功或失败，系统状态不变。
func (s *Service) ListObjects(bucket string) ([]string, error)
```
