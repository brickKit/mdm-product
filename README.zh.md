[English](README.md) · [中文](README.zh.md)

# mdm/product

SKU、分类、计量单位换算、批次 / 序列号追踪策略、标准成本。

## 在项目里使用

```bash
brickkit add mdm/product@2.0.0
brickkit up
```

先做准备：见 BRICKKIT.zh.md 的"部署前准备"（PostgreSQL schema 与登录角色、NATS、授权与 JWKS 地址，以及给负责启用 / 停用产品的人授权限键 `mdm.product.set_status`）。

## 文档

| 想知道 | 读 |
|---|---|
| 它做什么、不做什么，怎么配置，要准备什么 | [BRICKKIT.zh.md](BRICKKIT.zh.md) |
| 它的 gRPC、REST 与事件契约 | [contracts/](contracts/) |
| 为什么这样设计：边界、数据、单位换算、搜索排序、未决问题 | [docs/design.zh.md](docs/design.zh.md) |
| 它依赖什么（什么都不依赖）、有哪些配置键 | [component.yaml](component.yaml) |
| 怎么开发：代码地图、测试、易错点 | [AGENTS.zh.md](AGENTS.zh.md) |

## 开发

Go 1.25、Gin、`database/sql` + pgx，迁移经 be-sdk-go 用 golang-migrate。测试需要真实的 PostgreSQL（`TEST_PG_DSN`）；命令与成功的样子见 AGENTS.zh.md 的"构建与测试"。在 BrickEnterprise 项目里，项目根的 `make verify ID=mdm/product FOCUS=1` 真机跑一遍（容器形态与本机进程形态），结束后收尾。
