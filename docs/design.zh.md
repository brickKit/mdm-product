[English](design.md) · [中文](design.zh.md)

# mdm/product 设计

只写结论，给改本组件设计的人看。怎么用：`BRICKKIT.zh.md`；代码怎么组织：`AGENTS.zh.md`。

## 边界

本组件是产品"是什么"的唯一记录：SKU、名称、分类、基准计量单位、追踪策略（`tracking_type`）、标准成本、状态，以及计量单位和单位之间的换算因子。

| 不在这里 | 归谁 | 为什么 |
|---|---|---|
| 库存数量、实际的批次号与序列号 | `erp/inventory` | 本组件说的是一个产品收货时要不要批次号 / 序列号（主数据），不是具体是哪一个（交易数据） |
| 售价、价格表、折扣 | `erp/sales` | 定价跟着销售走；`standard_cost` 是给财务核算的成本，不是给客户的报价 |
| 实际成本（移动加权、先进先出） | `erp/inventory`、`erp/finance` | 实际成本由流水算出；和人工维护的标准成本混用，成本差异分析就失去意义 |
| 物料清单、工艺路线 | 制造类组件 | 本组件只描述单个产品，不描述产品之间的构成关系 |

删除产品就是停用它（`POST /products/{id}/status`，单独的权限键 `mdm.product.set_status`）；没有删除接口，因为订单和库存流水还引用着这个产品。启用 / 停用与编辑分开授权，是因为停用的产品会从所有选择器里消失：能改个名字的权限不应顺带有这个能力。

## 拥有的数据

| 表 | 分区 | 说明 |
|---|---|---|
| `products` | 否 | `sku` 唯一（`products_sku_uniq`）；`tracking_type` `NONE` / `BATCH` / `SERIAL`；`standard_cost NUMERIC(18,2)`；`status` `ACTIVE` / `DISABLED`；`version` 用于乐观锁；外键指向 `uoms`（基准单位）与 `product_categories` |
| `uoms` | 否 | `code`、`category`（`count`、`weight` 等）、`is_base`（每个类别恰好一个基准单位）、`rounding NUMERIC(18,6)` |
| `uom_conversions` | 否 | 每个非基准单位一行：`factor NUMERIC(18,6)`，1 个本单位 = `factor` 个本类别的基准单位 |
| `product_categories` | 否 | 分类树用物化路径（`path`，前缀索引）：一个子树就是一次前缀匹配，不需要递归查询 |
| `command_idempotency` | 否 | 主键 `idempotency_key`，记第一次执行的结果 ID |
| `event_outbox`、`event_inbox` | 按 `created_at` 周分区 | 推送从这里发布事件；分区由组件自己提前 4 周建好 |

没有终态：`DISABLED` 的意思是"不能用于新业务"，产品可以重新启用。调用方留空 `sku` 时按 ID 序列生成 `P` + 6 位数字，否则按传入的值；`sku` 已被占用是冲突（`409` / `ALREADY_EXISTS`），不是服务端故障。

计量单位是由迁移 `003_seed_uoms` 播种的参照数据：`EA`（个，`count` 类的基准）、`BOX`（= 12 EA）、`KG`（`weight` 类的基准，rounding 0.001）、`G`（= 0.001 KG）。单位与分类都没有管理接口：新增单位类别很少见，为它们做一整套带鉴权的增删改查，成本高于一条迁移。包装规格不同的客户在自己的 fork 里改播种的因子。

**单位换算**（`ConvertQuantity`，本组件里唯一没有参考实现可对照的规则）：

- 因子用 `NUMERIC(18,6)`，不用浮点：换算因子和金额是同一类精度问题，而且会乘进订单数量与金额。
- 只存一个方向（指向基准单位的 `factor`）；反向现算，不存，两个存下来的值就永远不会不一致。
- 不同类别的单位拒绝换算（`InvalidArgument`），不猜。
- 结果按目标单位的 `rounding` **向上**取整（`CEIL`）：出库时"需要 2.3 箱"必须变成 3 箱。收货场景想要别的舍入，由调用方自己处理；默认值宁可多备货，绝不少发货。
- 算术与舍入都在 SQL 里按 `NUMERIC` 做；`trim_scale` 只去掉显示结果末尾的零。

## 契约面

gRPC `mdm.product.v1.ProductService`，给别的组件用：

