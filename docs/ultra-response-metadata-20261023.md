# 可信 Codex Ultra 日志元数据

## 变更记录

| 日期 | 版本 | 变更说明 |
|---|---|---|
| 2026-10-23 | v0.13.2-carpool-r15.21-ultra-20261023已发布 | 方案92元数据链路上线，本地/独立审核与线上自然false通链、健康核验通过；真实Ultra=true自然样本待用户观察。 |

2026-10-23已发布；实现基于r15.20且保持Go1.26.10，实际发布状态以拼车主工程方案92与当日部署记录§7为准。

成功HTTP Responses响应中的`X-Codex2API-Ultra`被作为独立可空`RelayInfo.CodexUltra *bool`读取。只有ChannelBaseUrl、原请求地址及重定向后的最终响应地址均为`http://codex2api:8080`、无userinfo，且实际请求为`POST /v1/responses`时才信任。此逻辑不依赖`CODEX_RELAY_TRACE_ENABLED`。客户端同名头在transport前删除；不从入站请求采集。

精确唯一`true`/`false`才有效；缺失、非法、重复（含大小写混用Header键）、合并值、非2xx和非可信上游均为未知。每次外层attempt及transport入口清空旧值，防止换渠道、转换失败或网络错误保留前次元数据。

`GenerateTextOtherInfo`仅非nil时写`other.ultra`布尔值；`other.reasoning_effort`保留原始值。未知时完全沿用旧日志结构。不改请求/响应body、计费、路由、Key、数据库schema；不解析输入提示词或configuration_update判断Ultra，也不扩展失败日志语义。

定向测试覆盖可信/非可信/重定向/重复/非法/缺失、伪造头、重试清空、请求和响应body原样、原effort与可空布尔日志；仅使用本地fake transport/测试数据库，不调用真实模型。


## 实际发布标识

镜像`72f578e5c9e6ba3261e06700c3eb0a9606209b5aa2fa10c8f4dce49b1ddaaeca`，程序`677e5f4ec912ab30d46880ddb6b7f3d16aa31f13a621491d535a74b4e9ad8a09`。原前端资源和业务参数保持；未发送真实模型测试请求。线上新请求已有明确ultra=false并保留xhigh，经拼车日志API读回；true分支本地fake上游/日志及真实组件回归通过，暂未取得发布后的真实Ultra=true样本，不虚构已真人验收。完整备份/精确回滚见E:/OtherPro/carpool-platform/docs/4-发布记录/部署记录-20261023.md §7。
