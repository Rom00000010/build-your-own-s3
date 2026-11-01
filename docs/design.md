# 对象存储设计

本设计通过 HTTP 接口提供对象存储服务，并将桶和对象持久化到本地磁盘。

[ADT.md](ADT.md) 定义存储模型；[specification.md](specification.md) 定义 HTTP 契约；[Service 规格](service/specification.md) 定义内部存储接口。

## 架构与模块

服务入口组装 HTTP 层和 Service；HTTP 层调用 Service，Service 协调元数据与底层文件存储。

| 模块 | 职责 |
| --- | --- |
| 服务入口 | 打开存储、启动 HTTP 服务并管理关闭流程 |
| HTTP 层 | 解析请求、调用 Service，将操作结果转换为 HTTP 响应 |
| Service | 校验业务参数，协调元数据与文件操作 |
| 元数据 | 通过 SQLite 管理桶、对象 key、内部文件名和对象大小 |
| 文件存储 | 封装 FileBlob，生成内部文件名并读写、删除对象文件 |

## 数据表示

数据位于当前工作目录的 `data/` 下：`metadata.db` 保存 SQLite 元数据，`objects/` 保存对象文件。

- `buckets(name)` 记录桶。
- `objects(bucket_name, key, file_name, size)` 将对象 key 映射到底层存储文件名，并记录字节大小；以 `(bucket_name, key)` 为主键，通过外键关联桶。
- 底层存储文件名采用随机生成的内部名称，不直接使用用户 key。每次上传写入新文件，使覆盖操作可以通过切换元数据引用完成。

## 并发控制

HTTP 层不持有锁，各请求在自己的 goroutine 中调用 Service，由 Service 协调并发。

Service 按 `(bucket, key)` 使用互斥锁。`PutObject`、`GetObject`、`DeleteObject` 从方法开始到返回全程持锁，包括同步清理；不同对象使用不同的锁。

SQLite 通过事务协调元数据访问，FileBlob 支持不同文件并行传输（diskv不支持）；`ListObjects` 依靠数据库查询快照，不取对象锁；桶操作也不取对象锁，其并发限制由调用方保证。

## 关键路径

程序启动时打开存储并开始监听 HTTP 请求；停止时等待正在处理的请求结束，再关闭存储。

HTTP 层按请求方法和路径选择操作，将桶名、key 和上传内容交给 Service，再将结果转换为状态码、响应头和响应体。上传和下载通过流传递内容，无需先将整个对象读入内存。

桶操作和对象列表只访问元数据; 涉及对象内容的操作由 Service 协调，从方法开始到返回全程持锁：

- **上传与覆盖**：完整写入新文件后，更新元数据中的文件引用和大小，再同步清理旧文件。
- **读取**：在对象锁内查找并打开文件，返回读取流时释放锁。HTTP 层负责传输和关闭读取流，后续覆盖或删除不影响该流读取原版本。
- **删除**：先删除元数据记录，再同步清理对应文件。
