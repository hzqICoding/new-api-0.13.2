# Codex 协作登记

| 日期 | Canonical task | 模型/等级 | 职责 | 状态 | 说明 |
|---|---|---|---|---|---|
| 2026-10-05 | /root | 当前主会话 | 参照拼车端优化向导，scoped CSS构建、UI制品审核与r15.12发布 | 已发布，公网资源验证通过，视觉待用户验收 | 镜像60de5cd3…b71a；589项后端源码保持，713项旧非首页资源保持 |
| 2026-10-05 | /root/codex_setup_scripts | gpt-6-astra / high（复用） | 两步向导、四系统卡、窄屏/暗色、独立样式与现有契约回归 | 已交付、主代理复核后发布，可复用 | 原生followup复用，无新增会话；15组及14 URL案例通过，回执frontend-receipt.md |
| 2026-10-05 | /root | 当前主会话 | #490只读诊断、分组错误码区分、回归和r15.11发布 | 已发布，公网检查通过，页面待用户验收 | 保持TokenAuth相同分组授权，不更改实际Key/订阅/用户组；镜像57967c25…9815 |
| 2026-10-05 | /root/codex_setup_scripts | gpt-6-astra / high（复用） | 安全具体错误提示、初读/导出处理、前端定向回归 | 已交付并复核，可复用 | followup成功；仅canonical task，独立UUID未提供；15组验证通过；回执frontend-receipt.md |
| 2026-10-05 | /root | 当前主会话，未额外覆盖 | new-api Codex元数据、生产基线、增量资源、测试与发布 | r15.10已上线，实际用户验收待反馈 | 回执docs/codex-setup-20261005/README.md；不混提原有未提交改动 |
| 2026-10-05 | /root/codex_setup_scripts | gpt-6-astra / high（首次调用参数，复用） | 从carpool迁移classic向导、四系统包、定向验证及窄审身份竞态 | 已交付并复用审核，可继续 | 仅返回canonical task，独立UUID未提供；本线程followup已验证，跨客户端恢复未验证；最多主+1子代理，未扩容 |
| 2026-09-25 | /root | 当前主会话 | 前端兼容入口、缓存隔离、隔离构建与ServerB发布 | 已发布并完成接口/模块验收 | 回执 docs/frontend-module-fix-20260925/README.md；浏览器视觉验收待用户刷新确认 |
| 2026-09-25 | /root/frontend_dependency_audit | gpt-5.6-sol / high（调用参数） | 前端分包依赖审计与修复方案独立复核；仅写docs/frontend-module-fix-20260925/audit-* | 已完成，可复用 | Thread 01a0d771-0936-71b2-b7cb-15a42f8cc8c1；Session 01a0c49e-8bd9-7510-ad13-7a8e0763e458（继承关系未验证）；回执 audit-report.md；跨客户端恢复未验证 |
| 2026-09-23 | /root | 当前会话（未单独读取元数据） | Luna后端倍率豁免、测试、配置与发布整合 | 已发布并验收 | 父会话；回执docs/luna-pricing-20260923/README.md；未记录独立UUID |
| 2026-09-23 | /root/luna_pricing_ui | 元数据未向本线程暴露 | 前端价格展示及保持生产资源的最小补丁 | 已发布并验收，可复用 | Thread `01a0cc83-5d5a-7bc3-be0e-88c80dff8fbd`；Session `01a0c79b-ddb9-74f3-bf1d-d3b590062af4`（继承关系未验证）；回执 `docs/luna-pricing-20260923/frontend-production-patch.md`；本线程可 follow-up，跨客户端恢复未验证 |

## 2026-10-19 受控聚合候选（未发布）

复用 `/root/fast_list_backend`，gpt-6-astra/xhigh，实现model/controller/router聚合接口及测试，并只读审核拼车client接线及指定周期allowlist；独立UUID未提供。本线程followup复用已验证，最多主+1。主代理负责拼车接线/测试和临时只读HTTP实库比较，代理无生产操作。状态：交付及互审完成可复用；回执docs/usage-aggregate-20261019/README.md，拼车docs/81；未提交/推送/发布，默认关闭。不得把历史readonly80或本轮候选HTTP当成生产已切换。

2026-10-19部署收口：主代理在用户授权后完成r15.17默认关闭发布；/root/fast_list_backend仅交付发布脚本和deployment-preparation.md，无SSH操作。候选和关闭态生产检查通过，旧容器保留；详情usage-aggregate-20261019/README.md末节。状态已发布未启用shadow/灰度，源码f8a02685，未推送或合并main。

## 2026-10-20 wp86a 计费契约核对（needs-decision）

工作目录为本仓库，分支 `feat/carpool-billing-20261020`。任务书为拼车项目 `mailbox/to-codex/86a-newapi-carpool-billing.md`。只读核对发现异步任务 token 重算和 Realtime 分段预扣重新读取分组倍率，并不统一使用 `HandleGroupRatio` 的有效倍率；按任务书停工条件等待裁定。没有业务代码、测试或发布脚本交付，没有联网、SSH、部署或提交；仅登记本次协作状态。

| Canonical task | 模型/等级 | 身份 | 职责及回执 | 状态 / 接续 |
|---|---|---|---|---|
| `/root` | 当前主会话，工具未提供模型/等级元数据 | `CODEX_THREAD_ID` / `CODEX_SESSION_ID` 均为 `01a1580d-7314-7e11-9c91-653ec361cc20` | 契约、计费调用链及集成；最终回执在会话中，不另写回执文件 | 待裁定上述两条路径的修正口径 |
| `/root/billing_matrix` | `gpt-6-astra` / `xhigh`（调用参数） | `CODEX_THREAD_ID=01a1580e-a9e7-7753-8647-83c761d4b7a9`；`CODEX_SESSION_ID=01a1580d-7314-7e11-9c91-653ec361cc20`（与父会话相同） | 价格矩阵及 pricing 测试；回执确认未写文件、未运行测试，契约缺少完整 38 模型名单及 compact 改写目标 | 已停工；本次续接的 live agent 列表不含该代理，跨客户端恢复未验证 |
| `/root/billing_release` | `gpt-6.1-sol` / `high`（调用参数） | 工具仅返回 canonical task，独立 UUID 未提供 | 仅限新发布目录；因停工中断，工作区核对无该代理产物 | 已中断；本次续接的 live agent 列表不含该代理，跨客户端恢复未验证 |

后续先明确是否把上述绕行路径改为使用请求/任务冻结的有效倍率（兼容旧任务及无策略行为），并补齐 38 模型输入；再继续原任务书全部实现与验证。不把源码调用链核对视为价格矩阵或真实扣费测试通过。

## 2026-10-20 实现与发布接续

用户要求完成剩余。主代理核实旧wp86a仅needs-decision未写业务，已明确纳入异步任务和Realtime冻结路径，并只读采集38模型及映射。复用/root/fast_list_backend（沿用既有astra/xhigh，无新增UUID）负责17个Go增量与矩阵，/root/fast_list_review同配置负责发布脚本3文件；不另起子代理。两者已交付，主代理复核真实diff并跑CP实际复算12条全相等。主代理处理CP前端局部警告、R5计价时点、构建发布与台账。未把旧needs-decision记录改写为当时已实现；真实新回执为CP mailbox/from-codex/86a-completion.md。
