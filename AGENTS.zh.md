[English](AGENTS.md) · [中文](AGENTS.zh.md)

# mdm/product

开发本组件的 AI 指南。用法、边界与契约：BRICKKIT.md；为什么长这样：`docs/design.zh.md`；依赖、配置与部署：component.yaml。

## 代码地图

| 路径 | 负责 |
|---|---|
| `backend/module/module.go` | 唯一入口 `New(ctx, rt)`：组装 repo → service → HTTP + gRPC，启动 Outbox 推送与分区循环。独立运行与进外壳是同一个函数 |
| `backend/cmd/server/main.go` | 一行 `besdk.RunStandalone(module.New)` |
| `backend/cmd/migrate/main.go` | 一行 `migrate.Main(migrations.FS)`：迁移容器的入口（`./migrate up`） |
| `backend/internal/repo/repo.go` | 共用类型、哨兵错误（`ErrVersionConflict`、`ErrNotFound`、`ErrInvalidCursor`、`ErrSKUTaken`、`ErrInvalidReference`）、`classifyWriteErr`、列清单与扫描函数 |
| `backend/internal/repo/write.go` | 三个写命令；每个都在一个事务里做 查幂等键 → 写 → 记幂等键 → Outbox（`Update` 与 `SetStatus` 共用 `updateWithVersion`） |
| `backend/internal/repo/read.go` | `BatchGet`、`List`（关键字 `q`、排序档位、keyset 分页）、拼 SQL 的 `listSQL` |
| `backend/internal/repo/convert.go` | `ConvertQuantity`：算术与舍入都在 SQL 里按 `NUMERIC` 做 |
| `backend/internal/repo/cursor.go` | 列表游标：`created_at` 与 `id` 的 base64，翻到"只是包含"那一档后再带上档位 |
| `backend/internal/service/` | 入参校验（十进制字符串、状态值）与错误 → gRPC 状态码映射（`status.go`），REST 与 gRPC 共用 |
| `backend/internal/http/http.go` | REST 路由，每条都带权限键注册；`parseListInput` |
| `backend/internal/grpc/grpc.go` | `mdm.product.v1.ProductService` 与枚举映射 |
| `backend/internal/partition/` | 后台循环：给 `event_outbox` / `event_inbox` 提前 4 周建周分区 |
| `migrations/` | SQL 迁移（业务表、Outbox、播种的计量单位），由 `migrations/embed.go` 嵌进二进制 |
| `contracts/` | proto、OpenAPI、事件 schema |
| `gen/mdm/product/` | 生成的 Go 代码：独立的嵌套 Go 模块，单独打 tag（gen/mdm/product/v1.x.y）；不手改 |
| `scripts/` | 本地演示数据 `seed.sh` / `seed-clean.sh` |

| 要做的事 | 从这里开始 | 然后 |
|---|---|---|
| 列表的关键字搜索 `q` | `backend/internal/repo/read.go`（`listSQL`） | `backend/internal/repo/cursor.go`、`backend/internal/repo/search_test.go` |
| 新增一个 REST 查询参数 | `backend/internal/http/http.go`（`parseListInput`） | `contracts/product.openapi.yaml`、`backend/internal/http/http_test.go` |
| 某条路由要哪个权限键 | `backend/internal/http/http.go`（`RegisterRoutes`） | `assembly.yaml`（`permissions`）、`backend/internal/http/authz_test.go` |
| 给产品加一个字段 | `migrations/`（新迁移） | `contracts/`（只增）、`buf generate`、`backend/internal/repo/repo.go`（`productColumns`、`scanProductRow`） |
| 单位换算或舍入 | `backend/internal/repo/convert.go` | `migrations/003_seed_uoms.up.sql`、`backend/internal/repo/repo_test.go` 里的 `TestConvertQuantity_` 系列 |
| 某个错误回错了状态码 | `backend/internal/service/status.go` | `backend/internal/repo/repo.go` 里的哨兵错误与 `classifyWriteErr` |

## 构建与测试

```bash
# 测试连测试库 brickkit_test_db，绝不连 brickkit_db
# （在 BrickEnterprise 项目里由项目根的 make test-db-init ID=mdm/product 准备）
export TEST_PG_DSN="postgres://<用户>:<密码>@localhost:5432/brickkit_test_db?sslmode=disable"
make test                    # 每个包都以 "ok" 结尾；没设 TEST_PG_DSN 时拒绝运行
go test ./... -count=1 -v | grep -c -- '--- SKIP'   # 0：没有被跳过的测试
make check-version dag-check contract-check import-scan module-check   # 各打印一行 ✓
make docs-check              # "0 with errors, 0 warnings"
# 迁移，以登录角色运行：
PG_HOST=localhost PG_PORT=5432 PG_DATABASE=brickkit_test_db PG_USER=mdm_product_rw \
  PG_PASSWORD=<它的密码> PG_SCHEMA=mdm_product make migrate-idempotent   # ✓ 迁移幂等
```

