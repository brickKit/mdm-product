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

```bash
# 装配仓库根目录
make up                        # 起 PostgreSQL/NATS 等默认基础资源
cd components/mdm/product
go build -o build/migrate ./backend/cmd/migrate
PG_SCHEMA=mdm_product DATABASE_HOST=localhost DATABASE_PORT=5432 \
  DATABASE_USER=postgres DATABASE_PASSWORD=<.env 里的 POSTGRES_PASSWORD> DATABASE_NAME=brickkit_db \
  ./build/migrate up
go run ./backend/cmd/server     # 单独跑：besdk.RunStandalone 读 component.yaml 的端口
```

或者用平台：`brickkit up`（装配仓库根目录，`components/mdm/product` 登记为 submodule 且在 `brickkit.yaml` 里之后）。

## 怎么用

```bash
# 建一个产品（gRPC）——sku 留空则按 "P" + 6 位自增数字生成
grpcurl -plaintext -d '{
  "idempotency_key": "prod-demo-1",
  "name": "示例产品",
  "category_id": "1",
  "base_uom_id": "EA",
  "tracking_type": "TRACKING_TYPE_NONE",
  "standard_cost": "10.00"
}' localhost:9092 mdm.product.v1.ProductService/Create

# 查一个产品（REST，人类操作）
curl -H 'Authorization: Bearer <应用 token>' 'http://localhost:8082/mdm/product/products/1'

# 换算数量（前端下单页试算用，DryRun 纯函数不落库）
curl -X POST -H 'Authorization: Bearer <应用 token>' -H 'Content-Type: application/json' \
  -d '{"product_id":"1","qty":"2.3","from_uom_id":"EA","to_uom_id":"BOX"}' \
  http://localhost:8082/mdm/product/products/convert-quantity
```

## 配置项

| 配置键 | 默认值 | 说明 |
|---|---|---|
| `pgSchema` | `mdm_product` | 本组件的 PG schema |
| `otelBaseUrl` | `""` | 空 = Blackhole Exporter，零成本 |
| `iamJwksUrl` | `""` | JWT 本地验签的公钥来源，指向 `infra-iam-casdoor` |
| `authzBundleUrl` | `""` | 权限判定的 bundle 轮询地址，指向 `infra-authz` |

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
