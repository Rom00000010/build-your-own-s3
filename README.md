# 从零实现一个 S3

本仓库尝试从 beginner 视角逐步实现一个对象存储产品。

## 目录结构

| 路径 | 内容 |
| --- | --- |
| `main.go` | 程序入口：装配存储与 HTTP 处理器，监听 `127.0.0.1:8080` |
| `objectstorage/` | 桶与对象的核心操作，以及 SQLite + diskv 的本地持久化 |
| `httpserver/` | HTTP 层，把操作映射成路由并负责流式读写 |
| `web/` | 挂在 `/_ui/` 下的静态页面、样式和脚本 |
| `docs/` | `ADT.md` 数据模型、`specification.md` 对外行为、`design.md` 实现方案 |
| `browser-tests/` | 浏览器端测试 |

## 运行

在仓库根目录执行：

```bash
make run     # 构建并后台启动
make stop    # 停掉这个服务
make stage   # 只构建，不启动
```

`make run` 它把二进制和 `web/` 复制到 `~/tmp/my-s3/`，再切到那个目录启动，所以运行期的相对路径都以部署目录为基准：对象数据落到 `~/tmp/my-s3/data/`，页面从 `~/tmp/my-s3/web/` 读取。

启动后访问 <http://127.0.0.1:8080/_ui/>。
