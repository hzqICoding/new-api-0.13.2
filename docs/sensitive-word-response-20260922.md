# 敏感词错误提示优化与生产源码核对

> 下文“线上”“新版本”指2026-09-22当时的状态。当前运行r15.13，接续先读[维护索引](README.md)。本次文档整理没有重新进行生产请求验收或执行旧回滚脚本。

## 变更记录

| 日期 | 版本 | 变更说明 |
|---|---|---|
| 2026-09-22 | v1.1 | 已部署 `v0.13.2-carpool-r15.6-sensitive-20260922`；隔离实例接口验证、现网健康和拼车日志接口通过，观察到 6 次正常 relay 200、0 次 relay 5xx，旧容器保留。 |
| 2026-09-22 | v1.0 | 只读核对生产版本；本地优化敏感词错误提示，5 项针对性用例通过，尚未部署。 |

## 生产基线核对

- 本地目录：`E:\OtherPro\new-api-0.13.2`。
- 线上：`serverB` 的 `new-api` 容器，版本 `v0.13.2-carpool-r15.6`。
- 镜像：`sha256:ac7787130db42999cb4440760a9f81d20aa4ebbc7ce5ee62d7313513a60e65cb`。
- 运行程序 `/proc/1/exe` 与 `/opt/carpool/releases/seat-reset-20260907/new-api` SHA-256 一致：`84cd0991390896be6194ab1692cc20c5f48389fd36e38333dfdb33b4eac874ca`。
- 该发布目录的 `newapi-source.tar.gz` 中全部 Go 文件、`go.mod` 和 `go.sum` 共 583 项，与修改前本地文件逐项 SHA-256 相同；本地无额外 Go 文件。
- 核对包括本地已有未提交的拼车业务补丁。本次未覆盖或提交这些改动；未对前端源码逐文件比对。

## 改动

仅调整 `controller/relay.go` 中已命中敏感词的错误构造：HTTP 500 改为 400，保留 `sensitive_words_detected`，标记 SkipRetry。提示为：

> 请求包含敏感词「xxx」，已被拦截。请删除或替换当前消息及历史对话中的相关内容后重试。如仍有问题，请联系管理员。

仍使用原匹配器返回的命中词，不扩展为扫描全部词条；保留现有错误封装及请求 ID 机制。未改变词库、匹配规则、计费、鉴权、限流或渠道转发逻辑。HTTP 400 有助于客户端停止自动重试，但客户端自行实现的重试策略不受服务器保证。

## 验证

`go test ./controller -run TestRelaySensitiveWords -count=1 -v` 通过：

- 当前消息命中（含大小写匹配）。
- 历史消息命中。
- Responses 流式请求在转发前返回 JSON 400。
- Claude 请求返回原协议错误结构及中文提示。
- 普通文本通过过滤到达价格校验；使用未定价测试模型停在后续校验，无上游调用及扣费。这不是完整正常请求端到端测试。

`git diff --check` 通过。初次完成本地修改时尚未部署；后续发布结果见下节。

## 已完成发布与回退

2026-09-22 经用户授权完成部署，保留现有未提交业务补丁。发布前再次按原归档哈希比对，只有 `controller/relay.go` 改动和新增测试文件。使用 Go 1.26.4 本地交叉编译（linux/amd64、CGO_ENABLED=0、trimpath），原样嵌入从生产二进制提取的 357 个前端文件；新旧二进制内嵌资源逐项相同。镜像基于当前旧生产镜像，仅覆盖 `/new-api` 和发布标签。

- 新版本：`v0.13.2-carpool-r15.6-sensitive-20260922`。
- 新镜像：`hzqicoding/new-api:carpool-r15.6-sensitive-20260922`。
- 镜像 ID：`sha256:e0bce69b70a23f0d558c0f50fe4ac54c1bd514c6de10bc91fe062a3b99cac502`。
- 二进制 SHA-256：`e89e4ba6eea4020d55e7adbdc8445d062c379c08853c44e92caba542f643f50d`，与现网进程 `/proc/1/exe` 一致。
- 发布目录：`serverB:/opt/carpool/releases/sensitive-message-20260922/`，保存源码、前端归档、新程序、部署脚本、镜像信息和验证回执；配置快照及数据库结构/options 备份仅保存在服务器。
- 环境变量、端口、挂载、网络、重启策略与关键资源配置均按旧容器复制校验。全部 options 摘要前后一致，拼车后端容器 ID 和镜像未变；无业务配置或数据库结构变更。
- 新镜像在临时 SQLite 实例中经实际鉴权/路由验证：Chat、Responses、Claude 返回 400 和中文提示；正常文本到达价格校验。临时实例及测试数据随容器清理，未触及生产账号。
- 切换后内网 `/api/status`、拼车 token-scoped 日志查询、拼车 `/health` 通过。用户网络访问公网 `/api/status` 返回 200 和新版本；服务器本机经公网探测返回 403，未将其当作源站故障，也未调整网络/WAF 配置。
- 复核时新容器运行正常、重启计数 0，切换后生产日志已有 6 次 relay 200、0 次 relay 5xx；这是观察窗口内结果，不代表所有业务全量回归。
- 回滚容器：`new-api-rollback-sensitive-message-20260922`，已停止且保留原镜像。

容器回滚命令（恢复旧容器配置与网络，不恢复数据库）：

```powershell
ssh serverB "python3 /opt/carpool/releases/sensitive-message-20260922/deploy.py rollback"
```

源码回退只撤销此次敏感词错误构造及新增测试，不 reset/clean 整个工作区。未来发布须从本地完整补丁源码继续构建，不可回退到上游原版。
