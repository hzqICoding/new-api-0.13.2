# 拼车座位并发限制（r15.14）

> 方案与规则见拼车项目 [70 拼车座位并发限制](../../../../carpool-platform/docs/2-开发方案/70-用户并发限制.md)。本文记录 new-api 侧的改动、发布与应急方法。

## 变更记录

| 日期 | 版本 | 变更说明 |
|---|---|---|
| 2026-10-16 | r15.14（已准备，未发布） | 新增按用户的并发限制中间件；分支 `feat/carpool-concurrency-limit`；发布目录已 prepare、stage 通过，等待需求方在线时低峰切换。 |

## 改动范围（合并上游时需保留）

| 文件 | 改动 |
|---|---|
| `middleware/carpool_concurrency.go` | 新增。读 `common.OptionMap["CarpoolConcurrencyPolicy"]` 原文（持 `OptionMapRWMutex.RLock` 复制后释放），按原文缓存不可变快照；`issued_at` 进程内只增不减；off / observe / enforce；每用户 FIFO 有界队列；`defer` 按 lease 幂等释放；无按年龄回收 |
| `middleware/carpool_concurrency_test.go`、`carpool_concurrency_http_test.go` | 新增测试 |
| `router/relay-router.go` | 在 `/v1` 中继组与 gemini 兼容组的 `TokenAuth` 之后、`ModelRequestRateLimit` 之前各加一行 `Use(middleware.CarpoolConcurrencyLimit())` |

不改 `model/option.go`、配置注册、数据库、前端。策略键是未注册键：存库、进 OptionMap、启动加载、按 `SYNC_FREQUENCY` 刷新；旧版本只存不执行。

## 策略格式

```json
{"version":1,"issued_at":"2026-10-17T03:00:00.123456Z","mode":"observe","wait_seconds":10,
 "expires_at":"2026-10-17T03:15:00Z","user_limits":{"741":4}}
```

缺失 / 非法 / 过期 / 版本不符 → 不限制；`issued_at` 早于已采用的 → 忽略。拼车后端每 2–4 分钟写一次，有效期 15 分钟。

## 验证

- 本机：`go test ./middleware/` 通过；gofmt、vet、`go build ./...` 通过。
- serverB `golang:1.25.1` 容器 `go test -race -count=3 ./middleware/`：并发压测用例报 3 处数据竞争，**全部位于既有 `logger/logger.go:110` 的 `logCount++`**（上游注释写明有意不加锁的近似计数），本中间件只作为调用方出现在栈中；其余用例在 `-race` 下全部通过。限流器自身状态无竞争。该日志计数竞争是既有行为，生产中任何并发错误日志都会触发，不在本次改动范围内。
- 源码：分支归档与 r15.13 生产源码清单 589 项逐项比对，只有 `router/relay-router.go` 不同，另新增上述 3 个文件。
- 构建：Go 1.26.4，linux/amd64，`CGO_ENABLED=0`，`-trimpath`，`-ldflags "-s -w -X …common.Version=v0.13.2-carpool-r15.14-concurrency-20261016"`（与 r15.13 一致：r15.13 也是剥离符号的，依赖 114 项完全相同）。程序 SHA-256 `ad6e388bea9ce318dc903e3e4cdda1e31e7075b71442cf15f9a0bca1f0d9e4c3`。
- prepare：从新程序中提取的 956 个内嵌前端文件与 r15.13 归档清单逐项一致；镜像 `hzqicoding/new-api:carpool-r15.14-concurrency-20261016`（`sha256:dd537c918b406aa2369fee613da1628789b04aad961385fe2fa96a36f1795cf2`），基于 r15.13 镜像只覆盖 `/new-api`。
- stage（隔离 SQLite 实例 + mock 上游，真实 `/v1/chat/completions` 请求）：无策略两个并发都 200；enforce 上限 1 不等待 → 第二个 429 `carpool_concurrency_limit`，名额随后释放；enforce 等待 10 秒 → 第二个等 2.13 秒后 200；observe 两个都 200 且有超限日志；已过期策略不限制；先写新 off 再写旧 enforce → 旧的被忽略；前端资源 7 项、看板接口正常。

## 发布与回滚（serverB）

发布目录：`/opt/carpool/releases/newapi-concurrency-20261016/`

```bash
python3 /opt/carpool/releases/newapi-concurrency-20261016/deploy.py cutover   # 切换（失败自动回滚）
python3 /opt/carpool/releases/newapi-concurrency-20261016/deploy.py verify
python3 /opt/carpool/releases/newapi-concurrency-20261016/deploy.py rollback  # 回到 r15.13
```

回滚容器：`new-api-rollback-newapi-concurrency-20261016`（切换后生成，停止保留）。

## 应急关闭（按顺序）

1. 拼车后台「系统配置 → 用户并发限制」切「关闭」并确认同步成功。
2. 拼车后台异常时，**先停同步生产者**：确认拼车站点设置 `concurrency_mode=off`，或停掉 `carpool-backend` 容器。生产者未停时手工写 off 会被下次同步覆盖。
3. 然后在 serverB 执行（用拼车后端已有的 new-api 管理凭证，不打印）：

   ```bash
   python3 /opt/carpool/releases/newapi-concurrency-20261016/deploy.py policy-off
   ```

   立即生效（写入新的 `issued_at` 的 off 策略并读回核对）。或者什么都不做，等最多 15 分钟策略过期。
4. 以上都不行：`deploy.py rollback` 回到 r15.13。
