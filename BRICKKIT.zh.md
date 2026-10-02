# mdm/product

## 组件定位

每个产品"是什么"的唯一记录：SKU、名称、分类、基准计量单位、是否按批次或序列号追踪、标准成本、状态（启用或停用），以及计量单位和单位之间的换算因子。任何需要产品的组件都按 ID 从这里读，最多保存一份由本组件事件更新的摘要副本。

**负责**

- 产品主数据：`sku`（唯一；调用方留空时生成 `P` + 6 位数字）、名称、分类、基准单位、`tracking_type`（`NONE`、`BATCH` 或 `SERIAL`）、标准成本（十进制字符串）、状态。
- 计量单位及其换算因子，以及换算本身（`ConvertQuantity`：按目标单位的 rounding 向上取整）。单位由迁移播种（`EA`、`BOX`、`KG`、`G`；1 箱 = 12 个，1 克 = 0.001 千克），没有管理接口。
- 产品分类（一张带物化路径的表），目前还没有管理接口。
- 事件 `mdm.product.created.v1`、`mdm.product.updated.v1`、`mdm.product.disabled.v1`。

**不负责**

- 库存数量与实际的批次号 / 序列号：`erp/inventory`。本组件只说一个产品收货时要不要批次号或序列号，不说是哪一个。
- 售价、价格表、折扣：`erp/sales`。
- 实际成本（按库存流水算出的移动加权或先进先出成本）：`erp/inventory` 与 `erp/finance`。这里的标准成本是人工维护的值。
- 谁能看哪些行：这里没有行级数据范围；能打开产品页面的人都能看到全部产品主数据。

## 部署前准备

- **PostgreSQL**：schema `mdm_product`；如果你的 schema 约定会建 `mdm_product_archive` 也可以有（本组件从不写它）。登录角色 `mdm_product_rw`，在 `mdm_product` 上有 `USAGE` 与 `CREATE`，以及它的密码。迁移以这个角色运行并建表（同时播种四个计量单位），所以表归这个角色所有；这一点重要，因为运行中的组件要自己给 `event_outbox` / `event_inbox` 建周分区，这需要表的所有权。这些 BrickKit 都不建；在 BrickEnterprise 装配项目里由 `make dev-env` 把密码写进 `.env`、`make db-init` 建 schema、角色与授权。
- **NATS**：`NATS_URL` 可达。组件经 Outbox 表和后台推送发布事件；NATS 不可达时照样启动，事件留在 Outbox 里等。
- **授权**（`infra/authz`）与**身份**（`infra/iam-casdoor`，或任何提供 JWKS 的 IAM）：`AUTHZ_BUNDLE_URL` 与 `IAM_JWKS_URL` 可达，REST 路由才会回错误以外的东西。它们是配置，不是依赖：没有它们组件照样启动。
- **权限键**：启用 / 停用产品（`POST /products/{id}/status`）要求单独的权限键 `mdm.product.set_status`；`mdm.product.edit` 只管编辑，所以要把 `mdm.product.set_status` 授给能启用 / 停用产品的角色。
- 演示数据（可选）：组件跑起来之后，在组件目录 `make seed` 经真实 gRPC 接口建 14 个示例产品。

## 依赖说明

无。产品主数据被所有人读、自己不调任何人：别的组件经 gRPC 调它（一张订单、一页列表的产品用一次 `BatchGet` 取回）；它从不调用别的组件，也不消费事件，所以永远不会处在调用链中间或环里。尤其不调 `erp/inventory`：要显示库存的产品页分别问两个组件，再把结果拼起来。

授权 bundle 与 JWKS 从配置里的 URL 拉取，不经依赖边。没有它们时，每条受保护路由都失败关闭：没有或无效的 token → `401`；`IAM_JWKS_URL` 为空或不可达 → `403`；bundle 还没拉到过 → `503`；合法用户但没有该权限键 → `403`。`/healthz` 始终是 `200`：它只报告本进程活着。

## 配置指南

