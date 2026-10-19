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
