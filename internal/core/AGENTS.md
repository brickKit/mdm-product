# core：全组件共用的名字与写法（共用）

每个模块都引用、自己什么业务也不做的东西。改这里会影响所有模块。

## 负责与不负责

- 负责：组件 ID、依赖 `mdm/org`、流与事件类型、资源与操作、`MaxBatch`、`W`、资源定义 `Definitions()`（`names.go`）；本组件的错误原因、代码与身份的格式、授权检查、校验、审计（`Change`）、内容比较（`Same`）、译名的检查、十进制辅助（`Decimal`、`Canonical`）、开通的排序、本版不做的路由（`Unsupported`）（`rules.go`）；记录的类型、读取（产品连同换算与开通）、发件表（`Emit`）（`records.go`）；`Base`（`base.go`）。
- 不负责：任何业务规则（在各模块）。

## 文件

| 文件 | 内容 |
| --- | --- |
| `base.go` | `Base`：运行时、部署、实现实例 |
| `names.go` | 常量；`Definitions()` |
| `rules.go` | 错误原因、格式、`Unsupported`、`Check`、`Invalid`、`NotFound`、`Change`、`Same`、`CheckNames`、`Decimal`、`Canonical`、`SortAvailability` |
| `records.go` | `Product`、`Conversion`、`Availability`、`Category` 与 `Scan…`、`Get…`（`GetProduct` 先锁行，再读子表）；`Emit` |

## 依赖与被依赖

- 依赖：只有 SDK 与契约。
- 被依赖：全部模块与入口。

## 规则在哪

`docs/design.md`；装配项目协议 02、07、14。

## 测试

由 `itest/` 的流程测试间接覆盖。
