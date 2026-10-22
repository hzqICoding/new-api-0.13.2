# Go 1.26.10 安全重编译与发布（lan205 → ServerB）

## 变更记录

| 日期 | 版本 | 变更说明 |
|---|---|---|
| 2026-10-22 | maintenance-handoff | 补充 lan205 的现有工具链、构建输入/产物、后续复用限制与部署脚本依赖；补记 CPAMP 后续已独立上线，区分安全发布时快照和后续计费配置。仅更新维护文档。 |
| 2026-10-22 | r15.20-go12610已发布 | 12:42完成ServerB切换，运行程序Go1.26.10、公开状态与范围日志/聚合验证通过，定价/稳定options/schema及其他服务保持。旧r15.19容器保留，附CPA/codex2api同类更新核查。 |
| 2026-10-22 | r15.20-go12610候选 | 用户指定lan205编译；官方工具链校验、定制源码构建、x509及定向回归、956项内嵌前端和依赖清单核对全部通过。尚未部署ServerB。 |

## 目标与状态

针对New API rc.46所引用的CVE-2026-111943（Go crypto/x509），用官方Go1.26.10重编译r15.19定制版，**不是整体合并v1.0.0-rc.46**。编译阶段仅生成候选；用户随后授权上线，现网已为r15.20 / Go1.26.10。没有调整生产渠道、计费、schema或业务配置。

- 官方发布：https://github.com/QuantumNous/new-api/releases/tag/v1.0.0-rc.46
- 官方构建修复：https://github.com/QuantumNous/new-api/pull/7942
- 工具链官方清单：https://go.dev/dl/?mode=json&include=all
- 本地仓库HEAD：`b997b19774dbe0786f3e68bfcbb83ea8287eddc2`，其中生产实现提交 `cc8369c39717d5b76506404d23f74a21df68e038`。
- 构建源：既有生产归档 `E:/tmp/newapi-resource-guard-20261021/source.tar.gz`，608项Go/module文件与当前仓库内容一致（比较时仅规范CRLF）；构建过程这些文件逐项SHA未变化。
- 源码归档SHA256：`3b5072f987d7b1830a2dac970ffc5b6662e1eba943c72409ef6ee2b7958d626a`。

## lan205环境

- 主机 `192.168.31.205` / `lan205`，普通用户 `uatair`，i7-10700 8核16线程、32GB、NVMe。
- 独立工具链：`/home/uatair/.local/toolchains/go1.26.10/go/bin/go`。不使用sudo、不覆盖系统目录/PATH、不修改SSH授权。
- 官方包：`go1.26.10.linux-amd64.tar.gz`，SHA256 `ccfe7ae822d2c2f41f4fe7c956199967a4dac22d0ee0d2b2bfdb440aab3b20c5`；下载后本机和lan205均校验通过。
- 工作目录：`/home/uatair/builds/newapi-go12610-20261022`，源码src、程序out/new-api、日志logs、build-report.json、buildinfo.txt。
- 用户缓存：`/home/uatair/.cache/newapi-go12610`、`/home/uatair/.cache/newapi-gomod`，保留供后续增量构建。
- GOPROXY使用goproxy.cn，再回退proxy.golang.org/direct；GOSUMDB仍为sum.golang.org，经镜像校验代理，未关闭校验。lan205直连官方模块站有超时，国内镜像正常。
- Go编译使用GOTOOLCHAIN=local、GOENV=off、CGO_ENABLED=0、GOOS=linux、GOARCH=amd64、GOMAXPROCS=6、`-p 6 -mod=readonly -trimpath`。
- go.mod/go.sum及业务源码均未修改；当前仓库Dockerfile/旧CI仍可能选择旧Go，**不能直接用未调整的旧构建入口发布**。本次通过绝对路径选择1.26.10且在脚本中强校验版本。正式后续标准化构建可另行将Docker/CI工具链下限同步。

## 后续复用 lan205 构建

服务器硬件、SSH 和网络现状统一维护在 [服务器总览](<D:/Obsidian Vault/vps-ops/my-servers/服务器总览.md>)及[lan205 档案](<D:/Obsidian Vault/vps-ops/my-servers/vps-lan/lan205-服务器服务总览.md>)。使用完整命令 `ssh uatair@192.168.31.205`，本机尚未配置 `lan205` SSH 别名或公钥免密；需要局域网可达。凭据不复制进项目仓库。

在 lan205 登录后，可先执行以下只读检查：

```bash
hostname
uptime
free -h
df -h /
/home/uatair/.local/toolchains/go1.26.10/go/bin/go version
/home/uatair/.local/toolchains/go1.26.10/go/bin/go version -m /home/uatair/builds/newapi-go12610-20261022/out/new-api
ls -lh /home/uatair/builds/newapi-go12610-20261022/out/
```

