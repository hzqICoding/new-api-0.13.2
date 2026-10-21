# 本机Codex请求关联日志（r15.19，2026-10-21）

## 变更记录

| 日期 | 版本 | 变更说明 |
|---|---|---|
| 2026-10-21 | r15.19-resource-20261021 | 21:01已发布，只新增本机Codex HTTP转发元数据与关联头；计费、前端和schema保持。六组自然请求同ID/同大小验证通过。 |

实现提交`cc8369c39717d5b76506404d23f74a21df68e038`，基于已完成Luna迁移的r15.18。新增`relay/channel/codex_request_trace.go`及测试，在`doRequest`实际发送前调用；`go test ./relay/channel -count=1`通过。

启用环境变量`CODEX_RELAY_TRACE_ENABLED=true`；未设置默认关闭。仅`http://codex2api:8080`已知模型POST路径启用，服务器生成的请求ID覆盖客户端或渠道header override中的同名值，使用`X-Newapi-Request-ID`与codex2api关联。日志包含request_id、user_id、channel_id、attempt、path、body_bytes及known标记、stream/channel_test；未知长度为-1，不额外读取或复制请求体。没有正文、API Key、URL query或邮箱。此ID仅用于元数据关联，不作授权/计费身份。

当前：`v0.13.2-carpool-r15.19-resource-20261021`；镜像`sha256:608c40569380ef11b7d8038df04b36c3382e89553d2a21b0d70e059c2d4ad956`；程序`7411f485c5f152d311ab2dbc999a2c415b4acfe6dd5f477fcfb11bee8aba0870`。原606项Go/module源码只1项修改/2项新增，956项内嵌前端逐文件一致；pricing语义、稳定options/schema通过发布守卫，Luna/拼车倍数及其他服务保持。

服务器资产与实际旧r15.18容器：`/opt/carpool/releases/resource-guard-20261021/new-api/`，回滚仅执行：

```bash
python3 /opt/carpool/releases/resource-guard-20261021/rollout.py rollback new-api
```

该回滚只撤本次观察性代码，保留r15.18计费能力和数据库。不要调用10-20旧脚本的rollback退r15.17。运行Env与Docker重启策略已复制保留；原服务没有`/opt/new-api/docker-compose.yml`，未来新建重建模板时须保留本开关及当前代码基线。

配套codex2api已发布3.1.0-resource.20261021，响应缓存独立Redis512MiB策略/768MiB容器；邮件资源告警也已启用。完整配置保护、真实Redis/邮件验证、自然请求证据、跨服务回滚和风险边界见[拼车发布记录](<E:/OtherPro/carpool-platform/docs/4-发布记录/部署记录-20261021.md>)。未发主动模型请求，浏览器/客户端验收仍由需求方完成。
