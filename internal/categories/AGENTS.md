# categories：分类

产品的分类：代码、名称与译名。

## 负责与不负责

- 负责：表 `category`；路由 `/api/product/categories` 的列表、新建、修改；事件 `mdm.product_category.changed.v1`。
- 不负责：产品归哪个分类（`products`）。

## 文件

| 文件 | 内容 |
| --- | --- |
| `categories.go` | `categoryIn`、`CreateCategory`、`UpdateCategory`；列表 `categoryList`、`ListCategories` |

## 依赖与被依赖

- 依赖：`core`。
- 被依赖：入口登记路由。

## 规则在哪

`docs/design.md`。

## 测试

`itest/products_integration_test.go`（`catalog` 建一个分类）。
