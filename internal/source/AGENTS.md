# source：系统面

给部署里其他组件的读取（装配项目协议 02 的来源方法）、协议 07 的资格与解析、数量换算；任何组件都可调。

## 负责与不负责

- 负责：`SourceService.Read`、`Snapshot`（产品与分类，带戳）；`ProductService.CheckEligibility`、`Resolve`、`Convert`；一次最多 `core.MaxBatch` 个。
- 不负责：资格与换算的规则（`products`、`units`）。

## 文件

| 文件 | 内容 |
| --- | --- |
| `source.go` | `aggregates`、`stamp`、`readByID`、`Snapshot`、`Read`、`purposes`、`CheckEligibility`、`Resolve`、`Convert` |

## 依赖与被依赖

- 依赖：`core`、`products`（`EligibilityOf`）、`units`（`Convert`）。
- 被依赖：入口 `product/services.go`。

## 规则在哪

装配项目协议 02（来源要提供什么、戳）、协议 07、决定 0304（批量的上限）。

## 测试

`itest/source_integration_test.go`（`source`）。
