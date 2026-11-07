# products：产品

产品本身：代码、名称、种类（库存、服务、非库存）、基本单位、追踪政策（批次、序列号）、产品自己的换算、按法人的开通；以及资格：新单据此刻能不能以某种用途引用这个产品（装配项目协议 07）。

## 负责与不负责

- 负责：表 `product`、`product_conversion`、`product_availability`；路由 `/api/product/products` 的列表、新建、读取、修改、状态、按法人开通，与 `/api/product/eligibility`；事件 `mdm.product.changed.v1`；有流水之后追踪政策不再改（`has_movements`）；开通前在事务外问 `mdm/org` 法人能不能用（`legalEntityActive`）；搜索列。
- 不负责：换算的算法（`units`）；分类（`categories`）；给其他组件的读取（`source`）。

## 文件

| 文件 | 内容 |
| --- | --- |
| `products.go` | `productIn` 与 `apply`（追踪政策、基本单位、换算的规则）、`searchOf`、`write`；`CreateProduct`、`product`、`GetProduct`、`changeProduct`、`UpdateProduct`、`ProductState`；`legalEntityActive`、`PutAvailability`；列表 `productList`、`ListProducts`；`Eligibility`、`EligibilityOf`、`EligibilityHTTP` |

## 依赖与被依赖

- 依赖：`core`、`units`（`UnitOf`）。
- 被依赖：`source`（`EligibilityOf`）；入口登记路由。

## 规则在哪

`docs/design.md` 的状态与规则、未决问题；装配项目协议 07（资格）、协议 14（单位）。

## 测试

`itest/products_integration_test.go`（`catalog`、`services`、`movements`）、`itest/availability_integration_test.go`（`availability`）、`itest/lists_integration_test.go`（`lists`）。
