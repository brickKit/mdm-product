# mdm/product

## 组件定位

产品主数据。有销售、采购、库存或制造业务的客户都需要。
- **产品**：代码（唯一，永不复用）、种类（库存商品、服务、非库存物品）、名称与译名、分类、基本单位、追踪政策（不追踪、批次、序列号）。只有库存商品参与库存（`stocked`）：服务的订单不请求库存。
- **单位**：单位目录随组件提供（UN/ECE 第 20 号建议的代码），按类别（数量、质量、长度、面积、体积、时间、包装）带对类别基本单位的换算与数量的小数位。产品可以有自己的换算（一箱等于多少个），优先于目录的通用换算；包装单位只有产品自己的换算。换算结果按目标单位的小数位舍入。
- **按法人开通**：可售、可采购，各自开关。开通时同步问 `mdm/org` 法人是否启用（不可达不等于不存在，协议 07）。
- **追踪政策与基本单位**：库存有了该产品的流水之后不再改变（改用新产品并迁移）。“已有流水”由库存组件的事件设置（实验第 7 步接上），本组件按这个标记拒绝。
- **资格查询**：新单据能否在某法人以某用途（销售、采购）引用某产品；回答带是否参与库存与取证时刻（W 为 30 秒）。
- **批量解析**与**单位换算**也在系统面提供。

标准成本不在这里（`erp/costing`）。实验第 6 步的范围：条码与图片在契约里，调用答“能力不支持”（`CAPABILITY_UNSUPPORTED`，D75）。

## 部署前准备

- 数据库：PostgreSQL 16 及以上；schema `mdm_product`，属主角色 `mdm_product_owner`（只供迁移）与运行角色 `mdm_product_rw`（只有读写数据的权限），名字来自装配项目的注册表。三者由部署方在第一次启动前建好（be-assembly-standard 的 `make db-init` 按装配进项目的组件配置生成并执行）；表由本组件的迁移建，迁移里不建角色、不授权。迁移写入单位目录。
- 消息系统（`BUS_URL`）与实现实例（`IMPLEMENTATION_INSTANCE`）：每次变化以完整状态事件发布到流 `mdm.product`（4 个分区）。
- 部署的服务证书（`SERVICE_TLS_*`）；身份与授权（`IAM_*`、`AUTHZ_GRPC_URL`、`DEPLOYMENT_ID`）；审计（`AUDIT_GRPC_URL`）。
- 依赖 `mdm/org`：经它的系统面确认法人。

## 依赖说明

- 必需：数据库、消息系统、身份成员、授权成员、`mdm/org`。
- 可选：审计组件；附件组件（图片，本版没有）。
- `mdm/org` 不可达时：已有数据照常读写；开通可售、可采购被拒，直到它回来（关闭不需要确认法人）。

## 配置指南

全部键见 `component.yaml` 的 `configSchema`（由 SDK 生成，测试核对）。

| 键 | 说明 |
| --- | --- |
| `DEPLOYMENT_ID`、`IAM_ISSUER`、`IAM_JWKS_URL`、`AUTHZ_GRPC_URL`、`SERVICE_TLS_CERT_FILE`、`SERVICE_TLS_KEY_FILE`、`SERVICE_TLS_CA_FILE`（必填） | 受保护的用户面与 mTLS 的系统面要用；部署身份、身份与授权的地址在外壳里是进程级的键 |
| `PG_HOST`、`PG_DATABASE`、`PG_USER`、`PG_PASSWORD`、`PG_SCHEMA`（必填） | 自己的数据库，密码是密钥；迁移另用 `PG_OWNER_USER`、`PG_OWNER_PASSWORD` |
| `BUS_URL`（必填，密钥）、`IMPLEMENTATION_INSTANCE`（必填） | 消息系统；实现实例是安装时生成的随机身份，事件与来源方法的回答都带它（协议 02） |
| `AUDIT_GRPC_URL` | 审计组件的系统面；缺时审计记录留在本地，按协议 03 处理 |

mdm/org 的地址由 BrickKit 按依赖注入（`MDM_ORG_GRPC_ENDPOINT`），不是配置键。

## 契约索引

- **系统面**（本仓库 `contract/proto`，模块 `github.com/brickKit/mdm-product/contract/v3`，标签 `contract/v3.…`）：
  - `be.mdm.product.v3.SourceService/Snapshot`、`Read`：协议 02 的来源方法，聚合是产品与分类；一次最多 500 个。
  - `be.mdm.product.v3.ProductService/CheckEligibility`、`Resolve`、`Convert`。
- **用户面**：`contract/openapi.yaml`。操作 `mdm.product.read`、`mdm.product.edit`、`mdm.product_category.manage`。
- **事件**（流 `mdm.product`，schema 在 `contract/events/`）：`mdm.product.changed.v1`（含产品的换算与按法人的开通）、`mdm.product_category.changed.v1`，都是完整状态。

## 外壳声明

不是外壳。可以被 Go 的基础外壳（`be/go-infra`）承载。