| 变量 | 含义 |
|---|---|
| `PG_HOST` | PostgreSQL 主机。通常写项目共享值（`$var:PG_HOST`）。 |
| `PG_PORT` | PostgreSQL 端口；默认 `5432`。 |
| `PG_DATABASE` | 存放 `mdm_product` schema 的数据库（`$var:PG_DATABASE`）。 |
| `PG_USER` | 登录角色，写字面量 `mdm_product_rw`。进外壳时外壳用自己的角色登录，每个事务里 `SET LOCAL ROLE` 切到这个角色，所以角色名必须是 `<PG_SCHEMA>_rw`。 |
| `PG_PASSWORD` | `PG_USER` 的密码。密钥：写 `${MDM_PRODUCT_DB_PASSWORD}`（或你的密钥库的引用），绝不写值。 |
| `PG_SCHEMA` | 全部表、Outbox 与迁移状态表（`schema_migrations_mdm_product`）所在的 schema；默认 `mdm_product`。照样写出字面量，让每个组件的 schema 在一处看得见。 |
| `NATS_URL` | Outbox 推送发布事件的 NATS 地址（`$var:NATS_URL`）。 |
| `OTEL_BASE_URL` | OpenTelemetry collector 的基础地址；为空（默认）时不导出。 |
| `AUTHZ_BUNDLE_URL` | 权限判定轮询的授权 bundle 地址，例如 `http://infra-authz-2-0-0:8223/authz/bundle`。必填：没有它，每条受保护路由都回 `503`。要与项目里运行的 authz 版本保持一致。 |
| `IAM_JWKS_URL` | 本地验签用户 token 用的 JWKS 地址，例如 `http://infra-iam-casdoor-2-0-0:8200/.well-known/jwks.json`。必填：没有它，每条受保护路由都回 `403`。 |

## 契约索引

- `contracts/mdm/product/v1/product.proto` —— 给别的组件用的 gRPC `mdm.product.v1.ProductService`：`Create`、`Update`、`SetStatus`（都带 `idempotency_key`；`Update` 与 `SetStatus` 还带当前 `version`，过期时 `ABORTED`，产品不存在时 `NOT_FOUND`；`sku` 已被占用 → `ALREADY_EXISTS`；单位或分类不存在 → `INVALID_ARGUMENT`）、`Get`、`List`（关键字 `q` 匹配 SKU 或名称，大小写不敏感，前缀匹配在前；`status_filter`；`created_after` / `created_before`，默认最近 90 天；游标分页，没有 offset）、`BatchGet`（查不到的 ID 放在 `missing_ids` 里返回，不报错）、`GetSummary`（ID、SKU、名称、基准单位、追踪类型、状态、版本——摘要副本要的字段）、`ConvertQuantity`（不落库；按目标单位的 rounding 向上取整；两个单位不同类别 → `INVALID_ARGUMENT`）。Go 包是独立模块 `github.com/brickKit/mdm-product/gen/mdm/product`。
- `contracts/product.openapi.yaml` —— `/mdm/product` 下的 REST，每条路由都有权限键：`GET /products`（`mdm.product.view`；与 gRPC `List` 相同的 `q`、`status_filter`、`created_after` / `created_before` 与 `cursor`、`page_size`）、`GET /products/{id}`（`mdm.product.view`）、`POST /products`（`mdm.product.create`）、`PATCH /products/{id}`（`mdm.product.edit`）、`POST /products/{id}/status`（`mdm.product.set_status`；"删除"就是停用）、`POST /products/convert-quantity`（`mdm.product.view`）。`BatchGet` 与 `GetSummary` 不在 REST 上。
- `contracts/events/product.events.json` —— 经 Outbox 发布的事件：`mdm.product.created.v1`（新建时）、`mdm.product.updated.v1`（修改时，以及重新启用时）、`mdm.product.disabled.v1`（停用时）。每条都带完整产品（含 `tracking_type`）与聚合 `version`；消费者只在版本大于本地持有的版本时才应用。不消费任何事件。
- `assembly.yaml` —— 本项目的元数据：上面四个权限键、菜单项、网关路由 `/mdm/product/**`、schema 与角色、`data_scopes: none`。

## 外壳声明

不是外壳。它可以被 Go 外壳托管（在 BrickEnterprise 项目里是 `be/go-core`），也可以独立运行；两种方式代码相同。
