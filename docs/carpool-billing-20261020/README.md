# r15.18 拼车模型倍数：发布与维护

## 变更记录

| 日期 | 版本 | 变更说明 |
|---|---|---|
| 2026-10-20 | async-rollback-guard | 增加带 carpool_snapshot 的未完成异步任务回滚保护；非法/未知上下文或预算超限保守拒绝，不输出任务私有内容。累计 61 项离线脚本检查通过。 |
| 2026-10-20 | source-delta-pinned | 主代理确认并固定 5 新增 / 12 修改 Go 文件白名单；51 项离线脚本守卫检查通过。候选构建及实际发布另验，不代表已上线。 |
| 2026-10-20 | release-preparation | 新增独立软件发布脚本及回滚守卫；基线为当前启用展示聚合的 r15.17。本文不代表已上线，业务实现、矩阵测试和实际发布由主代理分别登记。 |

## 范围

版本 `v0.13.2-carpool-r15.18-billing-20261020`。拼车使用的最终分组倍率再乘 `CarpoolBillingPolicy` 中精确匹配的模型倍数；原 new-api / QuantumNous 身份和前端保持。行为契约见拼车仓库的方案 86 与复核记录 87。

本目录的 `deploy.py` **只切换 new-api 软件**，不写策略、价格选项、分组或业务数据，不执行 Luna 迁移，不调用模型。现有 `CARPOOL_USAGE_AGGREGATE_ENABLED=true`、容器环境、挂载和网络保持。内嵌 `default`、`classic` 两主题共 956 个资源必须与当前 r15.17 全量一致。

策略示意（发布脚本不提交此内容）：

```json
{"version":1,"issued_at":"2026-10-20T08:00:00.123456Z","groups":["carpool-example"],"model_multipliers":{"gpt-6-luna":2}}
```

分组和模型精确匹配；倍数范围 0.1–10。`issued_at` 必须单调推进；非法/回退的策略不能当作有效关闭，因为运行进程可能保留上一份有效快照。原 `group_ratio`、`user_group_ratio` 与日志新增 `carpool_ratio` 分开记录。异步任务、Realtime 及结算路径的实际实现和验证，以实现代理交付及主代理整合核价结果为准，本发布文档不提前宣称通过。

## 已固定的软件基线

| 项 | 值 |
|---|---|
| 旧版本 | `v0.13.2-carpool-r15.17-usage-aggregate-20261019` |
| 旧容器 ID | `bd726f2b1f6e5d336a4391671b1b00336663bf88afa5220e60dfa41180f2ce63` |
| 旧镜像 | `sha256:82ffa348e9abe6f352253a56eed6737afb2e480a5e83ae78b9580526ca0af28b` |
| 旧程序 SHA-256 | `756970f5ee85edef29eaada69bad653303e1443cb1029b5d0e4b3d2277764e3d` |
| 旧启动时间 | `2026-10-19T12:31:59.324728811Z` |
| 当前源码/资源归档 | `/opt/carpool/releases/newapi-usage-aggregate-20261019/` |
| 本次发布目录 | `/opt/carpool/releases/newapi-carpool-billing-20261020/` |
| 新镜像 tag | `hzqicoding/new-api:carpool-r15.18-billing-20261020` |

以上容器事实由主代理提供；本发布准备代理没有 SSH 核实。脚本执行时逐项再核对，不能仅凭旧文档放行。CP 当前方案 85 容器在 prepare 动态捕获，绝不使用历史 A2 容器 ID。

## 软件发布流程

输入及守卫细节见 [release-preparation.md](release-preparation.md)。脚本已按主代理确认固定 5 个新增、12 个修改 Go 文件的白名单。主代理完成业务实现、价格矩阵及 Linux 制品后上传制品和 manifest；候选源码清单必须严格匹配白名单，不能自动放行额外变更。

```bash
python3 /opt/carpool/releases/newapi-carpool-billing-20261020/deploy.py preflight
python3 /opt/carpool/releases/newapi-carpool-billing-20261020/deploy.py prepare
python3 /opt/carpool/releases/newapi-carpool-billing-20261020/deploy.py cutover
python3 /opt/carpool/releases/newapi-carpool-billing-20261020/deploy.py verify
```

