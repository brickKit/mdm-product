# itest/：集成测试与测试索引

产品主数据组件对着真实的 PostgreSQL 与消息系统、经它真实的接口（用户面带 be-sdk-go `sectest` 的令牌，系统面走 mTLS）的测试（L2）。`mdm/org` 是按它发布的契约写的替身。构建标签 `integration`；一条业务流程一个文件。

## 怎样跑

- `make test-integration`（`go test -count=1 -tags integration ./itest/`），约一秒。
- 环境变量：`BE_SDK_TEST_PG_ADMIN=postgres://postgres:<口令>@localhost:5432/brickkit_test_db`（口令是装配项目 `.env` 的 `POSTGRES_PASSWORD`）、`BE_SDK_TEST_NATS_URL=nats://localhost:4222`。
- 前提：装配项目里 `make up` 起了 `be-postgres` 与 `be-nats`；`be-postgres` 里有库 `brickkit_test_db`（没有就 `docker exec be-postgres createdb -U postgres brickkit_test_db`）；测试每次开始时删掉再建自己的 schema `mdm_product_it`，跑完留着；端口 28082、9092 与替身的 19094 要空着，被占时测试直接失败。
- **只有一个测试** `TestProductEndToEnd`：按下表的顺序跑各步（普通的方法调用，不是子测试），后面的步用前面留下的产品，所以一步不能单独跑。

## 文件

按运行顺序：

| 文件 | 步 | 测的流程 | 被测的模块 |
| --- | --- | --- | --- |
| `products_integration_test.go` | `catalog`、`services`、`movements` | 分类与产品，一盒装 12 个；服务不需要库存、不追踪，同样的内容不产生新版本；有流水之后追踪政策不能改、名称仍能改 | `products`、`categories` |
| `units_integration_test.go` | `conversions` | 产品自己的换算（整盒）、不相干的单位之间没有换算、目录里同一类别的换算 | `units` |
| `availability_integration_test.go` | `availability` | 按法人开通（法人不可用被拒）；资格：可卖、不可采购、没开通、产品暂停 | `products` |
| `lists_integration_test.go` | `lists` | 关键字（拼音首字母）、分页、单位目录 | `products`、`units` |
| `source_integration_test.go` | `source` | 系统面（mTLS）：资格、换算、来源读取与戳 | `source` |
| `recoverycheck_integration_test.go` | `recoveryChecks` | 整套恢复的核对：产品按版本答“还在不在”（`checksStory`） | 入口 `product/checks.go` |

共用的：

| 文件 | 内容 |
| --- | --- |
| `endtoend_integration_test.go` | `TestProductEndToEnd`（各步的顺序）；`mdm/org` 的替身 `org`；`fixture`（运行中的成员、两个法人、步与步之间传的产品）；`start`（建世界与替身、迁移并运行成员）；`client.do`、`portFree` |

## 加测试

新流程加一个文件与一步，在 `TestProductEndToEnd` 里按依赖排进顺序，在上表加一行；测试不为了通过而改。
