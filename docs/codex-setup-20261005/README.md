# New API classic：Codex 快速配置

## 变更记录

| 日期 | 版本 | 变更说明 |
|---|---|---|
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

## 用户验收

1. 刷新当前classic密钥管理页，选择有效Key，点击「Codex 快速配置」。
2. 核对Key ID、模型和HTTPS地址，选择实际运行Codex的系统，下载ZIP并完整解压。
3. Windows双击setup.cmd；macOS运行setup.command；Linux/WSL执行bash setup.sh。已有配置先备份确认；复杂MCP/profiles可选手动合并。
4. 回车仅本地检查，FULL才检查渠道及可选极短CLI调用；可能产生少量用量，结果在本机显示并保存脱敏报告。
5. 验证单个创建后引导、批量选择实际新ID、禁用/到期Key不可导出、切用户/关闭后不继续下载。

网站回滚不能收回已下载ZIP或恢复用户电脑；本机恢复使用脚本的Restore选项。保持模板/脚本版本配套，不把API Token传入 URL 或脚本命令参数。
