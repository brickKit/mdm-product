# units：单位与换算

单位目录（随迁移提供，UN/ECE 代码）与数量的换算：经产品的基本单位，产品自己的换算优先，目录里同一类别的换算其次（装配项目协议 14）；结果按目标单位的小数位舍入。

## 负责与不负责

- 负责：表 `unit`（只读，迁移写入）；路由 `/api/product/units`、`/api/product/products/:id/convert`；`Convert`（系统面也用它）。
- 不负责：产品的换算是怎样录入的（`products`）；金额（不在这里算）。

## 文件

| 文件 | 内容 |
| --- | --- |
| `units.go` | `Unit`、`UnitOf`、`ListUnits`；`arith`（换算的精度，远超系数的 12 位小数）；`Converted`、`Convert`、`ConvertHTTP` |

## 依赖与被依赖

- 依赖：`core`。
- 被依赖：`products`（`UnitOf`）、`source`（`Convert`）；入口登记路由。

## 规则在哪

`docs/design.md` 的换算；装配项目协议 14（数量与单位）。不用浮点：系数与数量都是十进制（`apd`）。

## 测试

`itest/units_integration_test.go`（`conversions`）；系统面的换算在 `itest/source_integration_test.go`。
