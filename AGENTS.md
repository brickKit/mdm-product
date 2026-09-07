# mdm-product · AI 助手导读

## 身份证

| 项 | 值 |
|---|---|
| 组件 ID | `mdm/product` |
| 仓库名 | `mdm-product` |
| 端口 | HTTP `8082` / gRPC `9092`（`registry/ports.tsv`，装配仓库根目录那份） |
| schema / role | `mdm_product` / `mdm_product_rw`（归档 schema `mdm_product_archive` 建了但不用，见设计计划 §7） |
| 语言 / 框架 | Go：Gin + `database/sql` + `pgx/v5/stdlib` + `sqlc` + `golang-migrate` |
| 合并部署时进 | 外壳一 `go-core` |
| 装配角色 | `default` |
| 设计真相源 | 装配仓库 `docs/design/mdm-product.md`——本文件与它冲突时，以那份为准，回来改这里 |

## 边界

**归我：** SKU、产品分类、基础属性、条码、计量单位与换算因子、`tracking_type`（是否启用批次/序列号追踪）、`standard_cost`（标准成本）——这些数据的唯一真相源。

**不归我：**
- 产品的**库存数量**、批次号/序列号的**实际值**：归 `erp-inventory`。本组件说的是"这个产品要不要按批次管"，不是"这批货的批次号是多少"
- 产品的**售价**、价格表、折扣：归 `erp-sales`（设计书 §5.5：定价模块在 `erp-sales` 内部）
- 实际成本（移动加权/FIFO 算出来的那个）：归 `erp-inventory`/`erp-finance`

`data_scopes: none`（设计书 §14.2.2：`mdm` 4 个组件全部 `none`）——产品主数据全员可见，不做行级过滤。

## 契约面与事件

**gRPC `mdm.product.v1.ProductService`：** `Create` / `Update` / `SetStatus`（命令）、`Get` / `List` / `BatchGet` / `GetSummary` / `ConvertQuantity`（读）。`BatchGet` 是 BFF 与 `erp-sales` 防 N+1 的唯一合法调用方式，任何时候都不许删掉它只留 `Get`。

**REST 前缀：** `/mdm/product/**`。`BatchGet`/`GetSummary` 不暴露到 REST——它们是给其他组件 gRPC 客户端用的批量读优化。`ConvertQuantity` **要**暴露到 REST——前端下单页面要实时试算换算结果（设计书 §8.4：这类联动必须走后端 DryRun 接口）。

**发布事件：** `mdm.product.created.v1` / `.updated.v1` / `.disabled.v1`，全部走 Outbox，下游按 `version` 单向递增更新摘要副本。⚠️ `tracking_type` 必须进 payload——`erp-inventory` 靠它维护"这个产品要不要收批次号"的摘要副本。

**消费事件：** 无——消费一条就是在给只读枢纽加一条依赖边。

## 依赖与「为什么不依赖某某」

`dependencies.components` 永远是空数组。

- **不依赖任何业务组件**：`mdm` 是只读枢纽，被所有人读、自己不调任何人（设计书 §2.6 三枢纽）。这不是"暂时没有依赖"，是这个组件存在的设计前提——加一条进去，枢纽就变成了链上一环。
- **不依赖 `erp-inventory`**：反直觉但重要——本组件不查库存。"这个产品还有多少货"走**展示上推**（§4.7），由 BFF/前端分别调两个域再聚合，不在后端建边。
- **不依赖 `infra-iam-casdoor`**：IAM 走 JWT 本地验签（决策 87），只需要 `iamJwksUrl` 拉公钥，不构成依赖边。
- **不依赖事件总线（NATS）作为组件**：走 `resources` 注入（`kind: mq`），它是基础资源不是组件（设计书 §2.7.0 形态 A）。

## 这个组件特有的坑

| 不许 | 症状 | 出处 |
|---|---|---|
| 给 `dependencies.components` 加任何一条（尤其是 `erp-inventory`） | 编译、启动、测试全都正常——**没有任何症状**。但 `mdm` 从只读枢纽变成了链上一环，同步图迟早成环 | §2.6、§4.2 |
| 省掉 `BatchGet` 只留 `Get` | 单测照样绿。但 `erp-sales` 建单时只能退化成循环调 `Get` 的瀑布流 | §3.8、决策 23 |
| `uom_conversions` 的换算因子用浮点存两个方向（`factor` 与 `factor_inv`） | 建表能过，正常场景测不出来——两个方向的因子在浮点下会不一致，且这个误差会乘进订单金额里 | 设计计划 §2.1 |
| `ConvertQuantity` 向下取整 | 出库场景"需要 2.3 箱"取整成 2 箱会导致防超卖失效——本组件默认 `CEIL`（向上取整），改成向下是错的方向 | 设计计划 §2.1 |
| 把 `products`/`product_categories`/`uoms`/`uom_conversions` 建成分区表 | 迁移能跑通、代码能编译——但这四张表是主数据不是交易流水，§11.2.5 的分区大表清单里没有 `mdm-*` | 设计计划 §7 |

## 改代码前的自查

1. **我是不是在给这个组件加一条 `dependencies.components`？** 停下——先回设计计划确认这条边是不是真的必要，几乎总是不必要（尤其是 `erp-inventory`，见上表第一条）。
2. **我写的这段逻辑，是不是应该属于 `erp-inventory`（库存数量/批次实际值）或 `erp-sales`（售价/定价）而不是这里？** 判据：这段逻辑改变的是"产品是什么"还是"有多少/卖多少钱"——前者归这里，后者归别处。
3. **我是不是在给这四张表加分区/归档/数据权限列？** 停下——设计计划 §7、§14.2.2 已经判定不需要，除非设计计划本身先改。
4. **这个改动会不会让 `contracts/product.proto` 出现破坏性变更（删字段、改类型、改 Tag 编号）？** 下游 `erp-sales`/`erp-inventory` 都消费这份契约，签名一旦发布只能向后兼容地追加（§3.4 铁律 3）。
