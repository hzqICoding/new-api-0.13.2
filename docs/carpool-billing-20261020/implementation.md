# wp86a 拼车模型倍数实施回执

日期：2026-10-20。分支 `feat/carpool-billing-20261020`。仅本地实施与验证；本会话未提交、未联网、未部署、未执行 Luna 迁移。发布脚本由独立工作包负责，不能把本回执当成已上线证明。

## 实现与契约

`CarpoolBillingPolicy` 继续使用未注册选项，读取 `common.OptionMap` 时只持锁复制字符串。原有启动加载、周期同步和管理员写选项负责更新 OptionMap；计费读取时按原文刷新不可变快照，不加数据库表或选项注册。

- version=1；issued_at 必须含小数秒且可解析为 RFC3339；groups 与模型名精确、区分大小写、保留空格。倍率必须是 JSON 数字且在 0.1–10。非法整包保留最后有效快照；启动没有有效快照时无额外倍率。非法原文按 SHA-256 去重 ERROR，不打印原文。
- 旧时间戳忽略；同时间戳不同内容拒绝；没有自动失效。清除规则须写更大的 issued_at 和空 model_multipliers，不能靠删除选项清除运行中的有效快照。
- `HandleGroupRatio` 先完成原覆盖/特殊用户组/普通组算法，再统一乘一次拼车倍率，包括原覆盖分支的提前返回。没有新取整点；所有原 quota 算法和舍入位置保留。
- 同一 UsingGroup + OriginModelName 的请求重试复用首次命中的冻结有效倍率。自动分组重试换 scope 时重新匹配，并同步已有表达式结算快照的 group ratio，避免按旧组结算却记录新组倍率。
- 日志新增 `carpool_ratio:number` 和 `carpool_priced_at:int64`，后者为本次 scope 选择策略时的 UTC Unix 秒，非完成或写日志时间。carpool_ratio 只在命中规则时出现；已在 policy.groups 内但当时没有模型rule的请求也记录 priced_at，证明其当时仍为×1。该时间元数据不创建倍率快照、不改变重算路径。组尚未进入策略名单时不伪造已知时点。`group_ratio` 和 `user_group_ratio` 不含拼车倍数，日志从原始倍率快照取值，不用除法还原。
- 新任务在原 private_data.billing_context JSON 内增加可选 `carpool_snapshot`，包含 group、model、ratio、priced_at、base_group_ratio、effective_group_ratio、user_group_ratio、has_special_ratio。另独立保存 `carpool_priced_at,omitempty`（默认0不补历史任务），用于只有时间证据但无rule的新任务。snapshot presence 才是计费路径分界；历史任务或没有命中规则的任务严格保留原重算。无需 schema 迁移。

## 计费路径核对

| 路径 | 实现 |
|---|---|
| 文本倍率/按次预扣 | `ModelPriceHelper` 使用叠加后的有效倍率；原 float/int 取整保持 |
| 表达式预扣/结算 | `modelPriceHelperTiered` 把有效倍率写入 BillingSnapshot；`TryTieredSettle` 继续使用冻结值 |
| 文本最终结算与工具费用 | 原 `calculateTextQuotaSummary` / `composeTieredTextQuota` 从 PriceData / BillingSnapshot 读取有效倍率，无另一次乘倍率 |
| 音频、Realtime 最终结算 | 原有效倍率参与 `calculateAudioQuota` / 表达式结算；日志独立记录原组倍率与拼车倍率 |
| Realtime 分段预扣 | 仅命中新快照时使用冻结有效组倍率；模型倍率保留原查表行为。无新快照时原 auto_group、用户特殊组、普通组查表逻辑原样 |
| MJ / Task 提交 | `ModelPriceHelperPerCall` 统一入口；MJ、任务提交日志均记录拼车字段 |
| 异步任务 token 重算 | 新快照使用提交时有效组倍率和已存模型倍率；OtherRatios 与原 int 截断不变。旧任务完全沿原分支 |
| 异步失败退款 / 差额日志 | 实际预扣退款算法不变；从持久快照补日志，不再次乘倍率 |
| adaptor 完成调整 | 当前 adaptor 默认返回0；原已计算实际额度的优先分支不再乘一次，避免重复收费 |
| 违规费用日志 | 原有效倍率仍参与费用计算，other 同样分开记录组倍率/拼车倍率 |
| `/api/pricing` | 无业务修改；整段响应字节一致测试通过 |

