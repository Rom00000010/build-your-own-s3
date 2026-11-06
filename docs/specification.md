# 对象存储 HTTP 规格

客户端通过 `127.0.0.1:8080` 访问服务。路径和方法采用 S3 的路径式形式；请求和响应格式由本项目定义，不要求 AWS SDK 兼容，也不要求认证或签名。

| 操作 | 请求 | 请求体 | 成功响应 |
| --- | --- | --- | --- |
| CreateBucket | `PUT /{bucket}` | 无 | `201 Created`，无响应体 |
| ListBuckets | `GET /` | 无 | `200 OK`，`Content-Type: application/json`，响应体为 `{"buckets":["photos","documents"]}` |
| DeleteBucket | `DELETE /{bucket}` | 无 | `204 No Content`，无响应体 |
| PutObject | `PUT /{bucket}/{key}` | 原始对象字节 | `204 No Content`，无响应体 |
| GetObject | `GET /{bucket}/{key}` | 无 | 完整下载返回 `200 OK`，range 下载返回 `206 Partial Content`；响应体为对应字节，`Content-Type: application/octet-stream`，`Content-Length` 为响应体大小，并返回 `ETag`、`Accept-Ranges: bytes`；range 下载还返回 `Content-Range` |
| DeleteObject | `DELETE /{bucket}/{key}` | 无 | `204 No Content`，无响应体 |
| ListObjects | `GET /{bucket}` | 无 | `200 OK`，`Content-Type: application/json`，响应体为 `{"keys":["a.txt","b.txt"]}` |
| CreateMultipartUpload | `POST /{bucket}/{key}?uploads` | JSON `{"totalParts":3}` | `201 Created`，JSON `{"uploadId":"..."}` |
| UploadPart | `PUT /{bucket}/{key}?uploadId={id}&partNumber={n}` | 该分片的原始字节 | `200 OK`，JSON `{"partNumber":1,"size":1024}` |
| ListParts | `GET /{bucket}/{key}?uploadId={id}` | 无 | `200 OK`，JSON `{"parts":[{"partNumber":1,"size":1024}]}` |
| CompleteMultipartUpload | `POST /{bucket}/{key}?uploadId={id}` | 无 | `204 No Content`，无响应体 |
| AbortMultipartUpload | `DELETE /{bucket}/{key}?uploadId={id}` | 无 | `204 No Content`，无响应体 |

成功响应也表示操作已经生效：创建后可在桶列表中看到空桶；删除空桶后它不再出现在列表中；上传后可下载该 key 的完整内容，同 key 覆盖后下载得到新内容；删除对象后该 key 不再可下载。

分片上传先创建空任务，再逐片保存内容；完成任务时按编号顺序连接全部分片，一次性新增或完整覆盖目标对象。完成前，列表和下载只看到此前已发布的对象。完成或取消后任务失效；取消后任务及其分片不再可访问，已发布对象保持不变。

### 错误响应

通常，失败响应使用下表中的状态码，设置 `Content-Type: application/json`，并返回 `{"error":"错误名"}`。

| 情况 | 状态码 | 错误名 |
| --- | --- | --- |
| 创建时桶名不合法 | `400 Bad Request` | `InvalidBucketName` |
| key 不合法 | `400 Bad Request` | `InvalidObjectKey` |
| 桶已存在 | `409 Conflict` | `BucketNameExist` |
| 删除非空桶 | `409 Conflict` | `BucketNotEmpty` |
| 桶不存在 | `404 Not Found` | `BucketNotExist` |
| 对象不存在（GetObject） | `404 Not Found` | `ObjectNotExist` |
| range 下载请求的范围不可满足 | `416 Range Not Satisfiable` | `InvalidRange` |
| 分片请求的参数、JSON 或 `Content-Type` 不合法，或总片数不是正整数 | `400 Bad Request` | `InvalidMultipartRequest` |
| 分片编号不在任务的 `1～totalParts` 内，或分片内容为空 | `400 Bad Request` | `InvalidPart` |
| 完成任务时缺少分片 | `400 Bad Request` | `IncompleteUpload` |
| 桶存在且 key 合法，但任务不存在、已失效或不属于该桶和 key | `404 Not Found` | `UploadNotExist` |
| 服务端存储发生未预期错误 | `500 Internal Server Error` | `InternalError` |

### 输入与行为规则

- **桶名**：长 1–63 个字符，仅含小写英文字母、数字和连字符；首尾必须是字母或数字。
- **key**：长 1–255 个字符，仅含英文字母、数字、点、下划线和连字符；首字符必须是英文字母或数字，不允许 `/`。
- **上传**：成功时生成独立的Etag，同一 key 再次上传会完整覆盖原内容；失败时保留原内容及 ETag。
- **range 下载**：通过 `Range` 请求单个字节范围，支持指定起止位置、从指定位置到末尾，以及末尾若干字节。超出末尾的部分截断，请求范围不可满足（包括空对象的 range 请求）时返回 `InvalidRange`。未指定 `Range`、格式错误、单位不支持或请求多个范围时返回完整对象。
- **条件 range 下载**：请求携带 `If-Range` 时，仅在其与当前 ETag 匹配时返回所请求的字节，否则返回完整对象。
- **删除**：key 不存在时仍返回 `204`。
- **列表**：包含桶内全部 key，每个只出现一次，顺序不保证；空桶返回 `{"keys":[]}`。
- **桶列表**：包含全部桶名，每个只出现一次，顺序不保证；没有桶时返回 `{"buckets":[]}`。
- **删除桶**：仅能删除没有已发布对象的桶；桶不存在时返回 `BucketNotExist`，非空时返回 `BucketNotEmpty`，这两种错误不改变状态。删除时取消桶内分片上传任务。
- **分片任务**：任务绑定创建时的桶和 key，由唯一、不复用的 `uploadId` 标识。
- **上传分片**：内容非空，分片可按任意顺序上传。同编号再次成功上传会替换旧片，替换失败时保留旧片。
- **分片列表**：只返回已成功保存的分片，按编号升序排列；没有分片时返回 `{"parts":[]}`。
- **完成任务**：成功时发布完整对象及新 ETag；失败时保留已发布对象及其 ETag。缺少分片时任务保持不变，可补片后重试。

### 并发

- **对象与分片操作**：对象由 `(bucket, key)` 标识。不同对象的 `PutObject`、`GetObject`、`DeleteObject`、`CompleteMultipartUpload` 可以并行；同一对象的这些操作由服务端串行处理。分片由 `(uploadId, partNumber)` 标识，不同分片的 `UploadPart` 可以并行，同一分片的上传由服务端串行处理。调用方须避免 `CompleteMultipartUpload` 与同任务的其他分片请求重叠；违反此前提时，行为不作保证。
- **列表**：在其他并发前提满足时，`ListObjects` 和 `ListParts` 可以与上述操作并行；分别返回请求执行期间某一时刻的完整 key 列表或已保存分片列表，不保证包含与该请求重叠的变更。
- **桶与任务管理**：`CreateBucket`、`ListBuckets`、`DeleteBucket`、`CreateMultipartUpload`、`AbortMultipartUpload` 不支持彼此并发，也不支持与上述操作并发。调用方须避免这些请求重叠；违反此前提时，行为不作保证。

内部spec见 [存储服务规格](service/specification.md)。
