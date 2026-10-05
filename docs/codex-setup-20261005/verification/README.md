# 版本化发布回执

2026-10-05文档维护时从原构建目录及服务器发布目录复制，未重新生成或改写历史验证结果。

| 版本 | 构建证据 | 公网结果 | 本地验证摘要 |
|---|---|---|---|
| r15.11 · 错误提示 | [build-evidence](r15.11/build-evidence.json) | [public-verification](r15.11/public-verification.json) | 见专题README的定向Go/JS记录 |
| r15.12 · 两步UI | [build-evidence](r15.12/build-evidence.json) | [public-verification](r15.12/public-verification.json) | [validation](r15.12/validation.json) |
| r15.13 · 两条线路 | [build-evidence](r15.13/build-evidence.json) | [public-verification](r15.13/public-verification.json) | [validation](r15.13/validation.json) |

[服务器发布回执](server-release-receipts.json)按版本保存当时`final-verification.json`与`stage-results.json`，只包含校验结果、镜像/哈希等信息，不含认证凭证。

上一级原有`build-evidence.json`、`stage-results.json`、`final-verification.json`都是**r15.10首次发布**的回执，保留原样；最新状态见[r15.13及维护说明](../README.md)。这些回执均不能推导为真实用户浏览器或本机CLI验收通过。
