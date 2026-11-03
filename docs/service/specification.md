# 存储服务规格

存储接口由 `build_your_own_s3/objectstorage` 包提供。

并发约定见[总规格的“并发”一节](../specification.md#并发)。

### 通用约定

- **调用前提**：所有 `Service` 方法均要求实例由 `Open` 成功打开，且尚未调用 `Close`。
- **桶名**：`CreateBucket` 的 `name` 长 1–63 个字符，仅含小写英文字母、数字和连字符，首尾必须是字母或数字；不合法时返回 `InvalidBucketNameErr`。以已有桶为目标的操作在桶不存在时返回 `BucketNotExistErr`。
- **key**：所有接收 key 的方法均要求其长 1–255 个字符，仅含英文字母、数字、点、下划线和连字符，首字符必须是英文字母或数字；不合法时返回 `InvalidObjectKeyErr`。
- **错误返回**：成功时 `err` 为 nil；失败时 `err` 非 nil，其他返回值为各自类型的零值。
- **列表结果**：成功时返回非 nil 切片；没有元素时返回空切片。
- **校验失败**：参数或业务条件不满足时，返回对应错误，存储状态不变。

### 启动、关闭与持久化

调用方通过 `Open` 获得 `Service` 实例，调用其方法操作存储，并在使用结束后调用 `Close`。

```go
// Open 打开当前工作目录下 data 目录中的存储；若尚无数据，则创建空存储。
// 成功时返回可操作的 Service 实例。
func Open() (*Service, error)

// Close 关闭该实例，成功后不可继续使用；可重新调用 Open 获取新实例。
func (s *Service) Close() error
```

成功的桶和对象操作在正常关闭后再次打开时仍然可见，包括新建或删除的桶、上传或覆盖的对象，以及已删除的对象。

未完成任务及已成功保存的分片在正常关闭后再次打开时仍可使用；已完成或取消的任务保持失效。

`Close` 停止后台清理，不要求清空全部待清理文件；再次 `Open` 后继续处理已持久化的待清理文件。

### 文件清理

对象覆盖或删除、分片替换、取消任务以及删除桶产生的废弃文件，服务尽力登记为待清理文件，登记成功后由后台异步清理，请求不等待这些文件的物理删除。

- **请求结果**：清理登记或文件删除失败不改变业务操作的结果，不回滚已生效的状态变更。
- **清理重试**：成功登记的待清理文件持久保存；删除失败后由后台继续重试，文件已不存在时视为清理成功。

### CreateBucket

```go
// CreateBucket 创建名为 name 的空桶；桶名已存在时返回 BucketNameExistErr。
// 成功时新增该空桶，返回 name，其他桶不变。
// 失败时，系统状态不变。
func (s *Service) CreateBucket(name string) (string, error)
```

### ListBuckets

```go
// ListBuckets 列出所有桶名。
// 成功时返回包含全部桶名的切片。
// 每个桶名只出现一次，顺序不作保证；存储状态不变。
func (s *Service) ListBuckets() ([]string, error)
```

### DeleteBucket

```go
// DeleteBucket 删除名为 name 的空桶。
// 若桶内有对象，返回 BucketNotEmptyErr。
// 未完成分片上传任务不计入桶内对象。
// 成功时删除该桶，使其未完成分片上传任务失效，其他桶不变。
// 失败时存储状态不变。
func (s *Service) DeleteBucket(name string) error
```

### PutObject

```go
// PutObject 从 object 逐段读取内容，上传到名为 bucket 的桶中，以 key 标识。
// object 须为非 nil 的 io.Reader。
// 成功时，若 key 不存在则新增对象，否则用读取的内容完整替换原对象；其他对象和桶不变。
// 读取或保存失败时，桶和对象的可见状态不变。
func (s *Service) PutObject(bucket string, key string, object io.Reader) error
```

### GetObject

```go
// GetObject 获取名为 bucket 的桶中 key 对应对象的读取流和实际大小。
// 若对象不存在，返回 ObjectNotExistErr。
// 成功时返回非 nil 的 io.ReadCloser 和对象大小；调用方负责关闭读取流。
// 无论成功或失败，存储状态不变。
func (s *Service) GetObject(bucket string, key string) (io.ReadCloser, int64, error)
```

### DeleteObject

```go
// DeleteObject 删除名为 bucket 的桶中 key 对应的对象。
// 若对象存在，删除该对象，其他对象和桶不变；对象不存在时也成功，状态不变。
// 失败时，系统状态不变。
func (s *Service) DeleteObject(bucket string, key string) error
```

### ListObjects

```go
// ListObjects 列出名为 bucket 的桶中所有对象的 key。
// 成功时返回包含全部 key 的切片，每个 key 只出现一次，顺序不作保证。
// 无论成功或失败，系统状态不变。
func (s *Service) ListObjects(bucket string) ([]string, error)
```

### 分片上传

除创建分片上传任务外，其他方法参数中的桶和 key 必须与任务绑定的目标一致。

任务不存在、已失效或目标不匹配时返回 `UploadNotExistErr`。

`GetObject`、`ListObjects` 在任务完成前只看到此前已发布的对象，完成时新增或覆盖当时的对象。

```go
type PartInfo struct {
	PartNumber int
	Size       int64
}

// CreateMultipartUpload 为指定桶和 key 创建空任务，totalParts 须为正整数。
// 总片数不合法时返回 InvalidMultipartRequestErr。
// 成功时返回唯一、不复用的非空 uploadID；已发布对象及其他任务不变。
func (s *Service) CreateMultipartUpload(bucket, key string, totalParts int) (uploadID string, err error)

// UploadPart 完整保存指定分片。
// content 须为非 nil 的 io.Reader。
// partNumber 须在 1～totalParts 内，内容须非空，否则返回 InvalidPartErr。
// 分片可按任意顺序上传。
// 成功时返回该片编号和实际字节数；同编号的旧片被替换。
// 读取或保存失败时保留原有分片；已发布对象及其他分片不变。
func (s *Service) UploadPart(bucket, key, uploadID string, partNumber int, content io.Reader) (PartInfo, error)

// ListParts 返回任务中已成功保存的分片编号和大小，按编号升序排列。
// 不改变存储状态。
func (s *Service) ListParts(bucket, key, uploadID string) ([]PartInfo, error)

// CompleteMultipartUpload 按编号顺序将任务的全部分片发布为一个对象。
// 缺少 1～totalParts 中任一分片时返回 IncompleteUploadErr，任务和对象不变。
// 成功时一次性新增或完整覆盖目标 key，使任务失效；其他任务和对象不变。
func (s *Service) CompleteMultipartUpload(bucket, key, uploadID string) error

// AbortMultipartUpload 使任务及其分片不再可访问；已发布对象及其他任务不变。
func (s *Service) AbortMultipartUpload(bucket, key, uploadID string) error
```

完成或取消后的任务失效，重复完成或取消也返回 `UploadNotExistErr`。
