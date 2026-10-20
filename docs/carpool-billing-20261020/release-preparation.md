# r15.18 发布脚本准备回执

## 变更记录

| 日期 | 版本 | 变更说明 |
|---|---|---|
| 2026-10-20 | async-rollback-guard-61 | 补齐未完成异步任务冻结快照降级门禁及 10 项假 SQL 检查，累计 61 项通过；最终源码白名单对本地 601 项生产基线实比为 5 新增 / 12 修改。 |
| 2026-10-20 | source-delta-pinned | 主代理确认 5 新增 / 12 修改 Go 文件白名单，已固定脚本。新增测试修复包括 stream_scanner 原实现断言对齐与 affinity 测试 Key 独立；不更改运行依赖。 |
| 2026-10-20 | offline-guards-51 | 新增 7 项精确基础镜像层及 verify 只读拒绝检查，累计 51 项通过；构建后拒绝额外层或基线 tag 漂移。 |
| 2026-10-20 | offline-guards-44 | 本地 44 项假运输/纯函数检查通过：pricing 严格规范化、回滚计费门禁、同镜像所有权与失联 create 恢复、manifest 拒绝错误基线/依赖/差异。没有服务器操作。 |
| 2026-10-20 | draft | 完成软件发布脚本结构、r15.17 固定基线、受限恢复与计费守卫；源码增量白名单及候选制品由主代理整合后补齐。未 SSH、未部署、未提交。 |

## 文件与依赖

本代理只写本目录的 `deploy.py`、`README.md`、`release-preparation.md`，不改业务 Go、前端或 CP 业务。

仅复用服务器已有 `/opt/carpool/releases/sensitive-message-20260922/deploy.py` 暴露的 `ops` 传输与 `recover` 内嵌提取函数。该依赖继续使用 `r15-20260906/release_r15.py` 和 `extract_embedded_frontend.py`。**不调用旧脚本的 prepare/cutover/verify/rollback**，其历史版本/容器 ID 和 stage 门槛不参与本次发布。

源基线必须等于 `/opt/carpool/releases/newapi-usage-aggregate-20261019/build-manifest.json` 的 `source_files`；该旧 manifest 的版本和程序 SHA 必须吻合当前运行 r15.17。前端必须等于同目录 `frontend-manifest.json.files` 的两主题 956 项，不能复用旧 r15.16 容器配置。

## 输入文件

发布目录 `0700`；JSON/配置与回执 `0600`；程序在该受限目录内为 `0755`，以保留容器既有运行用户的执行权限。所有凭证/完整容器配置只保存在服务器该受限目录，不进入 Git 或回执输出。

- `new-api`：主代理构建的 Linux 候选程序。
- `frontend-manifest.json`：`{"files":{"web/default/dist/...":"sha256", "web/classic/dist/...":"sha256"}}`，全量 956 项。
- `build-manifest.json`：沿用已有 source map，并增加当前容器和完整依赖清单：

```json
{
  "version": "v0.13.2-carpool-r15.18-billing-20261020",
  "baseline_container_id": "bd726f2b1f6e5d336a4391671b1b00336663bf88afa5220e60dfa41180f2ce63",
  "baseline_image": "sha256:82ffa348e9abe6f352253a56eed6737afb2e480a5e83ae78b9580526ca0af28b",
  "baseline_binary_sha256": "756970f5ee85edef29eaada69bad653303e1443cb1029b5d0e4b3d2277764e3d",
  "binary_sha256": "候选实际 SHA-256",
  "baseline_source_files": {"相对 Go 路径或 go.mod/go.sum": "SHA-256"},
  "source_files": {"相对 Go 路径或 go.mod/go.sum": "SHA-256"},
  "baseline_dependencies": ["从旧程序 go version -m 提取的规范化完整 dep 记录"],
  "dependencies": ["从新程序 go version -m 提取的规范化完整 dep 记录"]
}
```

两份依赖清单必须各 114 项且完全相同；也可用等价的模块路径→完整版本/校验信息字典。主代理从两个二进制的 Go buildinfo 提取并绑定相应程序 SHA，服务器脚本不安装 Go 或联网重新解析依赖。`go.mod/go.sum` 还必须在源码清单中逐字节不变。

`ADDED`、`CHANGED` 已按主代理最后确认固定，不能根据候选 manifest 自行推断放行。候选输入在 prepare 后按 manifest SHA 固定，再改任何输入都需重新审核，不能覆盖既有恢复记录。

新增 5 文件：`controller/carpool_pricing_test.go`、`relay/helper/carpool_billing_test.go`、`service/carpool_billing_test.go`、`setting/billing_setting/carpool_policy.go`、`setting/billing_setting/carpool_policy_test.go`。