## 价格矩阵及联合样本

测试数据：`service/testdata/carpool-pricing-20261020.json`（原10项只读快照），`service/testdata/carpool-inputs-20261020.json`（10-20 17:14北京时间补充的启用模型、渠道映射、组和最新计价；不含凭据）。相关原定价映射一致；新版 GroupGroupRatio=null 与原 {} 均无特殊组规则，CompletionRatioMeta 用于交叉核对，原完整 CompletionRatio 用于载入原运行算法。

矩阵44模型（38启用名称 + 6 Luna 别名）×8组（4拼车 + default / Pressure test / GPT-daycard / GPT-plus/team）×5向量（短档、>272k长档、缓存读、缓存写、小金额舍入边界）×3情景：

- 空规则保持原预扣/最终额度；
- Luna 官方表达式 + 拼车×2：拼车保持迁移前额度，非拼车使用官方×1覆盖，其他模型不变；
- gpt-6.1-sol×1.5：与直接在同一原乘算位置使用1.5倍组倍率的结果比较，而非把已舍入额度再乘1.5。

结果：**5280条比较符合，5160条数值比较相等，120条同样拒绝，差异0**。每条数值包含预扣与最终额度。

拒绝项是启用名单中的 `gpt-5-openai-compact`：最新渠道49的映射为空，ModelPrice / ModelRatio / 表达式和 `*-openai-compact` 通配都未配置；现 helper 按原逻辑拒绝未配置价格。该项未伪造价格，不属于本次改价。完整支持的数值名称是37个启用名称 + 6别名。

`settlement-fixtures.json` 是测试执行 `ModelPriceHelper → PreConsumeBilling → PostTextConsumeQuota` 后读取 SQLite consume log 得到的12条样本，并核对实际钱包扣减。涵盖表达式、倍率、按次、特殊组、非拼车、Luna迁移前后、跨策略冻结，以及无rule请求执行期间新增×2规则的长请求。RelayInfo 使用普通 OpenAI 路由的 `InitRequestConversionChain` 初始化，不手工补写输出日志字段。首批11条由主代理以 CP 实际 `RecalculateReviewLog` 复算，11/11精确相等；新增第12条后的联合结果见 CP `verification/carpool-billing-20261020/joint-settlement.json`。

## 验证

- 策略合法/非法/启动非法/回退/等时间戳修改/重复错误/并发读取测试通过。
- 精确匹配、覆盖与特殊倍率分支、同scope冻结及换组快照一致性测试通过。
- 真实钱包预扣/结算、任务JSON持久化后补差额、Realtime两段实际钱包和Token预扣、日志字段及 pricing 响应不变测试通过。
- 3处变异（不乘、覆盖提前漏乘、前缀匹配）逐一使对应测试变红，随后恢复原文件字节；变异未保留。
- `go build ./...`、涉及包 `go vet` 通过，均 `GOPROXY=off`。
- 完整相关包回归中发现既有测试问题：`service/channel_affinity_usage_cache_test.go` 用 Windows 同 tick 的 UnixNano 当不同测试 cache key，单独原测试也稳定碰撞；仅改 ruleName 为 `t.Name()`，保留全部断言后 service 完整包通过。
- `relay/helper` 完整包发现既有 `TestStreamScannerHandler_StreamStatus_PreInitialized` 失败：生产源码和HEAD无条件新建 StreamStatus，而旧测试要求保留之前错误计数。经主代理确认，只把测试改为 Reinitialized，断言新旧对象不同、新错误数0、旧错误数仍1、结束Done，不改流业务；完整包复跑40.222秒通过。
- 最终涉及包 `setting/billing_setting`、`relay/helper`、`service`、`controller`、`model`、`relay/common`、`pkg/billingexpr` 完整测试通过；`types`、`relay` 编译通过且无测试。最后新增加的生产 CompletionRatioMeta 交叉核对后，service 的全部 TestCarpool 再跑通过。`gofmt -l` 无输出，`git diff --check` 通过。
- 无rule的时间证据补齐后，setting/helper/service/controller 的全部 TestCarpool 再跑通过；包含 timestamp-only 任务序列化后继续旧算法的实际差额断言，未给历史任务补时间或新快照。

