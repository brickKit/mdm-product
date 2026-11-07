# mdm/product

产品主数据组件的代码。用法与边界在 `BRICKKIT.md`，为什么这样设计（术语、数据、状态与规则、未决问题）在 [docs/design.md](docs/design.md)；跨组件的规则在装配项目 be-assembly-standard 的协议 02（来源）、协议 07（资格与解析）、协议 14（金额、数量与单位、列表）。

## 代码地图

代码按业务模块分层。**先读这一节，再进目录**：每个代码目录都有自己的 `AGENTS.md`（同目录的 `CLAUDE.md` 让 Claude Code 读这个目录的文件时自动带上它），写这一层负责什么、不负责什么、每个文件、依赖谁、测在哪。

| 目录 | 是什么 |
| --- | --- |
| [`product/`](product/AGENTS.md) | 入口：组件定义与组装（路由、系统面、恢复核对都在这里登记，转给业务模块）；不写业务。`main.go` 与外壳只引用它 |
| [`internal/`](internal/AGENTS.md) | 业务：下面的模块各一个包；模块之间的依赖表在它的 `AGENTS.md` |
| [`itest/`](itest/AGENTS.md) | 集成测试，按业务流程分文件；它的 `AGENTS.md` 是测试索引 |
| `product/migrations/` | 表与单位目录 |
| `cmd/seed/` | 种子：角色与授予、分类、产品与包装层级、各法人的开通（`make seed`） |
| `contract/` | 本组件发布的契约，单独的模块：`contract/proto/`、`contract/events/`、`contract/openapi.yaml` |
| `docs/design.md` | 设计：术语、表、状态与规则、命令、事件、实现状态、未决问题 |

业务模块（`internal/` 下）：

| 模块 | 负责 | 拥有的表 |
| --- | --- | --- |
| [`products`](internal/products/AGENTS.md) | 产品：新建、修改（追踪政策与基本单位的规则）、状态、按法人开通、列表；资格 | `product`、`product_conversion`、`product_availability` |
| [`categories`](internal/categories/AGENTS.md) | 分类 | `category` |
| [`units`](internal/units/AGENTS.md) | 单位目录与换算 | `unit` |
| [`source`](internal/source/AGENTS.md) | 系统面：来源方法、资格、解析、换算 | — |

共用的：[`core`](internal/core/AGENTS.md)（名字与资源定义、校验、十进制辅助、审计与事件的写法、记录的类型与读取、本版不做的路由）。

| 想改 | 从这里开始 | 然后 |
| --- | --- | --- |
| 换算的规则 | `internal/units` 的 `Convert` | `itest/units_integration_test.go` |
| 有流水之后的锁定 | `internal/products` 的 `productIn.apply` | 第 7 步：订阅库存的流水事件设置 `has_movements`；`itest/products_integration_test.go` 的 `movements` |
| 加一个单位 | `migrations` 的单位目录（新迁移） | 协议 14 的单位规则 |
| 资格的规则 | `internal/products` 的 `EligibilityOf` | `itest/availability_integration_test.go` |
| 加字段、加接口、改事件 | 装配项目 be-assembly-standard 规范里的“改动配方” | `docs/design.md` 的数据与命令 |

## 构建与测试

- `go test ./...`（`make test`）：清单的 `configSchema` 与 `events`、OpenAPI 与路由、事件载荷、合并安全。
- `make test-integration`（`go test -tags integration ./itest/`）：真实的 PostgreSQL 与消息系统；每个文件测哪条流程、要什么前提，在 [itest/AGENTS.md](itest/AGENTS.md)。
- `brickkit lint`

## 设计取舍

- **换算与开通是产品状态的一部分**：一个事件就是产品的完整状态，消费者（销售、库存）投影一份就够了。
- **换算经基本单位**：任何两个单位都先换成基本单位再换出去，产品自己的换算优先；包装单位只能来自产品。结果按目标单位的小数位舍入，金额不在这里算。
- **服务与非库存物品不追踪批次或序列号**：数据库的约束与接口的校验都拒绝，它们没有流水可以追踪。

## 易错点

- 不要用浮点做换算：系数与数量都是十进制（协议 14），`apd` 的精度远超系数的 12 位小数。
- 产品连同子表一起读时不能直接 `FOR UPDATE`（聚合）：先锁行，再读（`core.GetProduct`）。
- 比较“有没有变化”按 JSON：复制切片用 `slices.Clone`，空列表要保持为空列表；复制成 `nil` 会编码成 `null`，同样的内容也被当成变化、多出一个版本（3.0.2 修过）。

## 改代码前自查

- 改了契约（`contract/`）：proto 改后 `make gen`，发布 `contract/v…`，再改这里的依赖；OpenAPI 与路由由测试核对。
- 改了版本号吗？每次改动都升版本。

<!-- brickkit:managed:begin lang=zh -->
<!-- 由 brickkit 维护（init、add、remove、upgrade、skills update）：这对标记之间的改动会被覆盖 -->

## BrickKit

这是一个 BrickKit 组件：平台只读 `component.yaml`。它依赖的规则：

- `configSchema` 的键就是代码读的环境变量名。不能用保留名：`COMPONENT_ID`、`COMPONENT_VERSION`、`PORT`、`BRICKKIT_SERVED_MEMBERS`、`BRICKKIT_SERVED_MEMBERS_CONFIG`，以及任何 `*_ENDPOINT`。
- 依赖写精确版本。依赖的地址以 `<ID>_ENDPOINT` 注入；缺席的可选依赖根本没有这个变量，读的时候要带兜底。
- `/healthz` 只查本进程，不查依赖。迁移命令用同一个镜像跑，遇到不认识的参数必须直接失败；`local.migrationCommand` 从源码跑同一条迁移，给 `mode: local` 用。
- `BRICKKIT.md` 会随版本进入每个使用它的项目，在那里是脱离仓库单独读的：跟代码一起改，不放相对链接。
- 发版：改 `metadata.version`，提交、推送，`brickkit release`。`brickkit lint` 会检查清单和这些文档——在项目里、在这个目录下跑，只查这个组件（`--all` 查整个项目）。
- 完整规则在 `brickkit-component` 技能里（装了技能的项目或仓库根目录下的 `.claude/skills/brickkit-component/SKILL.md`；`brickkit skills update` 会装上）；参数问 `brickkit <命令> --help`；BrickKit 自己的文档用 `brickkit docs` 看。
<!-- brickkit:managed:end -->
