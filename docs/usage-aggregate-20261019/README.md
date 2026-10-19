# 受控用量聚合接口（2026-10-19）

状态：**r15.17已发布；2026-10-19 20:34按用户指定启用A2灰度。** 程序不变，new-api聚合入口true，拼车消费端仅周期336/337使用新统计。实现提交f8a02685；历史默认关闭发布与最新灰度/回滚见文末。

工作分支：`feat/usage-aggregate-20261019`，起点为既有 `feat/carpool-failure-log`。本专题只新增读取接口与测试；不修改旧日志接口、收费/配额、业务日志写入、表结构或索引。主代理另负责客户端接入与受限生产只读对照；其结果不能由本回执替代。

## 目的与边界

现有拼车统计逐页请求管理员日志，每页重复 COUNT/OFFSET、序列化和网络往返。此前候选 SQL JSON 聚合在缓存 token 尾换行字符串等边界上与 Go 解析不等价，因此本实现不使用 SQL `SUM`、JSON函数或正则解析。

新接口使用一次 `LOG_DB.logs` 窄字段 SELECT，流式在 Go 中汇总；继续按 `id DESC` 累加，保持带符号 token 的中间溢出判定顺序。它只提供成功消费的总计和调用方指定 bucket，不直接替代日期/模型明细、carry 债务、有效额度或写入校验。

## 接口契约

`POST /api/log/usage/aggregate`，路由使用现有 `middleware.AdminAuth()`。权限协议保持：匿名401；普通用户沿用旧中间件 HTTP200/`success:false`，未改成403。管理员凭证和 `New-Api-User` 请求头规则不变。

请求示例：

```json
{
  "version": 1,
  "tokens": [
    {"token_id": 11, "bucket": "seat-1"},
    {"token_id": 12, "bucket": "seat-1"},
    {"token_id": 13, "bucket": "seat-2"}
  ],
  "from_unix": 1700000000,
  "to_unix": 1700086400,
  "after_log_id": 0,
  "through_log_id": null
}
```

- 请求体最多128 KiB；必须为有效UTF-8 JSON。`version`仅接受整数1。
- `tokens`最多1000条；Token ID必须正数且不可重复，即使重复项bucket相同也拒绝。
- bucket必须为非空白有效UTF-8、最多128字节；不同bucket最多100个。保存原字符串，不trim或大小写折叠，不按数据库collation分组。
- `from_unix`与`to_unix`同时提供或同时为null/缺省；提供时必须`to>from`，执行`created_at>=from AND created_at<to`。Unix 0为有效边界，不等于未提供。纳秒到整数秒的ceil转换由客户端完成。
- 有Token且无日期边界时，必须`after_log_id>0`或`through_log_id`非null，避免无界历史扫描。
- 水位必须非负。`after_log_id>0`才添加`id>after`；普通日历查询不会额外添加`id>0`来改变优化器计划。读取时仍检查ID正数、范围和降序。
- `through_log_id:null`不自动冻结；显式0为空范围，`through<=after`同样直接返回空。冻结对照由调用方显式提供同一上界。
- 空Token集合直接返回空，不查询数据库；仍校验版本、时间/水位等参数。

成功使用标准 `success/message/data` 封套，`data`包含：

```json
{
  "version": 1,
  "complete": true,
  "scope": {"version":1,"tokens":[],"from_unix":null,"to_unix":null,"after_log_id":0,"through_log_id":null},
  "totals": {"requests":0,"input_tokens":0,"output_tokens":0,"cached_tokens":0,"amount_micro":0},
  "items": []
}
```

`scope`按解析后的请求结构原值返回，保留Token顺序及nil/显式零边界。每个请求bucket均返回一项，哪怕没有匹配日志；所有五个数值字段显式存在，bucket按Go字符串升序排列。不会输出原始日志、`other`、SQL或连接配置。响应设置`Cache-Control:no-store`。

## 精确数值口径

