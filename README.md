# 从零实现一个 S3

本仓库尝试从 beginner 视角逐步实现一个对象存储程序。

## 目录结构

| 路径 | 内容 |
| --- | --- |
| `main.go` | 程序入口：装配存储服务与 HTTP 处理器，监听 `127.0.0.1:8080` |
| `objectstorage/` | 桶与对象的核心操作，并发协调以及 SQLite + FileBlob 的本地持久化 |
| `httpserver/` | HTTP 层，把操作映射成路由并负责流式读写 |
| `web/` | 挂在 `/_ui/` 下的前端静态资源|
| `docs/` | `ADT.md` 数据模型、`specification.md` 规格、`design.md` 设计文档 |

```bash
make help
```
