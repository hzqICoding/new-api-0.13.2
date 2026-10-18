# 拼车并发超限提示精简（r15.15）

> 方案见拼车项目 [73 座位并发上限单独配置](../../../../carpool-platform/docs/2-开发方案/73-座位并发上限单独配置.md) 的 §2 R11、§4.5。限流中间件本身见 [r15.14 并发限制](../carpool-concurrency-20261016/README.md)，应急关闭方法沿用该文档，`deploy.py policy-off` 在本目录同样可用。

## 变更记录

| 日期 | 版本 | 变更说明 |
|---|---|---|
| 2026-10-18 | r15.15（已发布） | 10:20（北京时间）`cutover` 通过，随后 `verify` 通过。回滚容器 `new-api-rollback-newapi-concurrency-message-20261018` 已停止保留。第一次 `cutover` 因核对快照里混入临时容器而判失败，脚本自动回滚到 r15.14，修正后重跑成功（见「发布经过」） |

## 改动

只改 `middleware/carpool_concurrency.go` 和测试，分支 `feat/carpool-concurrency-message`（基于 `feat/carpool-concurrency-limit`），提交 `a97b560f`。

| 拒绝原因 | 429 提示（数字取自当时策略） |
|---|---|
| 排队等满仍无空位 | `当前座位最大并发为 N，已等待 W 秒仍无空位，请稍后重试` |
| 排队已满，直接拒绝 | `当前座位最大并发为 N，排队已满，请稍后重试` |

- 等待秒数在入队时从策略里取，存在等待者对象上。
- 错误码 `carpool_concurrency_limit`、HTTP 429、错误格式、日志 `reason=timeout|queue_full` 都不变，new-api 追加的 `(request id: …)` 也保留。
- 状态机和锁都没有改动。

## 验证

- **本机**：gofmt、vet、`go test ./middleware/`、`go build ./...` 通过。把两种提示对调做变异，对应测试变红。
- **serverB 上 `golang:1.25.1` 容器跑 `go test -race -count=3 ./middleware/`**：
  - 共报 3 处竞争，全部在既有的 `logger/logger.go:110` 日志计数上，由 `TestCarpoolFixedEnforceStress` 并发写 429 时触发，与 r15.14 相同；限流器本身没有竞争。
  - 用 `-skip TestCarpoolFixedEnforceStress` 再跑 3 遍，其余用例全部通过。
- **构建**：Go 1.26.4，linux/amd64，`CGO_ENABLED=0`，`-trimpath`，`-ldflags "-s -w -X …common.Version=v0.13.2-carpool-r15.15-concurrency-message-20261018"`。
  - 源码在 r15.14 的构建目录基础上，只替换 2 个文件。
  - 依赖 114 项与 r15.14 完全相同。
  - 程序 SHA-256 为 `d90b21b16c242ef1bec4c5704c2275d1d683e25d513ce92854701df9b5e48c12`。
- **prepare**：内嵌前端 956 个文件与 r15.14（即 r15.13 归档）逐项一致。镜像 `hzqicoding/new-api:carpool-r15.15-concurrency-message-20261018`，`sha256:5b341ad6f1660ce80a62127a931b1d42a23b316f8d014f4f69262ee6fa06e7a0`。
- **stage**：隔离的 SQLite 实例加 mock 上游，跑真实中继请求，以下几项全部通过：
  - 无策略时放行；
  - enforce 上限 1、不等待时第二个请求返回 429，提示为「当前座位最大并发为 1，已等待 0 秒仍无空位，请稍后重试」，名额随后释放；
  - 排队后成功；
  - observe 只记录；
  - 策略过期后放行；
  - 旧策略被忽略；
  - 前端资源与看板接口正常。
- **verify**：
  - 版本、程序哈希、令牌日志接口、拼车健康检查都正常；
  - 前端资源、Luna 价格正常；
  - 其他 19 个容器未变；
  - `options_unchanged=false`，原因是拼车每 2–4 分钟会重写一次策略键 `CarpoolConcurrencyPolicy`，这是预期内的。

## 发布经过

发布目录：serverB 上的 `/opt/carpool/releases/newapi-concurrency-message-20261018/`。

1. `prepare`、`stage` 通过。
2. **第一次 `cutover` 失败并自动回滚**：
   - `prepare` 时，`-race` 测试的临时容器（`docker run --rm`，随机名 `jovial_kare`）正在运行，被一起记进了 `other-containers.json`。
   - 切换后核对「其他容器未变」时，这个容器已经不存在，于是判失败，脚本自动回滚到 r15.14 原容器（同一容器 ID，`cafd12dad507…`）。
   - 回滚后 `/api/status` 正常返回 200，版本为 r15.14。
   - 失败状态保存为 `deployment-state.failed-1.json`。
3. **修正后重跑**：其余 19 个容器逐个核对 ID 与启动时间，均未变化。从快照中去掉这个临时容器（原文件保留为 `other-containers.with-race-container.json`），再执行 `cutover` 和 `verify`，都通过。
4. **影响**：10:19–10:20 之间 new-api 共重启 3 次（切换、回滚、再切换），正在进行的请求会被中断，客户端会自动重试。

**教训**：从 `prepare` 到 `verify` 期间，不要在服务器上启动或删除其他容器，包括 `-race` 用的临时 Go 容器。要么在 `prepare` 之前跑完，要么在 `verify` 之后再跑。

## 回滚

```bash
python3 /opt/carpool/releases/newapi-concurrency-message-20261018/deploy.py rollback   # 回到 r15.14
```