- 只查询`type=2`，排除失败5、退款6等其他类型；零quota行仍计一次请求。
- 每条quota必须非负且不大于`MaxInt64/2`，精确乘2换成micro。每个bucket和总计都做逐条int64溢出检查。
- `prompt_tokens`、`completion_tokens`保留正负号，并按日志ID降序检查累计上下溢出，不采用请求明细的非负截断。
- 缓存token完整复用同样的两阶段语义：`common.Unmarshal`到`map[string]json.RawMessage`，再把顶层`cache_tokens`解为`json.Number`后调用`Int64()`并检查非负。不读取任何其他缓存字段。
- JSON整数与`"123"`可用；`"01"`、`"+1"`、前后空格、`"1\n"`、小数、科学记数、负数、越界、布尔/null/数组/对象/坏JSON均归0。`-0`最终也是0。重复JSON成员按Go解码的后值覆盖，末项非法则归0。
- SQL NULL数值按旧GORM标量字段语义视为0；NULL `other`视为空。

## 受控资源与开关

- `CARPOOL_USAGE_AGGREGATE_ENABLED`默认关闭。controller每次请求读取`os.Getenv`，仅去除首尾空白后大小写不敏感的`true`启用；`1`、空值、`false`及其他内容都关闭。测试使用`t.Setenv`；部署环境改变仍需使用对应进程/容器的环境配置，不提供在线设置接口。
- 进程全局最多2个正在聚合的请求；无等待队列，忙立即503。
- `ReadUsageAggregate`创建独立于查询次数的5秒处理context，继承请求取消；所有SQL使用该context。
- MySQL SELECT附静态`/*+ MAX_EXECUTION_TIME(4000) */`，提供数据库端4秒查询上限；SQLite/PostgreSQL不加MySQL hint，保持context限制。不强制索引。
- 一次SELECT只取`id, token_id, quota, prompt_tokens, completion_tokens, other`，`ORDER BY id DESC LIMIT 100001`；第100001条触发失败，最多汇总100000条。
- 原始`other`累计最多64 MiB，超限失败。此限制在扫描行后检查，是处理预算，不声称限制数据库驱动接收单个超大字段前的瞬时内存分配。
- 任意取消、超时、扫描失败、行/字节超限、非法quota、累计溢出都返回nil结果；不会发布部分总计或`complete:false`伪成功。

controller固定错误响应：

| HTTP | message |
|---|---|
| 400 | `invalid_usage_aggregate_request` |
| 413 | `usage_aggregate_request_too_large` |
| 422 | `usage_aggregate_limit_exceeded` / `usage_aggregate_invalid_data` |
| 503 | `usage_aggregate_disabled` / `usage_aggregate_busy` / `usage_aggregate_cancelled` |
| 504 | `usage_aggregate_timeout` |
| 500 | `usage_aggregate_unavailable`（包含数据库驱动失败，原错误不对外返回） |

## 实现文件与调用签名

- `model/usage_aggregate.go`：协议类型、参数校验、有界流式查询、精确解析/累加。
- `controller/usage_aggregate.go`：开关、请求体限制、2并发准入、错误封套。
- `router/api-router.go`：新增管理员POST路由一行。
- `model/usage_aggregate_test.go`、`controller/usage_aggregate_test.go`、`router/usage_aggregate_test.go`：本地验证。

```go
func model.ReadUsageAggregate(
    ctx context.Context,
    request model.UsageAggregateRequest,
) (*model.UsageAggregateResult, error)
```

模型入口本身限制5秒/行数/字节数；管理员鉴权、feature flag和进程准入在controller层。生产入口必须使用已注册HTTP路由，不绕过controller直接对外开放模型方法。

## 本地验证结果

环境：Windows，现有`github.com/glebarez/sqlite`纯Go测试驱动；未修改依赖和go.mod。

