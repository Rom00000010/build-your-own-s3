# 对象存储 HTTP 规格

客户端通过 `127.0.0.1:8080` 访问服务。路径和方法采用 S3 的路径式形式；请求和响应格式由本项目定义，不要求 AWS SDK 兼容，也不要求认证或签名。

服务收到 SIGINT 或 SIGTERM 后停止接收新请求，等待正在处理的请求完成，再关闭存储并正常退出。

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
| 桶已存在 | `409 Conflict` | `BucketNameExist` |
| 删除非空桶 | `409 Conflict` | `BucketNotEmpty` |
| 桶不存在 | `404 Not Found` | `BucketNotExist` |
| 对象不存在（GetObject） | `404 Not Found` | `ObjectNotExist` |
| 服务端存储发生未预期错误 | `500 Internal Server Error` | `InternalError` |

### 输入与行为规则

- **桶名**：长 1–63 个字符，仅含小写英文字母、数字和连字符；首尾必须是字母或数字。
- **key**：长 1–255 个字符，仅含英文字母、数字、点、下划线和连字符；首字符必须是英文字母或数字，不允许 `/`。
- **上传**：同一 key 再次上传会完整覆盖原内容。
- **删除**：key 不存在时仍返回 `204`。
- **列表**：包含桶内全部 key，每个只出现一次，顺序不保证；空桶返回 `{"keys":[]}`。
- **桶列表**：包含全部桶名，每个只出现一次，顺序不保证；没有桶时返回 `{"buckets":[]}`。
- **删除桶**：仅能删除空桶；桶不存在时返回 `BucketNotExist`，非空时返回 `BucketNotEmpty`。失败时状态不变。

内部spec见 [存储服务规格](service/specification.md)。