`prepare` 不停生产，只保存受限快照、核对候选内嵌前端并从精确旧镜像构建仅替换程序的镜像；构建不联网、不拉取基础镜像。`cutover` 重新核实所有权与配置后，保留旧容器并启动候选，立即完成核验。

必须先完成 new-api 本次 `verify`，再部署 CP。核验包括原 token-scoped 日志接口、聚合空范围 200/匿名 401、版本/程序哈希、CP health、前端、选项、schema/index 和其他运行容器。没有真实中继/扣费请求，也不把这些探测写成浏览器或完整生产核价。

### `/api/pricing` 的核验口径

原 `model/pricing.go` 遍历 Go map 生成模型和供应商数组，并把固定 `pricing_version` 缓存标记放到随机第一条模型。跨进程重启不能保证原始字节顺序相同。

因此保存前后原始响应 SHA-256，并严格比较规范化后的完整内容：仅按 `model_name` 排模型、按 `id` 排供应商、排序 `enable_groups` 和 `supported_endpoint_types` 集合；重复模型/供应商 ID 直接拒绝，集合重复元素的数量保留。模型行版本标记必须恰好一个且值相同，只忽略它落在哪个模型行。**所有价格、倍率、表达式、元数据和其他字段均保留比较**；`auto_groups` 顺序也保留。源码不因此修改。同进程业务测试仍可逐字节比较。

### 选项与其他服务

除已有定时更新的 `CarpoolConcurrencyPolicy` 外，全部 `options` 必须逐键同 SHA，包括 `CarpoolBillingPolicy`、Luna 表达式和覆盖值。动态并发策略另存前后摘要，不将它的变化伪报为未变化。发布不会写任何选项。

其他运行容器的 ID、镜像、启动时间、重启次数以及 Env/配置、HostConfig、挂载和网络摘要都必须保持。因此不要在本次 prepare 到 verify 之间并行发布 CP、重启其他服务或启停验证容器。

## 回滚与维护保护

```bash
python3 /opt/carpool/releases/newapi-carpool-billing-20261020/deploy.py rollback
```

旧容器保留为 `new-api-rollback-newapi-carpool-billing-20261020`。只能删除同时匹配本次新容器 ID、镜像、release 标签、服务标签和原容器 ID 标签的候选。同镜像的后续容器不属于本次发布；`verify` 失败**不会自动回滚**。只有正在执行的 `cutover` 异常会尝试带同样守卫的恢复；恢复不允许或不成功时保留证据与容器。

回退 r15.17 前，脚本只读检查并拒绝以下状态：策略不合法/不明确、非空模型倍数、CP 已迁移 Luna、存在未完成倍率操作、CP 本地仍持有非 ×1 规则。另检查状态不是 `SUCCESS` / `FAILURE` 的异步任务：只要 `private_data.billing_context.carpool_snapshot` 字段存在，就必须等待 r15.18 完成结算，不能因当前策略已清空而降级。未知/坏 JSON、缺失 tasks 表或检查预算超限均拒绝。没有 `--force`。

如遇拒绝，应先进入维护流程：

1. 停止/隔离 CP 的倍率写入者和相关定时同步，避免人工关闭后被自动写回；不要在旧版部署脚本里跳过所有权检查。
2. 若 Luna 已迁移，使用完整迁移备份按方案 86/87 撤回 **Luna 表达式、计费模式/覆盖值、策略、规则及迁移标记**，先完成表达式读回，再撤销加价；只恢复目标模型，保护无关模型。不能只清策略，否则可能少收。
3. 如果只是非 Luna 的紧急关闭，先确认取消倍数的业务决定及历史影响，再通过受控维护入口同步清除 CP 规则并发布 `issued_at` 大于已采用值的无倍数策略，读回核对。无效 JSON、空字符串或旧时间戳不构成可靠关闭。
4. 复核未完成操作、迁移标记和本地规则已经清理/恢复；等待带冻结计费快照的异步任务由 r15.18 完成结算。不要删除快照字段或直接改任务状态来绕过守卫。确认后重新运行本脚本 `rollback`。它不恢复数据库备份，不替你执行上述业务写入。
5. 保留原始策略/备份/相关日志，用 R2/R5 和人工抽样核价；任何多收/退款由需求方决定。切换进程不承诺在途请求零中断。