| rpc | 类型 | 说明 |
|---|---|---|
| `Create` | 写 | 按 `idempotency_key` 幂等；`sku` 被占用 → `ALREADY_EXISTS`；单位或分类不存在 → `INVALID_ARGUMENT` |
| `Update`、`SetStatus` | 写 | 幂等，并带当前 `version`；版本过期 → `ABORTED`，产品不存在 → `NOT_FOUND`；`SetStatus` 只接受 `ACTIVE` / `DISABLED`（未指定 → `INVALID_ARGUMENT`） |
| `Get`、`List` | 读 | `List` 按游标分页，没有 offset；默认窗口最近 90 天；关键字 `q`（见下） |
| `BatchGet` | 读 | 调用方用它代替循环调 `Get`；查不到或不是数字的 ID 放在 `missing_ids` 里 |
| `GetSummary` | 读 | ID、SKU、名称、基准单位、追踪类型、状态、版本：摘要副本要存的字段 |
| `ConvertQuantity` | 读 | 不落库；`qty` 是十进制字符串 |

`/mdm/product` 下的 REST，每条路由都有权限键：`GET /products` 与 `GET /products/{id}`（`mdm.product.view`）、`POST /products`（`mdm.product.create`）、`PATCH /products/{id}`（`mdm.product.edit`；名称、分类、标准成本整体替换）、`POST /products/{id}/status`（`mdm.product.set_status`）、`POST /products/convert-quantity`（`mdm.product.view`：只读换算因子的试算，是"看产品"的一部分）。`BatchGet` 与 `GetSummary` 只在 gRPC 上：没有哪个终端用户界面需要按一串产品 ID 取数据，放到公网 API 上等于邀请人拿它批量导出。`ConvertQuantity` 在 REST 上，是因为下单页要实时显示"3 箱 = 36 个"，而规则只在这里。没有给超时写操作用的状态查询接口：没有任何组件在自己的事务里同步写本组件。

调用方的错一律回 `4xx`，不回 `500`：写错的游标或时间参数、不是纯数字写法的十进制数（不认科学计数法、`NaN`、千分位）、不是数字的 ID（当作不存在）。

**关键字搜索**（`GET /products?q=`、gRPC `ListRequest.q`），给产品选择器用（下单、商机建档、移动 BFF 的 `searchProducts`，它走 gRPC）：

- `q` 先去掉首尾空白；为空就是不过滤，结果与不带 `q` 的列表完全相同。可与 `status_filter` 同时用。
- `lower(sku)` 或 `lower(name)` 包含 `lower(q)` 就算命中。用 `strpos` 与 `starts_with`，不用 `LIKE`：用户输入里的 `%` 与 `_` 是普通字符，不需要转义。
- SKU 或名称以 `q` 开头的档位为 0，只是包含的档位为 1。排序：档位，然后 `created_at` 倒序，然后 `id` 倒序。
- 档位是 keyset 的一部分：翻到档位 1 之后游标带上档位，所以两档交界处翻页不会漏行或重复。不带 `q` 的列表游标编码 `created_at` 与 `id`；有档位的页在前面加上档位。
- 默认 90 天窗口照常生效，与项目里所有列表一致；要找更早建档的产品，选择器显式传 `created_after`。REST 读 `created_after` / `created_before`（RFC 3339；格式不对 → `400`）。
- 不加索引。"包含"那一支无论有什么索引都要扫窗口内的行，前缀索引也服务不了和它 `OR` 在一起的查询；表是主数据量级（几千到几十万个 SKU）。将来真变大，答案是三元组索引（`pg_trgm`），不是前缀索引。

## 事件

发布，全部经 Outbox、与写操作同一个事务：

| subject | 何时 | payload |
|---|---|---|
| `mdm.product.created.v1` | `Create` | id、SKU、名称、基准单位、追踪类型、标准成本、状态、版本 |
| `mdm.product.updated.v1` | `Update`，以及 `SetStatus` 改回 `ACTIVE` | 同样的字段 |
| `mdm.product.disabled.v1` | `SetStatus` 改成 `DISABLED` | 同样的字段（契约只要求 id 与版本） |

每条 payload 都必须带 `tracking_type`：`erp/inventory` 用这些事件维护摘要副本，据此判断收货要不要批次号 / 序列号。没有 "enabled" 这个 subject：重新启用就是一次 payload 里带着状态的更新。标准成本变化也是普通更新；只有当某个消费者需要"只关心成本变化"时才另加 subject，因为 subject 一旦发布就永远删不掉。消费者只在事件的 `version` 大于本地持有的版本时才应用，因此不怕乱序与重复投递。

消费：无。消费一个事件，主数据就依赖了别人。

## 依赖

无，这是设计。看上去应该有、其实没有的：

