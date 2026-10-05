# New API classic：Codex 快速配置

当前维护入口：[定制与生产索引](../README.md)。当前为r15.13，下面依时间保留r15.10–r15.13的实现/发布事实；“发布结果与回滚”首次表格是r15.10历史记录，不能直接当作当前回滚点。

2026-10-05用户已认可本轮交付并要求文档归档；没有逐项收到四系统实机、FULL检查或真实CLI调用回执，因此这些不标为全量验收通过。原始回执已整理到[版本化验证索引](verification/README.md)，上一级未带版本的3份JSON保留为r15.10证据。

源码提交：首次功能`3351c13d`、明确原因`eae5de43`、两步UI`906abe5f`、CF/直连选择`0204d67d`；它们是增量提交，不包含全部历史生产定制。Luna等历史功能的继承和维护见[定价复核](../luna-pricing-20260923/README.md#2026-10-05-只读复核)。

## 变更记录

| 日期 | 版本 | 变更说明 |
|---|---|---|
| 2026-10-05 | r15.13 · 已发布 | 快速配置新增CF/直连线路选择，CF为默认，直连为api.dm.1aicode.space/v1。预览、手动配置及四系统ZIP的TOML/检查设置/说明全部跟随选择。两入口状态及匿名鉴权、公网8项资源哈希通过。 |
| 2026-10-05 | r15.12 · 已发布 | 参照拼车项目重做青绿两步向导、四系统卡、配置摘要和独立下载区。CSS已单独内嵌发布，公网8项新旧资源哈希通过；后端业务源码保持，视觉验收待用户刷新确认。 |
| 2026-10-05 | r15.11 · 已发布 | 用户反馈#490快速配置只显示泛化错误。已核实旧日卡分组授权随订阅到期失效，保持真实权限拦截；前端显示具体原因及处理方法。新旧公网资源及服务检查通过，实际页面待用户确认。 |
| 2026-10-05 | r15.10 · 已发布 | classic 密钥页接入已有令牌/创建成功后的 Codex 快速配置，四系统ZIP与本机检查；补令牌专属只读元数据和创建ID。保留原生产定制，未改计费/额度/数据库结构。真实浏览器及用户电脑验收待反馈。 |

## 已上线的行为

1. classic 密钥列表的启用令牌新增「Codex 快速配置」。下载不新建、不轮换Key。
2. 单个创建成功自动引导；批量创建只收集本批响应中的实际ID，由用户选择，不按名称或列表顺序猜测。
3. 支持 Windows、macOS、Linux、WSL，选择当前令牌真正可选的模型和管理员配置的HTTPS接入地址。
4. 浏览器即时鉴权读取Key并生成ZIP；包含脚本、config.toml、auth.json、哈希清单和说明。保留按文件手动下载。
5. 脚本复用拼车平台的备份/恢复/权限/锁/journal与本机检查。用户本机输入FULL才联网，可能产生少量CLI测试用量；默认仅本地检查，不自动安装客户端。

本轮只接 classic。default 前端资产原样保留，未增加它的配置向导。

## 接口与授权

### 新建令牌响应的兼容扩展

`POST /api/token/` 保留原 `success/message`，增加 `data` 为刚创建的脱敏Token对象，包含 `id/name/status/key` 等；`key` 是掩码，不回传明文。旧前端忽略新增字段仍可正常创建。新前端若连接旧后端无 `data.id`，不猜测、不误选其他令牌。

### GET /api/token/:id/codex-config

原有 `UserAuth` + `DisableCache`，只允许本人令牌；不返回Key/上游渠道凭证。非法ID为400，非本人或不存在为404，未登录401。合法但不可配置返回 `success:true`、`data.configurable:false` 和原因。

```json
{
  "success": true,
  "data": {
    "client": "codex", "schema_version": 1, "template_version": "newapi-codex-v1",
    "token_id": 123, "configurable": true, "reason_code": null, "reason": null,
    "base_url": "https://gateway.example.com/v1", "default_model": "gpt-5.6-sol",
    "models": [{"id":"gpt-5.6-sol","reasoning_efforts":["low"],"default_reasoning_effort":"low"}],
    "supported_targets": ["windows","macos","linux","wsl"], "notices": []
  }
}
```

- 模型为：当前令牌有效分组的启用能力 ∩ Token模型限制 ∩ 模板登记；并沿用现有模型是否配置计费的可见性条件。只读这些条件，不改价格、分组倍率或配额。
- 显式组必须仍被用户允许且未废弃；auto仅合并用户有权使用的auto组，不拿 `/api/user/models` 的全组并集冒充当前Key范围。
- 禁用/到期/额度耗尽的Key、禁用用户、失效分组均阻止导出；IP限制及用户余额信息以提醒呈现，实际网络与计费资格由调用端验证。
- 模板登记：gpt-5.6-sol、gpt-6-sol、gpt-6-astra、gpt-6-luna、gpt-6.1-sol，low推理。默认只优先gpt-5.6-sol，缺失时不静默改模型，要求选择。
- 地址来自ServerAddress，正规化一次 `/v1`；仅HTTPS、合法ASCII域名/IPv6/端口/普通子路径，无userinfo/query/fragment/控制符/百分号编码。空值或出厂localhost默认值，前端只回落当前HTTPS origin，不提供任意host输入框。
- 配置包导出时再次GET元数据并即时POST原 `/api/token/:id/key`，归一一次 `sk-`。不使用旧明文缓存，不将Key存状态/localStorage/URL/日志/Query缓存。
- 每个异步边界和最终下载前检查Token、generation及身份；reveal返回后、ZIP生成期间若切用户，即使React状态未更新也同步停止下载。已下载文件不能远程收回。

## 代码与生产资源策略

- 后端：`controller/token_codex_config.go`、`model/codex_setup.go`，以及 token.go 创建响应、api-router.go 一条路由。无DB迁移，查询使用GORM兼容各数据库；本轮运行的DB测试是SQLite，不冒充PostgreSQL集成验证。
- 源码前端：`web/classic/src/components/table/tokens/`、`src/lib/codex-setup/` 和对应hook。独立Host/Modal可正常整页构建，脚本不依赖TokensPage上下文。
- 生产以当前 r15.9 的586文件Go归档、474文件前端归档为基线。原474前端文件只改 classic index.html，另外473项哈希逐项保持；旧地址继续可用。
- 增加 `/assets/codex-setup-20261005/` 命名空间，镜像旧模块图，7处精确单次锚点增加行按钮、创建实际ID收集与引导、编辑/禁用/删除失效事件。新addon共享生产React/ReactDOM/API/i18n单例，不启动第二套应用；用同步bootstrap先建立入口stub，再按点击懒加载。
- 本地classic源码完整构建用于验证，**没有拿它直接覆盖现网定制前端**。将来全量升级仍需合并原Luna等定制。本轮生产增量步骤均保存为可重复脚本，锚点/基线不匹配即失败，不静默盲改。
- 同步bootstrap、身份末端门禁是主代理与复用子会话窄审后的收口项。所有原new-api/QuantumNous标识与许可证保留。

构建目录：`E:/tmp/newapi-codex-setup-20261005/build`。源码589项；两主题内嵌资源594项。运行程序SHA-256：`08fdbb5c80ee8b3fc6c373803721d3584895268901e2e3109c0f0d2e92fa6346`。

## 验证

- controller定向测试通过：Token归属、有效分组/auto/模型交集、disabled/expired/exhausted、IP提醒、URL校验、新建精确ID及脱敏、既有Key接口及Cline/敏感词回归。model/router通过编译；该定向正则在这两个包没有匹配测试，不记为新增覆盖。
- `npx --yes bun@1.3.9 run build`（classic）通过；依赖只新增固定fflate0.8.2。保留现有Browserslist过旧、Lottie eval和大包警告，未借机升级其他依赖。
- JS/ZIP/JSX共12组通过，涵盖身份在ZIP生成期间切换的回归；PowerShell5.1/7及Git Bash各14个URL谓词验证通过。只用假Key，无真实CLI调用。
- 生产JS定向语义检查：行按钮绑定真实ID、禁用Key、批量实际ID、部分成功、兼容旧后端、Luna字段保留通过；新入口和按需模块VM解析/链接通过。
- 新首页静态闭包69文件、新向导闭包8模块，缺失依赖均0。全目录扫描可见旧Mermaid两项动态依赖缺失，原基线同样存在，本轮不扩展修复，也不宣称全目录零缺陷。
- 发布前一次性SQLite容器验证启动、单个创建返回掩码和确切ID、无模型时拒绝宣称可配置、匿名鉴权和新静态资源；仅临时测试数据，未建真实渠道、未请求上游；测试容器和匿名卷随后删除。
- 线上版本/可执行文件哈希/拼车token范围日志API/拼车health通过。options摘要和其他容器ID/启动时间均不变；Luna倍率覆盖仍为1。
- 公网 `https://api.1aicode.space` 已返回r15.10/classic，新首页指向新命名空间；主包、bootstrap、addon与2个分包下载哈希全部匹配，匿名 `/api/token/1/codex-config` 返回401。

**未做**：真实用户浏览器操作、用户电脑安装、真实付费CLI调用、原生macOS/Linux/WSL实机。迁移不扩大此前脚本测试的覆盖声明。Windows原安装/恢复逻辑已在拼车项目做13项回归，本轮只对改变的URL等适配做定向验证。

## 发布结果与回滚

| 项 | 值 |
|---|---|
| 版本 | `v0.13.2-carpool-r15.10-codex-setup-20261005` |
| 新镜像 | `hzqicoding/new-api:carpool-r15.10-codex-setup-20261005` |
| 新镜像ID | `sha256:71d2e997c6bf6c8bbd5983ddbf9041d01e298712afb15aa27688a448964551e7` |
| 旧镜像ID | `sha256:24d3bf6f7e1ee9df6654ff52fa3c358a3d19d4ac842cdf5ec84536dc530a3e9b` |
| 发布目录 | `serverB:/opt/carpool/releases/newapi-codex-setup-20261005/` |
| 回滚容器 | `new-api-rollback-newapi-codex-setup-20261005`，停止保留 |

```powershell
ssh -o BatchMode=yes -J serverA serverB "python3 /opt/carpool/releases/newapi-codex-setup-20261005/deploy.py verify"
# 后续如已有新发布，先核对基线再回退：
ssh -o BatchMode=yes -J serverA serverB "python3 /opt/carpool/releases/newapi-codex-setup-20261005/deploy.py rollback"
```

服务器保留原容器完整受限配置、源码/资源清单、程序与镜像、启动定向检查及最终回执。凭证只在服务器使用，没有回传或写进本文。

本地开始时已有大量未提交的拼车、Luna、敏感词和Cline修改。它们与生产Go归档逐文件核对一致；未reset/clean、未混入本轮提交。部署可复现依据是生产归档+本轮指定文件+SHA清单，不把某个Git提交冒称为包含全部历史定制的干净基线。

本轮代码提交 `3351c13d`，router和会话登记仅暂存本轮新增行，历史未提交修改仍保留。服务器另留 `feature-source.tar.gz`（向导/脚本/构建与发布源码）配合完整589项Go源码归档及内嵌资源，便于接续。

## 用户验收

1. 刷新当前classic密钥管理页，选择有效Key，点击「Codex 快速配置」。
2. 核对Key ID、模型和HTTPS地址，选择实际运行Codex的系统，下载ZIP并完整解压。
3. Windows双击setup.cmd；macOS运行setup.command；Linux/WSL执行bash setup.sh。已有配置先备份确认；复杂MCP/profiles可选手动合并。
4. 回车仅本地检查，FULL才检查渠道及可选极短CLI调用；可能产生少量用量，结果在本机显示并保存脱敏报告。
5. 验证单个创建后引导、批量选择实际新ID、禁用/到期Key不可导出、切用户/关闭后不继续下载。

网站回滚不能收回已下载ZIP或恢复用户电脑；本机恢复使用脚本的Restore选项。保持模板/脚本版本配套，不把API Token传入 URL 或脚本命令参数。

## #490 提示不明确修复（2026-10-05）

### 实际原因

只读查询确认#490启用、永不过期、无限额度；`remain_quota=-588`不影响无限额度模式。ServerAddress合法。令牌指定`GPT-daycard`，所有者当前用户组为`default`；订阅43状态`expired`、结束于2026-08-05，升级组为`GPT-daycard`、原组为`default`。全局可用分组与default特殊分组均未授权日卡组。因此它在现有`TokenAuth`中也会被拒绝，并非下载向导新增了错误权限限制；最后成功计费记录为2026-08-04。

前端的问题是`validateMetadata`抛错后，初始加载和导出catch都把服务端提供的原因替换成笼统提示，用户无法区分分组、Key状态、模型和地址问题。

### 改动

- 后端保持分组判断顺序及权限条件，拆分`group_forbidden`与`group_retired`，不改变请求路由、用户分组或订阅。
- 前端使用受控错误码映射本地中文提示，兼容旧`group_unavailable`；初次读取与下载前再次读取均保留具体原因。网络、HTTP、配置结构和地址错误分别提示，服务器原始文本、Axios对象、Key不会进入提示。
- 此Key仍需用户在编辑令牌中选择当前可用分组（例如`default`），或恢复适用订阅；本次排查未更改#490、用户、订阅、渠道或额度。
- 复用`/root/codex_setup_scripts`处理3个前端文件与定向回归，主会话负责只读诊断、后端区分、整合及发布；无新建子会话。

### 验证与制品

Go定向回归通过，新增模拟“用户订阅组→default回退、无限额度负数、选择可用分组恢复配置、已弃用分组”案例。前端15组验证及14个Bash URL谓词通过，无真实Key/渠道/CLI调用。源文件和生产增量构建结果以最终发布回执为准。

修复从本地已核验的r15.10完整589项源码和594项资源重建，仅覆盖2个后端文件、添加`codex-error-fix-20261005`资源命名空间及切换首页；保持之前593项非首页资源原哈希，包括r15.10的旧URL。构建脚本`prepare_error_fix.py`、`build-addon.mjs`，部署入口`deploy_error_fix.py`。构建目录`E:/tmp/newapi-codex-error-fix-20261005`，基线固定r15.10镜像`71d2e997…551e7`；未直接覆盖生产定制前端。

已发布`v0.13.2-carpool-r15.11-codex-error-fix-20261005`。镜像`hzqicoding/new-api:carpool-r15.11-codex-error-fix-20261005`，ID `sha256:57967c259db06950d9e9668177b889f93e63968d4185278d6e1e821993bd9815`；程序SHA-256 `3f6ed55cca67a167781d2172fc9be59f50f418afb867e5aab9f39a46b7170694`。

验证结果：classic完整Vite构建通过（保留原Browserslist、Lottie eval、大包提示）；生产入口与ZIP模块静态链接9模块通过，行按钮/精确创建ID/失效事件/Luna保留的定向检查通过。服务器内嵌714项资源清单全匹配，临时启动/创建脱敏ID/无模型元数据/匿名鉴权检查通过并已清理临时容器与卷。切换后版本、程序哈希、拼车日志接口/health、6个新静态资源通过；options摘要及其他容器ID/启动时间不变。公网版本及7个新旧资源哈希通过，包括r15.10旧入口URL。

发布目录`serverB:/opt/carpool/releases/newapi-codex-error-fix-20261005/`，保留源码/前端归档与清单、修复源文件、验证回执；回滚容器`new-api-rollback-newapi-codex-error-fix-20261005`（r15.10）停止保留。确认没有后续发布后可执行：

```powershell
ssh -o BatchMode=yes -J serverA serverB "python3 /opt/carpool/releases/newapi-codex-error-fix-20261005/deploy_error_fix.py verify"
ssh -o BatchMode=yes -J serverA serverB "python3 /opt/carpool/releases/newapi-codex-error-fix-20261005/deploy_error_fix.py rollback"
```

用户验收：Ctrl+F5刷新密钥页，#490应显示明确的分组权限提示；选择当前有权使用的令牌，或自行编辑#490为可用分组后再打开，确认可以进入系统/模型选择与下载。实际浏览器、用户电脑和付费CLI调用仍由用户验收；本轮未执行真实模型调用，也未更改用户业务数据。

## 快速配置界面优化（r15.12）

用户反馈#706向导视觉效果与已认可的拼车端存在明显差距。参照拼车端实际`frontend/src/components/console/CodexSetupModal.tsx`和原H5设计，采用两步向导：第一步选择系统/模型并查看接入地址、配置目录及可选CLI测试；第二步展示配置包、目标系统执行方法、下载与手动配置。顶栏图标及Key ID摘要、横向步骤、Windows/macOS/Linux/WSL四系统卡和统一底部操作使用青绿与轻边框样式。第三阶段明确为用户电脑上的检查，网页不显示伪造的本机检测通过状态。

保留现有令牌归属、到期清理、关闭/改选/换用户失效、即时重新验证和读取Key、最终下载前身份检查及r15.11具体错误提示；不改变后端、模型、脚本模板、计费或生产业务数据。Semi Modal继续承接焦点和Esc关闭；新增CSS严格作用于`.newapi-codex-setup`，避免影响其他模态框和站点主题。

生产构建不能仅用旧addon的空CSS loader，否则会再次丢失定制样式。`build-addon.mjs`仅对向导CSS显式产出`codex-setup-entry.css`，其他Semi样式继续复用生产资产；`prepare_ui.py`在`codex-ui-20261005`命名空间放入新资源并显式写入首页stylesheet链接。`deploy_ui.py`在原启动/资源验证基础上额外校验CSS链接与字节哈希。

发布基线：r15.11镜像`57967c25…9815`、589项后端源码、714项内嵌前端资源。本轮后端589项源码原哈希全部保持（仅内嵌资源与版本重新编译），之前713项非首页资源保持原哈希；原命名空间与Luna价格定制继续保留。新增后资源共835项，构建目录`E:/tmp/newapi-codex-ui-20261005`。

已发布`v0.13.2-carpool-r15.12-codex-ui-20261005`，镜像`hzqicoding/new-api:carpool-r15.12-codex-ui-20261005`，ID `sha256:60de5cd305bb1114f5128059701693a629442401b7d915869d1f3837a3deb71a`；程序SHA-256 `99caefed7e28da2a44d72c789fb3f81eccd69b68388171601d4cde875f46f1a1`。独立CSS SHA-256 `87722f73e5e323985c0fc6521e33b3aad60ef003f7fd6d1e86183779eb6954ec`。

验证：最终15组JS/四系统ZIP/身份失效回归、14个Bash URL谓词、classic完整Vite构建通过；保留原Browserslist/Lottie eval/大包警告，未升级依赖。生产JS静态链接9模块、原行按钮/精确创建ID/失效事件/Luna保留检查通过。CSS解析检查180个选择器均有独立作用域。服务器835项内嵌资源全匹配，临时启动/创建精确脱敏ID/元数据鉴权与7项静态资源（含CSS链接）通过，临时容器及卷已清理；切换后版本/程序哈希/拼车日志API/health通过，options摘要和其他容器ID/启动时间不变。公网8项新旧资源哈希匹配，首页已引用新CSS，旧r15.11 addon URL仍可用。

生产目录`serverB:/opt/carpool/releases/newapi-codex-ui-20261005/`保存程序、835项前端归档、589项Go源码归档、UI源码/构建发布脚本、清单与验证回执。回滚容器`new-api-rollback-newapi-codex-ui-20261005`（r15.11）停止保留。确认没有后续发布后可执行：

```powershell
ssh -o BatchMode=yes -J serverA serverB "python3 /opt/carpool/releases/newapi-codex-ui-20261005/deploy_ui.py verify"
ssh -o BatchMode=yes -J serverA serverB "python3 /opt/carpool/releases/newapi-codex-ui-20261005/deploy_ui.py rollback"
```

用户Ctrl+F5刷新后验收：系统卡选择、模型与地址复制、下一步/上一步、下载ZIP及手动文件、关闭/换Key/超时提示，以及窄屏和暗色显示。实际浏览器视觉及交互仍由用户验收；没有自动浏览器测试、真实模型调用或用户电脑配置操作。主代理最终补回原URL的copyable操作，下载模板和脚本文件未变化。

## CF / 直连线路选择（r15.13）

按用户明确指定增加两条线路：默认`https://api.1aicode.space/v1`（CF），可选`https://api.dm.1aicode.space/v1`（直连）。第一步右侧改为“请求线路”下拉及可复制实际地址，第二步显示所选线路以供下载前核对。每次新打开向导仍默认CF；同一向导切系统、模型或返回上一步保留已选线路。

前端以固定线路ID选择已批准入口，仅管理员ServerAddress正规化后为本站CF端点时提供这两个选项。其他站点继续只提供其管理员默认地址，不把其他部署的Key导向本站。保持元数据原始base_url用于下载前复验，所选endpointId单独快照；线路改选取消在途导出。重新获取元数据后，校验原地址/所选线路再取Key。预览、手动config.toml、ZIP内config.toml、manifest.json、settings.tsv及README全部使用同一选择；auth.json不因线路变化而更换Key。检查脚本读取settings.tsv，因此本机HTTPS/模型/CLI检测也跟随所选地址。

本轮主代理直接处理，没有新增或复用子会话。20组JS回归通过，新增4系统×2线路的实际ZIP内容/哈希/手动配置一致性、默认CF、拒绝未知线路及其他部署不提供本站直连的验证；14个Bash URL谓词、classic完整构建、生产JS静态链接通过。未改后端业务源码、脚本模板、数据库、站点options或基础设施。

已发布`v0.13.2-carpool-r15.13-codex-routes-20261005`，镜像`hzqicoding/new-api:carpool-r15.13-codex-routes-20261005`，ID `sha256:7ce5c41556333571245e1bf3d36f788c0a85d4da35e2351d76b359b347aeb736`；程序SHA-256 `43b07db7e4e33696027c6a3dc8f33a97d90c365dc4d3842b3831312dcecefc7e`。589项Go源码与r15.12一致，保留834项旧非首页资源，新内嵌资源共956项；CSS字节保持，命名空间`codex-routes-20261005`。完整构建保留原Browserslist/Lottie eval/大包提示。

服务器内嵌资源清单、临时启动/令牌元数据与7项静态资源、切换后版本/程序哈希/拼车health及日志联通通过；临时容器及卷已清理，options摘要/其他容器ID及启动时间不变。CF与直连公网/api/status均返回r15.13，两入口匿名/v1/models均401；8项新旧JS/CSS哈希匹配。没有真实模型调用，也没有浏览器或用户电脑验收。

本地构建`E:/tmp/newapi-codex-routes-20261005`，命令：`python docs/codex-setup-20261005/prepare_ui.py E:/tmp/newapi-codex-routes-20261005 E:/tmp/newapi-codex-ui-20261005 codex-ui-20261005 codex-routes-20261005`。发布目录`serverB:/opt/carpool/releases/newapi-codex-routes-20261005/`，r15.12回滚容器`new-api-rollback-newapi-codex-routes-20261005`停止保留。

```powershell
ssh -o BatchMode=yes -J serverA serverB "python3 /opt/carpool/releases/newapi-codex-routes-20261005/deploy_routes.py verify"
# 确认没有后续发布后再回滚：
ssh -o BatchMode=yes -J serverA serverB "python3 /opt/carpool/releases/newapi-codex-routes-20261005/deploy_routes.py rollback"
```

用户Ctrl+F5后选择线路下载即可。只读状态探测不代表用户网络或实际CLI调用已验证；已有电脑配置不会被网站自动替换，需用户下载对应线路的配置包并自行运行。
