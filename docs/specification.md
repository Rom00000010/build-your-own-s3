# 对象存储 specification

## 客户端：HTTP 接口

客户端通过 `127.0.0.1:8080` 访问服务。路径和方法采用 S3 的路径式形式；请求和响应格式由本项目定义，不要求 AWS SDK 兼容，也不要求认证或签名。

| 操作 | 请求 | 请求体 | 成功响应 |
| --- | --- | --- | --- |
| CreateBucket | `PUT /{bucket}` | 无 | `201 Created`，无响应体 |
| ListBuckets | `GET /` | 无 | `200 OK`，`Content-Type: application/json`，响应体为 `{"buckets":["photos","documents"]}` |
| DeleteBucket | `DELETE /{bucket}` | 无 | `204 No Content`，无响应体 |
| PutObject | `PUT /{bucket}/{key}` | 原始对象字节 | `204 No Content`，无响应体 |
| GetObject | `GET /{bucket}/{key}` | 无 | `200 OK`，响应体为原始对象字节；`Content-Type: application/octet-stream`，`Content-Length` 为对象大小 |
| DeleteObject | `DELETE /{bucket}/{key}` | 无 | `204 No Content`，无响应体 |
| ListObjects | `GET /{bucket}` | 无 | `200 OK`，`Content-Type: application/json`，响应体为 `{"keys":["a.txt","b.txt"]}` |

成功响应也表示操作已经生效：创建后可在桶列表中看到空桶；删除空桶后它不再出现在列表中；上传后可下载该 key 的完整内容，同 key 覆盖后下载得到新内容；删除对象后该 key 不再可下载。

上传与下载使用一个 HTTP 请求逐段传输对象内容。

### 错误响应

通常，失败响应使用下表中的状态码，设置 `Content-Type: application/json`，并返回 `{"error":"错误名"}`。

| 情况 | 状态码 | 错误名 |
| --- | --- | --- |
| 创建时桶名不合法 | `400 Bad Request` | `InvalidBucketName` |
| key 不合法 | `400 Bad Request` | `InvalidObjectKey` |
| 上传内容为空 | `400 Bad Request` | `EmptyObject` |
| 桶已存在 | `409 Conflict` | `BucketNameExist` |
| 删除非空桶 | `409 Conflict` | `BucketNotEmpty` |
| 桶不存在 | `404 Not Found` | `BucketNotExist` |
| 对象不存在（GetObject） | `404 Not Found` | `ObjectNotExist` |
| 服务端存储发生未预期错误 | `500 Internal Server Error` | `InternalError` |

### 输入与行为规则

- **桶名**：长 1–63 个字符，仅含小写英文字母、数字和连字符；首尾必须是字母或数字。
- **key**：长 1–255 个字符，仅含英文字母、数字、点、下划线和连字符；首字符必须是英文字母或数字，不允许 `/`。
- **上传**：内容必须非空；同一 key 再次上传会完整覆盖原内容。
- **删除**：key 不存在时仍返回 `204`。
- **列表**：包含桶内全部 key，每个只出现一次，顺序不保证；空桶返回 `{"keys":[]}`。
- **桶列表**：包含全部桶名，每个只出现一次，顺序不保证；没有桶时返回 `{"buckets":[]}`。
- **删除桶**：仅能删除空桶；桶不存在时返回 `BucketNotExist`，非空时返回 `BucketNotEmpty`。失败时状态不变。
- **本阶段范围**：不支持分页、Range 请求或分片上传。

## 服务端：存储接口与运行行为

存储接口由 `build_your_own_s3/objectstorage` 包提供。

### 启动、关闭与持久化

服务启动时调用 `Open` 打开当前工作目录下的 `data/`，监听 `127.0.0.1:8080`；正常停止时调用 `Close`。客户端表中的七个操作分别对应下面的同名服务端方法，业务错误按客户端错误表返回。`Open` 和 `Close` 只供服务端使用。

```go
// Open 打开当前工作目录下 data 目录中的存储；若尚无数据，则创建空存储。
// 成功时返回可操作的 Service 实例和 nil；失败时返回 nil 和非 nil 错误。
func Open() (*Service, error)

// Close 关闭该实例。仅在成功调用 Open 后、尚未关闭时调用。
// 成功时返回 nil，之后不可继续使用该实例；可重新调用 Open 获取新实例。
// 关闭失败时返回非 nil 错误。
func (s *Service) Close() error
```

成功的桶和对象操作在正常关闭后再次打开时仍然可见，包括新建或删除的桶、上传或覆盖的对象，以及已删除的对象。进程崩溃或断电后的恢复不在当前规格内。

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
// ListBuckets 列出所有桶名，需已成功调用 Open。
// 成功时返回包含全部桶名的非 nil 切片和 nil；没有桶时返回空切片。
// 每个桶名只出现一次，顺序不作保证；存储状态不变。
// 读取存储失败时返回 nil 和非 nil 错误。
func (s *Service) ListBuckets() ([]string, error)
```

### DeleteBucket

```go
// DeleteBucket 删除名为 name 的空桶，需已成功调用 Open。
// 若桶不存在，返回 BucketNotExistErr；若桶内有对象，返回 BucketNotEmptyErr。
// 成功时删除该桶并返回 nil，其他桶及对象不变。
// 失败时存储状态不变，返回非 nil 错误。
func (s *Service) DeleteBucket(name string) error
```

### PutObject

```go
// PutObject 从 object 逐段读取内容，上传到名为 bucket 的桶中，以 key 标识。
// 需已成功调用 Open，object 须为非 nil 的 io.Reader。
// key 长 1–255 个字符，仅含英文字母、数字、点、下划线和连字符，
// 且首字符必须是英文字母或数字。
// 若桶不存在，返回 BucketNotExistErr；若 key 不合法，返回 InvalidObjectKeyErr。
// 若读到 EOF 时内容为空，返回 EmptyObjectErr。
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