1. `go test ./model ./controller ./router -run 'TestUsageAggregate|TestLogTokenScope|TestLogWatermark' -count=1`：全部通过；实际输出分别为`3.848s`、`4.017s`、`2.729s`。
2. `go test ./model ./controller ./router`：三个包全部通过，含原有接口回归；实际输出分别为`3.734s`、`3.659s`、`4.073s`。
3. 本轮Go文件已gofmt；`git diff --check`定向检查通过。

覆盖：真实AdminAuth路由；关闭/开启和固定错误；128KiB请求体；参数上下限；nil/0及半开日期；type2-only；同bucket多Token无重复；大小写bucket分离、零行补零、scope原序；缓存JSON边界；非法quota和金额/token累计溢出（含中间溢出但最终本可抵消）；读取前/读取中取消；行/other字节限制与失败不返回partial；真实并发两个SQL、第三个立即503；SQLite实际单次查询；MySQL/PostgreSQL/SQLite GORM SQL构造。

首轮两个epoch0测试夹具失败已修正：GORM会自动填充创建时为0的`CreatedAt`，故测试改为创建后显式UPDATE为0，再验证真实epoch0边界；没有为通过测试修改生产过滤规则。

边界：行/字节预算的超限执行分支使用缩小预算的本地夹具验证，并单独核对生产常量及SQL的`LIMIT 100001`；本轮未在本机插入十万行或分配64MiB日志来做性能压测。MySQL/PostgreSQL仅SQL构造测试，不声称本地连接实库测试通过。真实现网数据库的受限只读对照由主代理独立执行和记录；未部署本接口，未修改生产服务/数据。

## 2026-10-19 主代理候选HTTP对照补记（仍未发布）

主代理使用 `verification/usageaggregate/main.go` 编译一次性Linux验证程序，实际AdminAuth+AggregateUsage，通过loopback HTTP访问现有生产日志，所有MySQL连接session transaction_read_only=1/max_execution_time=4000；未启动应用main、迁移、作业或模型中继。临时容器0.5CPU/384MiB，只读文件系统，结束自动删除；现网两个服务镜像、启动时间和重启次数保持。

同一冻结上界584140，四组每组3次，金额/请求/输入/输出/缓存Token逐bucket与旧侧证据全相等：单座位8182条0.412–0.482秒，4座位13090条1.829–2.245秒，历史重置1811条0.044–0.062秒，显式0上界1.1–1.5ms。匿名401、关闭503通过。该结果是候选handler+鉴权+实库+响应耗时，不是公网/整页面/P95，也不是已经发布。

完整证据在拼车 `verification/usage-aggregate-20261019/http-results.json`；方案与灰度范围见拼车docs/81。程序SHA256 `1e8f8ca0c07e0de984b91b6b880d67670f8f1b6d3dac1143994b85ebc8b37f34`。主代理补做new-api应用入口Go build通过，未生成或替换生产发布包。原有前端、价格、渠道配置不变。

## 2026-10-19 默认关闭发布（已完成）

用户授权先发布默认关闭版本，当前r15.17已上线。源码f8a02685，镜像`sha256:82ffa348e9abe6f352253a56eed6737afb2e480a5e83ae78b9580526ca0af28b`，程序`756970f5ee85edef29eaada69bad653303e1443cb1029b5d0e4b3d2277764e3d`。`CARPOOL_USAGE_AGGREGATE_ENABLED`仍未配置，等同false；带管理员身份新API返回503 usage_aggregate_disabled、匿名401。拼车模式也保持off，未启用shadow或周期灰度。

596个旧源码基线与生产构建树一致，增量后601项源码；114个依赖不变；内嵌956资产与r15.16逐项匹配。使用新脚本preflight/prepare/cutover，未执行隔离业务stage、不伪造stage回执。Env/挂载/网络保持，数据库schema/index摘要一致；options除既有任务动态更新的CarpoolConcurrencyPolicy外逐项摘要一致。旧日志token过滤契约、版本/hash、首页和8个静态探测、拼车health通过，new-api切换阶段其余运行容器未改。之后主代理另发布拼车默认off后端；不得在这之后直接运行依赖“所有其他容器不变”的本轮verify来误判拼车的已授权更新。