| 构建资料 | 本轮位置 / 要求 |
|---|---|
| 脚本 | [build.py](build.py)，固定本轮 ROOT、VERSION、Go 包和源码 SHA；不是通用构建命令 |
| 输入文件 | 工作区根目录内的 `go1.26.10.linux-amd64.tar.gz`、`source.tar.gz`、`frontend-manifest.json`；源码归档包含两主题生产 `dist` |
| 输出 | `out/new-api`、`logs/dependencies.log`、`logs/build.log`、`logs/x509.log`、`logs/regressions.log`、`build-report.json`、`buildinfo.txt` |
| 项目内回执 | [构建耗时/内存及结果](build-report.json)、[制品验证](artifact-verification.json) |
| 共享缓存 | `~/.cache/newapi-go12610`、`~/.cache/newapi-gomod`，后续可继续复用；不要仅因目录名带日期而清空 |
| 前端工具 | lan205 后续为 CPA Manager Plus 安装了 Node 24.22.1；本轮 New API 复用生产前端，未用 Node 重新打包。需要重建 New API 前端时另按项目 Bun 工作流准备 |

后续修改应建立新的日期/版本工作区，保留本轮源码、制品和日志作为回退证据。复制构建脚本后更新固定 ROOT / VERSION / 输入 SHA，并重新核对源码提交、当前定制前端清单及必要测试；不能只替换压缩包或关闭 SHA 断言。本轮脚本仅在 `src/` 不存在时解压，已有工作区直接换包不会自动刷新源码，因此不要复用旧 `src/` 来声称构建了新代码。

产物先回收至当前 Windows 工作站，核对二进制 SHA256、`--version`、`go version -m` 和前端清单，再进入 ServerB 发布阶段。lan205 无需生产数据库、API Key 或 ServerB 登录凭据；仅复制源文件到 lan205 不会更新生产服务。

部署脚本还依赖 ServerB 的 `/opt/carpool/releases/resource-guard-20261021/rollout.py` 与 `/opt/carpool/releases/r15-20260906/extract_embedded_frontend.py` 等既有发布工具。清理历史目录前先检查导入关系；新部署应重新固定实际生产基线，不能孤立复制本目录 `deploy.py` 后直接运行。

## 实测耗时与内存

构建记录时间：2026-10-22 12:00:14至12:02:08（Asia/Shanghai），不包括此前安装包下载和源文件传输。

| 步骤 | 实测耗时 | 采样进程树RSS峰值 | 主机最低可用内存 |
|---|---:|---:|---:|
| 冷缓存依赖下载 | 28.49秒 | 25.1MiB | 30787.6MiB |
| 程序编译 | 22.93秒 | 1402.0MiB | 28937.6MiB |
| Go标准库crypto/x509测试 | 40.67秒 | 220.7MiB | 29841.8MiB |
| 定制功能定向回归 | 20.55秒 | 1309.6MiB | 29080.6MiB |

内存按约0.8秒一次采样整个子进程树RSS累计，可能包含共享页且不能捕捉采样间瞬时峰值；不是系统硬限制。CPU/网络/缓存变化会影响以后耗时，不保证每轮都相同。

## 验证

- Go包SHA、源码包SHA、工具链 `go version go1.26.10 linux/amd64` 验证通过。
- `go test crypto/x509 -count=1`通过；这是Go标准库回归，不冒称已自行构造CVE利用复现。
- controller、relay/helper、relay/channel、setting/billing_setting、service、middleware、pkg/billingexpr中Cline检测、敏感词、Luna覆盖、拼车计费/并发、Codex关联日志及分段结算定向用例全部通过。没有声称全仓测试全部执行。
- 新程序 `--version`通过；`go version -m`确认为1.26.10，116行模块路径/依赖信息与现网原程序一致。
- Go1.26.10的DefaultGODEBUG兼容默认值较旧程序多出 `httpservecontentmaxranges=1`，系工具链根据原go.mod生成；没有手工覆盖。业务源码/依赖不变不代表标准库行为完全相同。
- 从候选ELF实际提取956个内嵌前端文件，与当前生产manifest逐项SHA256一致；保留Luna/看板/快速配置等定制资源，不重建前端。
- 编译阶段未做隔离HTTP或部署；后续发布时已补充运行环境TLS与隔离HTTP验证，结果见下方。没有发起真实付费模型测试，采用mock与自然请求观察。

## 制品

- 候选版本：`v0.13.2-carpool-r15.20-go12610-20261022`。
- 本机目录：`E:/tmp/newapi-go12610-20261022/`。
- Linux程序：`new-api`，135217314字节，SHA256 `97e241c4ce36ea36d1c361296beff02e0606e3bbc394971bb231297b1c028e02`。
- 压缩程序：`new-api.gz`，SHA256 `723a7798f6bf958130700b5240263627800a25f23c8047be654489e3f4173acd`。
- 发布候选包：`newapi-r15.20-go12610-20261022.tar.gz`，包含程序、源码归档、构建脚本/回执、模块信息、前端manifest和本说明；不含生产.env、SQL_DSN、API Key或账户数据。
- 只上传程序不会自动启动；未给lan205配置ServerB私钥/密码，产物经当前Windows工作站回收。