本脚本的整轮 invariants 只对本次软件切换期间成立。后续已经授权的 CP 发布或计价迁移会使旧 `verify` 的基线检查失败，这是诊断结果，不是允许恢复旧生产容器的依据。

## 主代理联合核价与发布守卫补充

2026-10-20：CP实际RecalculateReviewLog对12条真实new-api预扣/结算日志逐条精确一致。包括当时无rule、途中新增×2的长请求，时间戳证明原×1，不补carpool_ratio或改变旧任务计费。初次fixture漏普通路由InitRequestConversionChain导致9条语义不足被CP拒绝；已修fixture正常初始化并重新由真实结算生成，不人工补写输出日志。

跨进程/api/pricing可能因Go map顺序而改变字节，逐项验证按模型/厂商标识排序、集合排序，保留重复数并拒绝重复标识。model/pricing.go将固定row pricing_version放到随机第一条，controller/pricing.go又按可用分组过滤，故公开响应可见该marker为0或1个；主代理实读两个未改的生产基线源码后修正守卫：存在时必须等于固定值，位置/被过滤导致的缺省不算价格变化。顶层pricing_version、全部价格/表达式/倍率等业务字段仍逐项严格比较，保存raw SHA与语义SHA，不声称跨进程原字节一致。

回滚不仅检查策略/本地迁移与规则，还检查未完成任务的carpool_snapshot；即使当前已恢复空规则，仍需等新计费任务结算才能退旧软件。这里只发布软件，不自动执行需求方页面的Luna迁移。

## 2026-10-20 实际发布完成

18:05软件切换成功，程序56892ffcc5467ad6e039a446a2041c6c4a60bb0ba784a53f65d92e97be19136a、镜像db4eb820a0255018f29a228b64a1aba5c1934ef314d0b96b20edc7327fdd224b；源e6db31c6，主代理完整基线/语义pricing/原接口/956资产/114依赖/其他服务检查通过。脚本实际prepare曾因Docker派生Mounts数组顺序不稳定误判，未切换；原attempt保留，确认identity/config/host/network/pricing/options/schema不变后规范化挂载排序、重新prepare通过。没有重置其他服务或关闭守卫。

随后拼车后端352315f和share-02上线，新增管理schema；18:13自动下发4组空规则，18:16读回一致，Luna未迁移、现价不变；18:23公网版本/资源通过。new-api现有表结构未变。源码/程序/配置私有备份均在/opt/carpool/releases/newapi-carpool-billing-20261020，旧容器停止保留。完整联动发布与回滚记录在CP docs/4-发布记录/部署记录-20261020.md §4。

新API脚本verify的options基线是软件切换阶段；配套CP之后新增CarpoolBillingPolicy属于预期变化，不要拿旧基线作长期全站状态检查。退旧软件须先退配套CP并满足策略、迁移和未完成任务计费守卫。没有恢复数据库、发模型测试请求或替用户执行Luna迁移。

## 2026-10-20 19:34 用户已执行Luna迁移

用户19:34:18完成Luna迁移（备份#1）；19:42只读核验8模型标价减半、4组×2策略一致、其他模型/覆盖不变。审计跨度112.288ms（含精度余量上界113.288ms），窗口整秒候选0；截至日志599627有1条迁移后消费，实扣/复算/迁移前同价均362quota，差额0。两服务健康、无pending，旧程序直接回滚守卫已只读确认拒绝。助手未发模型请求或改数据。

当前8条Luna倍数全2，模型模式与覆盖值不变，目标表达式与备份中的官方预设完全相同。窗口按落库精度扩大核查，当前真实样本1条，不声称3条。r15.17直接回滚只读守卫已拒绝，不能再沿空规则发布阶段直接降级；必须先完整处理计价状态。完整证据与步骤见CP部署记录-20261020.md §5。本轮只读，无服务/数据库/计价修改。
