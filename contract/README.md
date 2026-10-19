# mdm-product 的契约

`mdm/product` 发布的契约包：系统面的 protobuf、事件载荷的 JSON Schema、用户面的 OpenAPI。被调方发布契约，放在组件仓库的 `contract/` 子目录里单独发布，不另建仓库。

依据：装配项目 [be-assembly-standard](https://github.com/brickKit/be-assembly-standard) 的协议 02（S1–S3）、协议 07（资格查询与解析）、协议 14（数量与单位）、边界卡 `mdm/product`。

## 内容

| 位置 | 是什么 |
| --- | --- |
| `proto/be/mdm/product/v3/product.proto` | `SourceService.Snapshot`、`Read`（来源方法）；`ProductService.CheckEligibility`、`Resolve`、`Convert` |
| `gen/go/be/mdm/product/v3/` | Go 代码，包 `productv3`；模块 `github.com/brickKit/mdm-product/contract/v3` |
| `python/` | Python 包 `contract-mdm-product`，模块 `be.mdm.product.v3` |
| `events/` | `mdm.product.changed.v1`、`mdm.product_category.changed.v1` 的载荷，同时是来源方法里 `data` 的格式 |
| `openapi.yaml` | 用户面；每个路由的操作写在 `x-operation` |

## 来源的义务（协议 02 的 S1–S3）

与 `mdm-currency`、`mdm-org` 的契约相同：每次变化版本加一，没有变化不产生新版本；同一事务写完整状态事件，流 `mdm.product`，4 个分区；按身份读取一个身份一个结果、顺序相同，没有的答“已清理”，一次最多 500 个；快照按身份升序分页；每次回答带部署、实现实例、纪元与各分区位置高点，取自同一个读快照。

## 资格、解析与换算（协议 07、14）

- **资格**：产品启用、该法人已开通、该用途（销售、采购）打开，才 `eligible`；否则 `reason` 是 `not_found`、`product_suspended`、`product_retired`、`not_opened`、`not_sellable`、`not_purchasable`。回答带 `stocked`（是否参与库存）与取证时刻；调用方在取证时刻加 W（30 秒）之内提交。
- **解析**：当前、已并入、墓碑、不存在（本版没有合并，所以只有当前与不存在）。
- **换算**：经产品的基本单位；产品自己的换算优先，其次是目录里同一类别的通用换算；没有时答 `NO_CONVERSION`。结果按目标单位的小数位四舍五入（远离零）。