## 后续发布与回退边界

1. 再确认线上仍为预计r15.19镜像/程序及配置；若已变化，不使用旧候选直接覆盖。
2. 在ServerB以当前镜像为运行时基线，只替换候选/new-api，复制当前运行Env、挂载、网络、端口、资源/重启策略；不重建前端和数据库。
3. 隔离验证TLS上游、健康、拼车范围日志/聚合、冻结计费、Luna、资源关联日志；受Go新证书校验影响的上游应修复证书链，不能通过全局跳过TLS验证回退安全性。
4. 完成后切换并保留当前r15.19容器，回退只恢复本次前一个程序，不回退r15.18之前的计费能力、不回写旧价格数据库。

上述是编译阶段的发布设计；本次已经按下方流程上线。旧发布脚本不能未经基线核对就直接用于本次发布。

## 2026-10-22 12:42 已上线

| 项目 | 实际值 |
|---|---|
| 版本 | `v0.13.2-carpool-r15.20-go12610-20261022` |
| 镜像标签 | `local/new-api:go12610-20261022` |
| 镜像ID | `sha256:51fc2635fc5e3174dded87178c271b4f98812aab2865b24455c557965d851a62` |
| 新容器ID | `169eed27242a9b65764159506f01179f30e19ad11973236b1d2d9703de45c046` |
| 程序SHA256 | `97e241c4ce36ea36d1c361296beff02e0606e3bbc394971bb231297b1c028e02` |
| 实际运行Go | 从 `/proc/<pid>/exe` 读取确认为 `go1.26.10` |
| 发布目录 | `/opt/carpool/releases/security-go12610-20261022/` |
| 回滚容器 | `new-api-rollback-security-go12610-20261022`，旧r15.19已停止保留 |

发布仅用当前生产镜像作基线替换 `/new-api`，完整复制运行Env、挂载、网络、端口及资源/重启策略；保留CODEX_RELAY_TRACE_ENABLED、聚合开关和计费能力。备份在发布目录 `new-api/`：before.json、candidate-spec.json、previous-program、invariants.json、others.json及回执，敏感快照只保留服务器受限目录。

验证：

1. 候选ELF与实际生产956项前端哈希一致，所有模块信息一致。
2. 在候选镜像运行环境中使用Go1.26.10标准TLS验证OpenAI、DeepSeek、Cline证书链；分别HTTP401/401/200且VerifiedChains非空，未关闭证书校验。401是无凭据请求的预期认证响应，不是TLS失败。
3. 独立SQLite实例中Cline渠道55的256/关闭思考规则保持，渠道56仍16；使用mock，不触发真实模型费用。管理员登录、聚合空集合、现网前端命名空间验证通过，临时容器及卷已清理。
4. 生产运行版本/程序hash、令牌范围日志、聚合接口和拼车health通过；稳定options/schema/pricing、codex设置摘要和其他容器身份/配置前后一致。
5. 用户网络公开 `https://api.1aicode.space/api/status` 返回success=true及新版本；短时观察16条HTTP200、0条HTTP5xx，零重启。短窗不是长期稳定性保证。

实施时修正了部署脚本三项兼容：Debian Python3.11.2没有tar数据过滤参数，改为固定10个已校验的普通文件白名单提取；验证用户名控制在12字符内；旧资源脚本硬编码RPM80已过时，本次读到codex2api实际RPM60，改为只冻结/比对当前设置，不回写80。前两次prepare及一次stage失败均发生在切换前，未影响生产。

回滚只退到相邻r15.19，保持数据库和现有价格，不撤Luna迁移：

```bash
python3 /opt/carpool/releases/security-go12610-20261022/deploy.py verify
python3 /opt/carpool/releases/security-go12610-20261022/deploy.py rollback
```

脚本复用既有受审容器配置/网络/回退守卫，增加本次镜像、二进制、Go版本、stage验收和全局发布锁检查。若之后又有版本发布，不能用旧回滚脚本强行替换；回退r15.19会重新引入本次未修复Go版本，仅用于故障应急。

这里的 `verify` 比较的是本次软件切换期间的冻结基线，不是长期全站健康检查。后续 CPAMP 发布、拼车模型倍数调整等已授权变更可能使旧基线比较失败；应先核对对应变更记录与实际差异，不回写旧配置或绕过守卫。回退前也必须核对当前容器所有权及关联服务状态。

## 相邻代理服务安全核查

见[CPA/codex2api只读核查](proxy-security-review.md)：安全核查时 codex2api 两实例为 Go1.26.9，CPAMP 为 Go1.24.13；同一 Go 漏洞另有 Go1.27.3 修复分支。New API 12:42 发布没有升级这些服务。用户随后单独授权，CPAMP 已于 14:25 发布 `v1.14.8-security.20261022` / Go1.26.10，见[独立发布记录](../../../CPA-Manager-Plus/docs/security-20261022/README.md)。codex2api 两实例在本轮后续操作中仍未更新；已退役的 CPA API 容器保持退役。
