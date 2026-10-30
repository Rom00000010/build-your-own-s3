# 从零实现一个 S3

本仓库尝试从 beginner 视角逐步实现一个对象存储产品。

## 目录结构

| 路径 | 内容 |
| --- | --- |
| `main.go` | 程序入口：装配存储服务与 HTTP 处理器，监听 `127.0.0.1:8080` |
| `objectstorage/` | 桶与对象的核心操作，以及 SQLite + diskv 的本地持久化 |
| `httpserver/` | HTTP 层，把操作映射成路由并负责流式读写 |
| `web/` | 挂在 `/_ui/` 下的前端静态资源|
| `docs/` | `ADT.md` 数据模型、`specification.md` 规格、`design.md` 设计文档 |

## 运行

在仓库根目录执行：

```bash
make help    # 查看命令说明
make build   # 构建程序并复制页面到部署目录
make run     # 后台运行；已运行时先停止再启动
make stop    # 停止服务
make clean   # 停止服务并删除整个部署目录，包括对象数据
```

`make build` 把程序和 `web/` 复制到 `~/tmp/my-s3/`，`make run` 从该目录启动。对象数据位于 `~/tmp/my-s3/data/`，页面位于 `~/tmp/my-s3/web/`，运行日志为 `~/tmp/my-s3/server.log`。修改代码或页面后，先重新构建，再运行。

启动后访问 <http://127.0.0.1:8080/_ui/>。
