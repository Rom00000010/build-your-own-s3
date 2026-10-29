# 对象存储设计

[ADT.md](ADT.md) 定义桶、key 和对象内容；[specification.md](specification.md) 定义七个对外操作的行为。

`s3.go` 用两层 map 保存状态：外层按桶名索引桶，内层按 key 索引对象的字节内容；空桶由空的内层 map 表示。

状态只保存在进程内，不持久化，退出后即消失。
