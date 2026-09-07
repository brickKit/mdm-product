# mdm-product · 产品/物料主数据

SKU、分类、计量单位换算、批次/序列号追踪策略、标准成本。

## 它能做什么
- 产品/物料建档、改档、启用/停用
- 计量单位换算（`ConvertQuantity`：买箱卖个这类跨单位场景）
- 供其他组件批量取产品摘要（`BatchGet`，防 N+1）

## 需要哪些基础资源
| 资源 | 形态 | 为什么需要 | 怎么起 |
|---|---|---|---|
| PostgreSQL 16 | **A**（brickKit 基础资源，`kind: database`） | 数据持久化，独占 schema `mdm_product` | 装配仓库根目录 `make up` |
| NATS 2.10 | **A**（`kind: mq`） | 发布 `mdm.product.*` 事件（Outbox 推送） | 同上 |

⚠️ 形态 A / B / C 的区别见设计书 §2.7.0。本组件**不需要** Traefik 与 Casdoor
就能单独跑起来——它不对 IAM 建依赖边，JWT 走本地验签（决策 87）。

## 怎么起来
（Task 6 实现完成后补：装配路径 + 单独跑的完整命令）

## 怎么用
（Task 6 后补：一条 curl + 一条 grpcurl）

## 配置项
（Task 5 写完 component.yaml 后补，平台注入的保留变量单列一段）

## 参考实现
| 项目 | 看的模块 | 借鉴了什么 | 许可证（已复核） | 用法 |
|---|---|---|---|---|
| Odoo 17.0 | `addons/product/models/product_uom.py`（`uom.uom`） | 单位类别内换算 + 基准单位 factor=1 的模型；跨类别直接拒绝这个正确判断 | LGPL-3 | 借鉴逻辑 |
| Odoo 17.0 | `addons/stock/models/stock_lot.py` + `product.tracking` 枚举 | `tracking` 挂在产品上而不是挂在批次上——追踪策略是产品的属性 | LGPL-3 | 借鉴逻辑 |
| Apache OFBiz 18.12 | `applications/product/entitydef/entitymodel.xml` | `Product`/`ProductCategory`/`UomConversion` 实体关系的完整度 | Apache-2.0 | 借鉴逻辑 |

**要避免它的什么**：Odoo 的 `uom.uom` 同时存 `factor` 与 `factor_inv` 两个 float 字段——
浮点+冗余迟早不一致，还会乘进订单金额。我们只存一个方向的因子（`NUMERIC(18,6)`），现算不存反向值。

## 边界与禁令
- 本组件**不查库存数量**——"这个产品还有多少货"归 `erp-inventory`，产品档案页要显示库存量走**展示上推**（§4.7），不在后端建同步边
- 实际成本（移动加权/FIFO 算出来的那个）归 `erp-inventory`/`erp-finance`；本组件的 `standard_cost` 只是人工维护的标准值
- `dependencies.components` **永远是空的**：`mdm` 是只读枢纽，被所有人读、自己不调任何人（§2.6）。加一条进去就是把枢纽变成了链上一环
