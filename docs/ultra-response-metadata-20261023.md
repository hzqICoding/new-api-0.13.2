# 可信 Codex Ultra 日志元数据

2026-10-23本地实现，发布状态以拼车主工程方案92与当日部署记录为准；当前生产基线为r15.20/Go1.26.10，不能使用旧Go重编后覆盖。

成功HTTP Responses响应中的`X-Codex2API-Ultra`被作为独立可空`RelayInfo.CodexUltra *bool`读取。只有ChannelBaseUrl、原请求地址及重定向后的最终响应地址均为`http://codex2api:8080`、无userinfo，且实际请求为`POST /v1/responses`时才信任。此逻辑不依赖`CODEX_RELAY_TRACE_ENABLED`。客户端同名头在transport前删除；不从入站请求采集。

精确唯一`true`/`false`才有效；缺失、非法、重复（含大小写混用Header键）、合并值、非2xx和非可信上游均为未知。每次外层attempt及transport入口清空旧值，防止换渠道、转换失败或网络错误保留前次元数据。

`GenerateTextOtherInfo`仅非nil时写`other.ultra`布尔值；`other.reasoning_effort`保留原始值。未知时完全沿用旧日志结构。不改请求/响应body、计费、路由、Key、数据库schema；不解析输入提示词或configuration_update判断Ultra，也不扩展失败日志语义。

定向测试覆盖可信/非可信/重定向/重复/非法/缺失、伪造头、重试清空、请求和响应body原样、原effort与可空布尔日志；仅使用本地fake transport/测试数据库，不调用真实模型。