2026-10-19T11:57:42Z新容器启动，RestartCount0；未配置Docker Healthcheck，运行与HTTP验证通过，不冒称Docker health为healthy。CF与直连/api/status均200/r15.17；未进行模型付费请求或浏览器验收。无表结构迁移、无人工额度/Key修改；切换进程不等于承诺所有在途请求零中断。

完整归档：serverB `/opt/carpool/releases/newapi-usage-aggregate-20261019/`，含受限配置快照、manifest、schema/options摘要及关闭检查；安全回执 [verification/r15.17-deployed-off.json](verification/r15.17-deployed-off.json)。旧容器`new-api-rollback-newapi-usage-aggregate-20261019`已确认停止保留。

回滚（先核对无后续发布）在serverB执行：

```bash
python3 /opt/carpool/releases/newapi-usage-aggregate-20261019/deploy.py rollback
```

恢复r15.16容器/配置/网络，不恢复或回写数据库。本轮没有执行生产回滚演练。拼车发布与回滚另见其10月19日部署记录§4；本地源码已提交，未推送/合并main。

## 2026-10-19 Pro20x-260910A2灰度（当前运行配置）

用户指定A2，pool3在用座位10/11的7天周期336/337。程序/镜像/前端不变；同镜像重建后，new-api新增CARPOOL_USAGE_AGGREGATE_ENABLED=true，拼车新增NEWAPI_DISPLAY_USAGE_MODE=aggregate、NEWAPI_DISPLAY_USAGE_BUCKETS=336,337。该allowlist在消费端控制，不是把new-api管理员接口做成只允许pool3；普通用户无此管理员权限，其他主号的拼车统计仍用旧路径。

先保持拼车off，做真实Go客户端新→旧→新对照：日志上界586159，1216条，五字段逐bucket和总计完全相等，旧14次HTTP约7.919秒，新两次各1个POST分别1.437/0.312秒且无fallback。对照回执15分钟有效，scope和Key集合保持后才开启拼车灰度。未配置全局shadow（此样本超过其500条上限），没有把采样跳过当作验证通过。

new-api新容器启动12:31:59Z，拼车12:33:57Z，均RestartCount0；拼车healthy。20:35观察尚未出现实际页面触发的新聚合日志，已通知用户刷新A2详情做页面体验验收，不能据配置验证宣称浏览器已验收。

运行Env只修改上述3项，没有修改compose/env文件。Docker restart保持；以后按其他配置重新创建容器须核对是否保留灰度。周期换新后allowlist不匹配会走旧路径，不永久跟随主号。

恢复材料serverB:/opt/carpool/releases/usage-aggregate-a2-20261019/；关闭态旧容器new-api-rollback-usage-aggregate-a2-20261019停止保留。回退必须先在serverB执行：

```bash
python3 /opt/carpool/releases/usage-aggregate-a2-20261019/rollout.py rollback
```

该操作先恢复拼车off，再恢复new-api关闭，程序仍r15.17。若还需回到r15.16，完成灰度回退后再用上节版本回滚；直接跳过灰度使用旧版本回滚脚本会触发容器所有权守卫。所有切换无人工业务数据写入，无数据库恢复。详细范围、首次未启动候选的OomKillDisable null/false守卫修正及证据见拼车10月19日部署记录§5。

A2灰度用户验收补记：20:41观察实际展示路径aggregate_ok两次（3个Token，528/444ms），无回退，CP ERROR级事件0；已完成的/v1/日志抽取72个200、1个401，未观察到5xx，不将其作为全量成功率/高峰证明。用户随后反馈“明显更快”，A2主号详情提速验收通过。仍只保留周期336/337灰度，未扩展其他主号或窗口。
