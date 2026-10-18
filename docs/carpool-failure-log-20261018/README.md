# 中继失败请求写入日志（r15.16）

> 方案见拼车项目 [75 失败请求记录与用量明细展示](../../../../carpool-platform/docs/2-开发方案/75-失败请求记录与用量明细展示.md)。并发限制与应急关闭沿用 [r15.14](../carpool-concurrency-20261016/README.md)；本目录的 `deploy.py policy-off` 同样可用。

## 变更记录

| 日期 | 版本 | 变更说明 |
|---|---|---|
| 2026-10-19 | r15.16（已发布） | 00:07（北京时间）`cutover` 通过，随后 `verify` 也通过。回滚容器 `new-api-rollback-newapi-failure-log-20261018`（r15.15）已停止，保留备用 |

## 改动

分支 `feat/carpool-failure-log`（基于 `feat/carpool-concurrency-message`），提交 `cb6b59f1`。

- **新中间件 `middleware/relay_failure_log.go`**
  - 挂载位置：在 `/v1` 与 gemini 兼容组中，`TokenAuth` 之后、拼车并发限制之前。
  - 记录范围：已通过令牌认证、最终状态码 ≥ 400 的请求。
  - 写法：每个请求写**一条**类型 5 日志，`other.failure` 包含 `status`、`reason`、`code`、`path`、`suppressed`。
  - 写日志不受 `ERROR_LOG_ENABLED` 控制。生产环境中该开关是关的，所以只会写这一条最终日志。
- **每用户每分钟 30 条上限**：超过的部分只计数，在下一条写入的日志里用 `suppressed` 字段带出。
- **原因代码**：在以下几处拒绝时写入原因代码：并发（`concurrency_timeout` / `concurrency_queue_full`）、每分钟限流（`rate_limit`）、分发（`request_invalid` / `model_unavailable`）、额度不足（`quota_insufficient`）。没有写入原因代码的，按状态码和错误码兜底判断。
- **内存版每分钟限流**：原来只返回空的 429，现在带上提示「请求过于频繁：N 分钟内最多 M 次请求（含失败），请稍后重试」，错误码为 `model_rate_limit`。
- **性能**：成功请求不缓冲，流式响应不受影响；只在失败时截取最多 4 KB 的响应体。

## 验证

- **本机**：
  - 以下检查全部通过：gofmt、vet、`go test ./middleware/ ./model/ ./controller/`、`go build ./...`。
  - `service` 包有 2 个用例失败。在不含本次改动的 HEAD 上也会失败，属于原有问题，与本次改动无关。
- **serverB 上用 `golang:1.25.1` 容器跑 `-race`**（在 `prepare` 之前跑完，容器已删除）：
  - `go test -race -count=3 -skip TestCarpoolFixedEnforceStress ./middleware/`：通过。
  - `TestCarpoolFixedEnforceStress` 报了 3 处竞争。堆栈都在原有的 `logger/logger.go:110` 日志计数上，经过 `abortWithOpenAiMessage`，与 r15.14、r15.15 相同。新中间件不在这些堆栈里。
  - `controller`、`service` 的失败记录相关用例在 `-race -count=2` 下全部通过。
  - 测试依赖 testify 不在旧的模块缓存里，本次通过 goproxy.cn 下载到发布目录的 `race/gomod`。
- **构建**：
  - 环境：Go 1.26.4，linux/amd64，`CGO_ENABLED=0`，`-trimpath`，`-ldflags "-s -w -X …common.Version=v0.13.2-carpool-r15.16-failure-log-20261018"`。
  - 源码：在 r15.15 构建目录的基础上替换 10 个文件。其余 Go 文件与分支的 `git archive` 逐个比对，完全一致。
  - 依赖：114 项，与 r15.15 完全相同。
  - 二进制 SHA-256：`c987f75bb5db4a72119951da575351c34d7b2c17f7f5369ccdcc5c7d6064afd6`。
- **prepare**：
  - 内嵌前端 956 个文件，与 r15.15 逐个比对一致。
  - 镜像 `hzqicoding/new-api:carpool-r15.16-failure-log-20261018`，ID 为 `sha256:45a4d257a4f048bdca4c60b12e77881f2814425b9f0222ef5d8409ff1174fb18`。
- **stage**：在隔离的 SQLite 实例上配 mock 上游，跑真实的中继请求。
  - r15.15 原有的各项检查全部通过。
  - 新增 3 项也通过：
    - 并发 429 写入了 `concurrency_timeout` 记录；
    - 无渠道的模型返回 503，并写入 `model_unavailable` 记录；
    - 每分钟限流返回 429，提示为「请求过于频繁」，并写入 `rate_limit` 记录。
  - 成功请求不产生类型 5 记录。
  - stage 中途失败过两次，都是测试脚本自己的问题，已在脚本里修正：
    - 请求刚结束就马上发下一个，赶在名额释放之前，所以判了 429。修正方法是中间等 0.5 秒。
    - 循环变量名和令牌变量都叫 `key`，令牌被覆盖，返回 401。
- **verify**：
  - 版本、二进制哈希、令牌日志接口、拼车健康检查、前端资源、Luna 价格都正常。
  - 其他容器没有变化。
  - `options_unchanged=false` 在预期之内：拼车每隔几分钟会重写一次并发策略键。

## 回滚

```bash
python3 /opt/carpool/releases/newapi-failure-log-20261018/deploy.py rollback   # 回到 r15.15
```
