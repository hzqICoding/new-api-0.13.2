# CPA / codex2api 同类 Go 安全更新核查

## 变更记录

| 日期 | 版本 | 变更说明 |
|---|---|---|
| 2026-10-22 | read-only-review-1 | 实测 ServerB 三个现网程序的 Go build info、版本及哈希；下载并在内存解析三个最新官方 Linux amd64 发行资产；核对 Go 官方公告及 CPAMP #1049。未安装、升级、重启或修改服务器。 |

## 结论

**后续状态更新（2026-10-22 14:25）：** 用户另行授权后，CPA Manager Plus已从固定安全merge6f09b197构建并部署为v1.14.8-security.20261022/Go1.26.10，包含前端依赖更新，原数据/密钥保留；见[CPAMP发布记录](../../../CPA-Manager-Plus/docs/security-20261022/README.md)。下表及“尚未实施”建议保留为本轮发布前只读核查快照。codex2api两实例未在该后续操作中更新。

这是 **Go 标准库 `crypto/x509` 的同一个证书信任问题**：`CVE-2026-111943`，官方 Go 编号 **GO-2026-6809**。不是 New API 独有的鉴权漏洞，也不能把 codex2api 的首次初始化修复与它当成同一漏洞。官方修复边界是 **Go 1.26.10 / Go 1.27.3**；Go 1.26.9、1.26.4、1.24.13 均在公告列出的受影响范围内。[官方 advisory](https://vuln.go.dev/ID/GO-2026-6809.json)、[可读公告](https://pkg.go.dev/vuln/GO-2026-6809)、[Go 发布记录](https://go.dev/doc/devel/release#go1.26.10)。

| 对象 | ServerB 当前实际状态 | 已核实的最新正式发行物 | 同一 CVE 的更新结论 |
|---|---|---|---|
| CPA API / Pro / CC-Ant | `docker ps -a` 无 `cli-proxy-*` 容器；历史退役记录仍有效 | CLIProxyAPI `v8.0.38`，2026-10-21 发布；Linux amd64 程序为 **go1.26.4** | 现网没有这几个 API 容器，无需为本次任务恢复或升级。最新版下载资产仍未覆盖同一 Go 修复。 |
| codex2api / 9680 | `/health`：`3.1.0-resource.20261021`；实际程序 **go1.26.9**；独立响应缓存补丁 | 官方 `v3.1.1`，2026-10-16 发布；Linux amd64 程序 **go1.26.9** | 现网携带未修复标准库。直接换官方最新版不能修复同一 CVE，还会丢失当前响应缓存隔离补丁。 |
| codex2api-plus / 9690 | `/health`：**`v3.1.1`**；实际程序 **go1.26.9**；程序 SHA256 与官方 v3.1.1 Linux amd64 资产逐字节一致 | 同上 | 已是最新正式应用版本，但工具链仍未修复。无需仅为了版本号重复安装 v3.1.1。 |
| CPA Manager Plus / 18317 | `healthy`；记录版本 `1.12.1`、运行镜像 digest 与记录一致；实际程序 **go1.24.13** | 正式最新 `v1.14.8`，2026-10-21 发布；Linux amd64 程序 **go1.24.13** | 正式最新版仍未覆盖同一 Go 修复。但今天已有 #1049 安全更新合并到 **dev**，尚未进入 main / 最新正式资产。 |

三个正在运行的程序均包含 `crypto/x509.(*Certificate).Verify` 符号。上述“受影响”是标准库版本和相关代码存在的证据，**不是已经被利用的证明**。官方说明多数平台会使用系统根信任库，部分场景系统信任状态不随更新生效；本次补丁在 `Certificate.Verify` 加入已失信中间证书拒绝列表。实际攻击成立还取决于目标 TLS 证书链、信任库、连接路径及是否调用验证；本轮没有做攻击复现或全站漏洞审计。[官方 advisory 详情](https://vuln.go.dev/ID/GO-2026-6809.json)。

## 上游更新证据

### CPA / CLIProxyAPI

- 仓库是 [router-for-me/CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI)，最新正式发行 [v8.0.38](https://github.com/router-for-me/CLIProxyAPI/releases/tag/v8.0.38)。
- 实际下载 [CLIProxyAPI_8.0.38_linux_amd64.tar.gz](https://github.com/router-for-me/CLIProxyAPI/releases/download/v8.0.38/CLIProxyAPI_8.0.38_linux_amd64.tar.gz)，内存解包、读取 Go build info，结果 `go1.26.4`。下载 SHA256 与 GitHub Release API 的 `asset.digest` 一致。
- `main/go.mod` 为 `go 1.26.0`，Dockerfile 使用浮动 `golang:1.26-bookworm`。**浮动构建镜像不等于既有发布资产或现网程序已经使用 1.26.10**。本次没有拉取、分析最新 Docker 镜像，也不将其认定为已修复。[go.mod](https://github.com/router-for-me/CLIProxyAPI/blob/main/go.mod)、[Dockerfile](https://github.com/router-for-me/CLIProxyAPI/blob/main/Dockerfile)。
- 本次读取的最新正式 release 未明确声明同一 CVE 已修复；公开 Security Advisories API 返回空列表。后者仅是本次查询结果，不能证明未来或非公开通报不存在。

### codex2api

- 仓库 [james-6-23/codex2api](https://github.com/james-6-23/codex2api)，最新正式发行 [v3.1.1](https://github.com/james-6-23/codex2api/releases/tag/v3.1.1)。
- 最新 Linux amd64 资产仍是 `go1.26.9`，提交 `5ee6c29273773a2398531d061d23f199ca8c25f7`。ServerB Plus 实际程序 SHA256 与该官方资产中的程序一致，因此应以实测 `v3.1.1` 更新对其版本的认识，而不是使用旧镜像标签或旧文档版本。
- `main/go.mod` 和 Docker Go builder 都仍明确固定 `1.26.9`。[go.mod](https://github.com/james-6-23/codex2api/blob/main/go.mod)、[Dockerfile](https://github.com/james-6-23/codex2api/blob/main/Dockerfile)。
- `v3.1.1` 确实包含另一项安全修复：首次 bootstrap 必须提供一次性令牌，解决私网地址/转发头条件下的初始化抢占。这项修复影响未设置管理密钥且未初始化的部署；它与本次 x509 中间 CA 信任问题不同，不能据此判定 Go CVE 已解决。发行说明明确说已经初始化的部署不受该初始化流程变化影响。[v3.1.1 说明](https://github.com/james-6-23/codex2api/releases/tag/v3.1.1)。

### CPA Manager Plus

- 仓库 URL 规范化为 [seakee/CPA-Manager-Plus](https://github.com/seakee/CPA-Manager-Plus)，正式最新 [v1.14.8](https://github.com/seakee/CPA-Manager-Plus/releases/tag/v1.14.8)。实际下载资产为 `go1.24.13`，不是 Go 1.26.10。
- [PR #1049](https://github.com/seakee/CPA-Manager-Plus/pull/1049) `fix(security): patch Go and Node dependencies and enforce audits` 于 **2026-10-22 00:54:21 UTC / 北京时间 08:54:21** 合并到 **dev**，merge commit `6f09b197ad35bbefffae6dc218af0ffc26c8de2e`。
- PR 将 `apps/manager-server/go.mod` 从 Go 1.24.0 改为 **1.26.10**，Docker Go builder 也固定 1.26.10，CI 从固定 1.24.x 改为跟随 go.mod；另含 Node / npm / x/sys 升级与安全扫描门禁。因此它包含本次 Go 修复，但不只是一个版本号修改。[PR 文件差异](https://github.com/seakee/CPA-Manager-Plus/pull/1049/files)。
- 实时读取：`main` 的 go.mod 仍为 1.24.0、Docker builder 1.24、release CI 1.24.x；`dev` 已是 1.26.10。**合并到 dev 不等于正式发行或当前 `latest` 镜像已经修复。** 本次未读取 dev Docker 镜像内部程序。[main/go.mod](https://github.com/seakee/CPA-Manager-Plus/blob/main/apps/manager-server/go.mod)、[dev/go.mod](https://github.com/seakee/CPA-Manager-Plus/blob/dev/apps/manager-server/go.mod)。

## 建议目标及边界

1. **codex2api 9680：** 若要同步补齐同一 CVE，最小目标是使用 **Go 1.26.10** 重编当前 `3.1.0-resource.20261021` 定制源码，保留独立响应 Redis、只读程序挂载、on_demand和本轮读回的RPM60（旧记录80已过时）与现有前端资源；验证后单独安排发布。不要执行旧的官方镜像一键升级流程来覆盖定制程序。
2. **codex2api-plus 9690：** 以当前官方 v3.1.1 源码使用 Go 1.26.10 重编，或等待后续正式发行并检查实际程序 build info。仅重复安装当前 v3.1.1 不能解决。
3. **Manager Plus：** 优先等待包含 #1049 的正式稳定版本，并在升级前核对产物 Go build info。若需立即处理，应单独评估当前版本的 Go 1.26.10 安全重编/回补，以及完整 dev 依赖升级的影响；本轮没有将 dev 分支当作可直接替换生产的正式版本。
4. **已退役 CPA API：** 保持删除状态。以后重新引入时，要求所选产物实测为 Go 1.26.10 或 1.27.3 及后续修复版本；不能用 v8.0.38 的版本号替代工具链证据。

只安装系统 Go、升级容器基础系统或改 go.mod 而不重编替换运行程序，不会把已有 Go 程序内部的标准库换成修复版本。以上是后续目标建议，本轮未实施这些变化。

## 实测证据

### 现网程序

主机：SSH `serverB` → `root@108.62.160.72:52320`；实测 hostname `VM-aa817359-d881-4ea7-9723-1e8ca9a2c172`。按容器 `.State.Pid` 只读打开 `/proc/<pid>/exe`，解析 Go build info，并只输出版本、选定构建字段、SHA256 及相关符号存在性；该远程取证过程没有输出容器环境变量、账号凭据、业务正文或服务配置。

| 容器 | `/proc` 实际程序 | Go | 程序 SHA256 |
|---|---|---|---|
| codex2api | `/usr/local/bin/codex2api` | go1.26.9 | `42b3d632168658ed01d9f4f8d830020c4a3fa3625f074845ae76f853799b86aa` |
| codex2api-plus | `/usr/local/bin/codex2api` | go1.26.9 | `d432edf8da586f94f37f733d74a5ecc4616fa373785b3941fd95c3eea2d049aa` |
| cpa-manager-plus | `/usr/local/bin/cpa-manager-plus` | go1.24.13 | `7add0d7a3d0f11a8c4323616eea65fd7d8f18102378d975c581e4cf2a0d8d3a2` |

codex2api 当前只读程序 bind 来源：`/opt/carpool/releases/resource-guard-20261021/codex2api/codex2api`，源码构建 revision `e8d3a525bb2a1f496694d2f2b43c5d31f31f1e86`，`vcs.modified=true`。因此不能从容器镜像标签推断程序版本。

Manager Plus 记录版本 1.12.1 的运行镜像 digest：`sha256:9e01830bb2dae48ed85111e5a95520b0a1b9e65055c56a732f035fe8dadea244`；本次未通过管理认证接口读取其应用版本，1.12.1 是与镜像 digest 对齐的部署记录，Go 版本则来自当前运行二进制。

### 最新官方资产（只下载到客户端内存，不执行、不落盘）

| 官方资产 | 下载归档 SHA256（与 Release API digest 一致） | ELF 程序 SHA256 | 实际 Go |
|---|---|---|---|
| `codex2api_3.1.1_linux_amd64.tar.gz` | `f9e139671eb0855be4d455d3d640926ada9daeb43ef5964050209ca63092cf97` | `d432edf8da586f94f37f733d74a5ecc4616fa373785b3941fd95c3eea2d049aa` | go1.26.9 |
| `CLIProxyAPI_8.0.38_linux_amd64.tar.gz` | `05690c53245a149230a8b7d64085ed96036275a72414d0097f88bb6314acb39b` | `6f82ae9900708679560aeb6a83902bac8f88dc22e5d834a260fd43deee75a917` | go1.26.4 |
| `cpa-manager-plus_v1.14.8_linux_amd64.tar.gz` | `ac71ab9b2d1bdb8376d7ee0f3a958ef140168bef10282fbc33b131a36c5cc5e8` | `a186e28623b2afa7ace813e18c540f851c4fb9e8bb40376d23ed27640e5c47ff` | go1.24.13 |

资产原始元数据：[codex2api API](https://api.github.com/repos/james-6-23/codex2api/releases/latest)、[CLIProxyAPI API](https://api.github.com/repos/router-for-me/CLIProxyAPI/releases/latest)、[CPAMP API](https://api.github.com/repos/seakee/CPA-Manager-Plus/releases/latest)。`latest` 会变化，以上为 2026-10-22 本轮核查快照，不将未来变化写成已验证事实。

## 会话登记与执行范围

- 任务 canonical：`/root/proxy_security_updates`，职责：独立只读核查 CPA / codex2api 上游与现网安全版本。
- 客户端环境观测 `CODEX_SESSION_ID=01a0c79b-ddb9-74f3-bf1d-d3b590062af4`、`CODEX_THREAD_ID=01a161d9-2a2e-72d3-b270-2eb09ffaa9b3`。这是工具进程继承环境中的 ID，未独立验证为该子代理专属 thread，不用于承诺跨客户端恢复。
- 仅生成本报告；服务器只执行容器元数据、程序读取、健康接口等只读动作；未安装、重建、重启、更新配置、发模型调用或测试漏洞利用。
- 本报告与主代理负责的 New API r15.20-go12610 发布记录分开，不能据此断言其他服务已升级。