`contract-check` 会打印它对比的 tag（`buf breaking --against '.git#tag=…'`）：必须是上一个发布版本，不能是 `main`。`buf generate` 之后（契约变了），契约包要打新 tag：见易错点。真机：在项目根 `make verify ID=mdm/product ROUTE=/mdm/product/products FOCUS=1`，它构建镜像、只起本组件需要的东西、核对迁移、健康与权限判定、跑一次 focus，然后收尾。

## 设计取舍

- **被所有人读、自己不调任何人。** 没有依赖、不消费事件：主数据在调用图里始终是叶子。调用方一张订单、一页列表的产品用一次 `BatchGet` 取回，不循环调 `Get`。
- **没有行级数据范围**（`data_scopes: none`）：产品主数据是共享的参照数据。
- **每个写操作都幂等、带版本。** `idempotency_key` 是 `command_idempotency` 的主键，与写操作在同一个事务里记下；`Update` / `SetStatus` 带当前 `version`。停用不是终态，所以什么都不归档。
- **启用 / 停用单独一个权限键** `mdm.product.set_status`，与 `mdm.product.edit` 分开。
- **换算的算术留在 `NUMERIC` 里**：换算因子只存一个方向（`uom_conversions.factor`，1 个本单位 = factor 个基准单位），结果按目标单位的 rounding 向上取整（`CEIL`），不同类别的单位拒绝换算。
- **搜索排序在 SQL 里**：`match_rank`（0 = SKU 或名称以 `q` 开头，1 = 只是包含）是 keyset 的一部分；用 `strpos` / `starts_with` 而不是 `LIKE`。不加索引。默认 90 天窗口对搜索同样生效。
- **调用方的错一律 4xx**：SKU 被占用（`ErrSKUTaken`）、单位或分类不存在（`ErrInvalidReference`）、不是数字的 ID（当作不存在）、写错的十进制数或状态值。

## 易错点

| 不要 | 症状 | 为什么 |
|---|---|---|
| 往 `dependencies.components` 加任何一条（尤其 `erp/inventory`），或消费事件 | 编译、测试全过；组件不再是叶子，别人再加一条边就可能成环 | 主数据被所有人读；产品页上的库存由调用方拼起来。`make dag-check` 对任何依赖都失败 |
| 给 `products` 加列却不在同一次改动里改 `scanProductRow` | 别的测试全过；`BatchGet` / `Get` 报 `sql: expected 12 destination arguments in Scan, not 11` | `besdk.BatchGetRouted` 跑的是 `SELECT *`，表的列序就是扫描顺序；新列加在末尾并扫进来 |
| 从事件 payload 里去掉 `tracking_type` | 本组件测试照绿；`erp/inventory` 不再知道哪些产品收货时要批次号 / 序列号 | 它的摘要副本就是从这些事件建的 |
| 换算向下取整，或在 Go 里用 `float64` 算 | "需要 2.3 箱"只发 2 箱；0.001 这类小因子会漂 | 出库绝不能少发；`NUMERIC` 让因子保持精确 |
| 返回调用方传入的 `standard_cost`，而不是读回落库值 | `Create` 回 `"0"`，随后 `Get` 同一行回 `"0.00"` | `NUMERIC(18,2)` 会规整数值；`insertProduct` 用 `RETURNING` 读回整行 |
| 改了 `contracts/mdm/product/v1/product.proto` 的注释或选项却不给契约包打 tag | 本地编译全过（`replace` 把问题盖住了）；外壳拉取 `mdm-product/v2` 时，对着 `go.mod` require 的那个 tag 上的旧 `gen/` 编译 | `gen/mdm/product/` 下的任何变化都要打新 tag `gen/mdm/product/v1.x.y`，并让根 `go.mod` require 它 |
| 列表按 `match_rank, created_at DESC, id DESC` 以外的顺序排，或把 `rank` 从游标里去掉 | 恰好在"前缀匹配结束、包含匹配开始"的地方翻页漏行或重复 | `listSQL` 的 keyset 条件以这个顺序为前提；`TestList_q与游标一起翻页不重不漏` 守着它 |
| 把 `POST /products/{id}/status` 改回绑 `mdm.product.edit` | 别处的测试照绿；能改名字的人也能让产品从所有选择器里消失 | `TestSetStatus路由要set_status键_只有edit键得到403` 守着它 |

## 改代码前自查

1. 这件事是关于产品"是什么"（这里）、"库存有多少"（`erp/inventory`）还是"卖多少钱"（`erp/sales`）？只有第一种放这里。
2. 我是不是在加依赖、加消费的事件、分区或数据范围列？停下：看易错点与 `docs/design.zh.md`。
3. 改契约？只增：新字段、新 rpc、新查询参数。绝不删除或改类型（字段、rpc、路径、事件 subject 都一样）。
4. `gen/` 变了吗？变了就要给契约包打新 tag，`go.mod` 也要 require 它。
5. 新的 REST 路由用 `besdk.GET` / `POST` / `PATCH` 注册，并带 `assembly.yaml` 里的权限键。
6. 新规则 → 先写会失败的测试（凡是 SQL 都连真实数据库），再写代码。
7. 发布之后的第一个改动之前先升 `metadata.version`；BRICKKIT.md 与 `docs/design.md` 与代码在同一个提交里更新。