| 不是依赖 | 为什么 |
|---|---|
| 任何业务组件 | 主数据被所有人读、自己不调任何人 |
| `erp/inventory` | 反直觉但是刻意的：本组件不知道库存。要显示库存的产品页分别问两个组件再把结果拼起来（在 BFF 或前端），后端不需要这条边 |
| `infra/iam-casdoor` | token 用 `IAM_JWKS_URL` 的 JWKS 在本地验签：是配置，不是边 |
| `infra/authz` | 权限 bundle 从 `AUTHZ_BUNDLE_URL` 轮询：是配置，不是边 |
| NATS | 经 `NATS_URL` 访问的基础资源，不是组件 |

## 在同步调用图里的位置

叶子：只有入边。`erp/sales`、`crm/opportunity` 与移动 BFF 经 gRPC 调它；它不调任何人。它是项目三个枢纽之一（被所有人读的主数据；库存流水唯一的写入者 `erp/inventory`；主要接受命令、监听事件的 `erp/finance`）。同时被 CRM 与 ERP 读，并不违反"CRM 与 ERP 之间没有同步边"：本组件两边都不属于。

## 分区与归档

`products`、`product_categories`、`uoms`、`uom_conversions` 不分区、不归档：主数据不随时间增长，又没有终态，所以永远没有"一行已经结束"的时刻。停用的产品同样永远不删、不归档：几年前的订单和库存流水还引用着它的 ID，`BatchGet` 查不到就会让这些单据显示成空白。按项目约定可以有 `mdm_product_archive` schema；没有任何东西写它，`BatchGet` 的归档回退查不到表时只返回热表的结果。`event_outbox` / `event_inbox` 按周分区，随分区滚动。

## 数据范围

无（`data_scopes: none`）。产品主数据是参照数据，销售（报价）、仓库（收发货）、财务（核算）看到的必须是同一份；不存在"只看我的产品"。所以表里没有负责人或部门列。

## 参考实现

| 项目 | 版本 | 看的模块 | 借鉴了什么 | 许可证 | 用法 |
|---|---|---|---|---|---|
| Odoo | 17.0 | `addons/product/models/product_uom.py`（`uom.uom`） | 单位类别内换算、基准单位因子为 1；拒绝跨类别换算 | LGPL-3 | 借鉴思路 |
| Odoo | 17.0 | `addons/stock/models/stock_lot.py`、`product.tracking` 字段 | 追踪策略（`none` / `lot` / `serial`）是产品的属性，不是批次的属性 | LGPL-3 | 借鉴思路 |
| Apache OFBiz | 18.12 | `applications/product/entitydef/entitymodel.xml` | `Product` / `ProductCategory` / `UomConversion`：用来核对有没有漏掉必需字段 | Apache-2.0 | 借鉴思路 |

**刻意避开的**：Odoo 的 `uom.uom` 同时用浮点存 `factor` 与 `factor_inv`；浮点加上冗余的反向值迟早不一致，误差会乘进订单金额。这里只存一个方向、用 `NUMERIC(18,6)`、反向现算。另外没有采用 ERPNext 每个物料自带一份换算表（`UOM Conversion Detail`）的做法，它让同一组换算在每个物料上重复一遍；这里换算是单位的属性。

不是槽位族：两种换算模型是同一需求的两种建模，按单位存的那种明显更好（改一处全部生效）。

## 未决问题

| 问题 | 目前的答案 |
|---|---|
| `standard_cost` 由谁维护，成本变化要不要单独的事件？ | 人经 `Update` 维护；随 `mdm.product.updated.v1` 发出。单独的 subject 等有消费者需要时再加 |
| 可配置属性（颜色、尺寸、规格变体）？ | 标准组件里不做：动态属性表让代码和工具都失去类型。真有变体需求，做成 fork 或槽位族 |
| 向上取整对所有调用方都对吗？ | 它是默认值，因为少发货是更严重的错误。如果收货场景确实需要别的舍入，就加一个显式的请求参数，默认值仍是 `CEIL` |
| 分类有表没有接口，所以 `category_id` 目前只能为空 | 还没有界面需要它；分类接口是只增的契约改动 |
| 关键字搜索要不要忽略默认 90 天窗口？ | 暂时不：项目约定对所有列表一致，选择器传 `created_after`。如果选择器用起来别扭再议（由前端工作决定） |
| 两个并发请求带同一个 `idempotency_key`：后到的那个收到错误，而不是先到者的结果 | 只落一条，这是承诺的部分；要让后到者拿到先到者的结果，需要在主键冲突时重试读取 |