## 源码增量（供生产基线逐项覆盖）

业务修改：`controller/relay.go`、`model/task.go`、`relay/common/relay_info.go`、`relay/helper/price.go`、`relay/mjproxy_handler.go`、`service/log_info_generate.go`、`service/quota.go`、`service/task_billing.go`、`service/violation_fee.go`、`types/price_data.go`。

新增业务：`setting/billing_setting/carpool_policy.go`。

新增测试：`controller/carpool_pricing_test.go`、`relay/helper/carpool_billing_test.go`、`service/carpool_billing_test.go`、`setting/billing_setting/carpool_policy_test.go`；修改既有测试：`service/channel_affinity_usage_cache_test.go`、`relay/helper/stream_scanner_test.go`。合计11个业务Go文件、6个测试Go文件。另有上述2份 testdata、本文和真实结算样本。

`docs/codex-sessions.md` 为任务开始前已有登记变更，非本轮计费实现；旧 `verification/usageaggregate/main.go` 不属于本次生产增量。内嵌前端、运行配置、选项写入和 Luna 迁移均不在本实施会话操作范围。

## 最终Go增量冻结SHA-256

清单含17个Go文件；集合摘要为路径→SHA映射的UTF-8紧凑排序JSON之SHA-256：`1a1a539e2f47fa6ca6e6be7652c17d288cd0776388b6315f12b61841f26ba747`。

| 文件 | SHA-256 |
|---|---|
| `controller/carpool_pricing_test.go` | `b902b865496bf87b1c82bcd5dd50b07d3c1e521e1703602361de9af222fe2c57` |
| `controller/relay.go` | `43375745bd2fe6d53d5a480bd6c6853bfdc8d763121ee32667fcd900c9a82f02` |
| `model/task.go` | `9f85fac11e65e17da91786650799db417db2acfb1912417857439a809ec57b34` |
| `relay/common/relay_info.go` | `857ae41be2fcf138c844664571836e0fbc45412e18e87335a43d11bda6d494cd` |
| `relay/helper/carpool_billing_test.go` | `b2bd832b94c122aace1d1410673e514c2a96b2cb4aba8dd090c36cb6235c2809` |
| `relay/helper/price.go` | `a2a3449dc32abcb91dc5434e661fbd1296d2d489caac30f337bbe39eab00823e` |
| `relay/helper/stream_scanner_test.go` | `eb05121dd21404493b12e22afcdee510432f23c2a9cb298b43310bb31b011628` |
| `relay/mjproxy_handler.go` | `bb8bea00fdcb33a64de07d5c06468947b0358d37dfda66458a6f01389c2e7b23` |
| `service/carpool_billing_test.go` | `42430e34ee7cf961819f23f40b8b74efea2f098ddafe5cac5341a70bfb2e29c1` |
| `service/channel_affinity_usage_cache_test.go` | `28871ed4e0652c423da30f3cbc0cb47191dbde36e1e1590caca3ad867d0d8c7a` |
| `service/log_info_generate.go` | `33f64b8df043bd3f144564a3b3c0e117c28cf5e0194d60598ff9eb2b4e31a8fa` |
| `service/quota.go` | `13e8af0e20b74e4410d357bd55b5890747b9be0f8c2cf2f54522678c55caf7ac` |
| `service/task_billing.go` | `cad2c269d075270be3df888e4b0f418db5f9c6b0d1bdc26a3cbde3127f0b0e1d` |
| `service/violation_fee.go` | `231191fa19e1153b0a4188549ad59731080002b8ba80a4f42405007bff4f5d2d` |
| `setting/billing_setting/carpool_policy.go` | `840e15ecc00b937a86e41ee85d00230543e9602f892d6424f0d180df1b38585e` |
| `setting/billing_setting/carpool_policy_test.go` | `031688715f1f0517fe58f46228d085a828cf51f3cb99f3f4cf99ca98313d7042` |
| `types/price_data.go` | `90f27253017c0f4a38bc6fda9d4dc137e13f096b9a4d1769107aabecb924405e` |
