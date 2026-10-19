# mdm-product

组件 `mdm/product`：产品、分类、单位与换算、按法人开通。使用说明见 `BRICKKIT.md`，修改说明见 `AGENTS.md`。

设计依据：装配项目 [be-assembly-standard](https://github.com/brickKit/be-assembly-standard) 的边界卡 `docs/zh/07-components/mdm.md`（`mdm/product` 一节）。旧代码在 `legacy/v2` 分支，按终局设计从零开始（D59），版本从 `3.0.0` 起（A110）。

## 在项目里使用

在装配项目里 `brickkit add mdm/product@<版本>`，再按 `BRICKKIT.md` 的“部署前准备”配置。种子数据：`make seed`（见 `cmd/seed`）。

## 文档

| 问题 | 看哪里 |
| --- | --- |
| 它做什么、怎样部署与配置 | `BRICKKIT.md` |
| 怎样改它 | `AGENTS.md` |
| 契约与来源的义务 | `contract/README.md` |

## 开发

见 `AGENTS.md` 的“构建与测试”。