修改 12 文件：`controller/relay.go`、`model/task.go`、`relay/common/relay_info.go`、`relay/helper/price.go`、`relay/helper/stream_scanner_test.go`、`relay/mjproxy_handler.go`、`service/channel_affinity_usage_cache_test.go`、`service/log_info_generate.go`、`service/quota.go`、`service/task_billing.go`、`service/violation_fee.go`、`types/price_data.go`。与运行基线无关的 `verification/usageaggregate/main.go` 不属于发布源码 envelope；测试 JSON 快照不是运行 Go 源码差异，业务矩阵回执应另绑定其哈希。

## 命令行为与验证边界

- `preflight`：只读核对当前容器 ID/image/start/binary/version、聚合 true、CP 健康、无有效倍数/迁移、源码/依赖/资源清单。
- `prepare`：完整提取候选两个主题的资源并比对；保存原配置、全选项摘要、schema/index、公开 pricing、首页和其他容器；本机镜像基线固定到精确 SHA，`docker build --network=none --pull=false` 仅 COPY 候选程序，不改运行容器。
- `cutover`：再核对全部基线/输入，保存创建前 owner 记录，先创建不运行的候选，再停旧容器、改名、断开旧网络、启动新容器；原环境不加不删。只允许本次拥有的候选在失败时恢复。
- `verify`：只读检查版本/hash/原日志接口/聚合空范围鉴权/CP 健康；前端当前和旧入口代表探测；全量 options（动态并发策略单列）、schema/index 和其他容器保持；pricing 按 README 的严格语义逐项比对。**失败只报错，不执行回滚。**
- `rollback`：先验证全部可能容器名称/ID的所有权，再读策略和 CP 状态；不安全时零删除。允许后才删除本次候选并恢复原 r15.17 容器/网络。支持 create 成功但响应/新 ID 落盘失败的候选名称恢复；不删除后续同镜像容器。

采用 release 命令互斥锁防同脚本并发；其他历史脚本未必使用该锁，所以每个切换/删除仍依赖实际 ID 与所有权校验，不声称跨所有部署工具实现全局锁。

回滚还检查未完成异步任务：`tasks.status` 严格排除 `SUCCESS` / `FAILURE`，其余最多读取 1001 行两项布尔事实；第 1001 行触发预算拒绝。SQL session 只读、UTC、单 SELECT 最长 3 秒。仅返回 JSON 合法性和 `billing_context.carpool_snapshot` 的 presence，不取回/输出任务私有 JSON。SQL NULL 是旧 `TaskPrivateData.Value()` 的已知空值，可以通过；坏 JSON、非对象、异常 billing_context、查询失败或缺 tasks 表一律保守拒绝。即使当前倍数清空，仍需等待冻结快照任务由 r15.18 结算后再退旧程序。

## 当前状态

脚本已完成 Python AST 语法检查及 `git diff --check`。本地仅导入本脚本的纯定义，以假运输累计运行 61 项检查：

- 25 项 pricing / HostConfig / 计费回滚门禁：重排与版本标记位置变化等价，价格变更/丢重复集合元素/auto_groups 顺序改变不等价，重复模型/供应商/版本标记拒绝；OOM null/false 等价且 true 不等价；空策略通过，有规则、非法/重复字段策略、迁移、pending、本地规则拒绝；名称切换空档能从原容器 ID 取得只读 DSN。
- 8 项容器所有权：后续同镜像容器、new ID 不匹配、old_id 标签不匹配、计费门禁失败均在任何删除前拒绝；正常回滚及 create 成功但未记录 ID 的恢复只删除本次候选，重复回滚不再删除。
- 11 项 manifest：合成正确材料通过；容器/镜像/二进制/源基线不符、删文件、越界新增、go.mod 变化、依赖缺失/变化和未固定源码白名单均拒绝。
- 7 项镜像基线与 verify：仅允许精确 r15.17 文件系统上新增一个 COPY 层，错误原镜像 ID、错误层前缀、多层/无新增层均拒绝；verify 面对后续容器或已回滚状态直接报错且没有 Docker 修改。
- 10 项未完成任务门禁：无任务/已知无快照通过；存在快照、未知 JSON 标志、损坏返回、预算超 1000 行、缺 tasks 表、SQL 失败均拒绝。测试断言查询只返回两项布尔事实、使用严格终态过滤和 3 秒只读预算；没有连接 MySQL，实际 JSON SQL 执行由服务器 prepare/rollback 检查。

假运输没有调用 Docker、网络、数据库或服务器命令。manifest 用例是合成材料，不冒称真实 r15.18 制品已通过。源码白名单已固定；真实候选制品及新旧 buildinfo 依赖清单仍由主代理构建后交给 prepare 验证。

另已把当前工作区每个既有 Go/go.mod/go.sum 文件与本地归档的 r15.17 `build-manifest.json.source_files` 的 601 项 SHA 逐项对比：没有缺失，变化集合恰为固定 12 项，5 个新增项均存在且不在原清单；此项为真实本地源码比对，不是服务器运行状态核验。

真实价格矩阵、变异测试、Go build/vet/test、生产基线探测和实际部署不是本回执已完成的内容；由各负责会话独立记录。不制造 stage-results，也没有模型调用或生产业务验收。
