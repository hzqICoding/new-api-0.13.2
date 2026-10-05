# NewAPI 前端模块加载修复发布

> 此文记录r15.8当时的发布；2026-10-05已核实当前为r15.13。后续Codex发布保留该兼容入口与命名空间策略，当前目录/资源清单见[维护索引](../README.md)。本页旧脚本不可跨版本直接执行。

## 变更记录

| 日期 | 版本 | 变更说明 |
|---|---|---|
| 2026-09-25 | r15.8 | 已发布前端模块兼容入口与独立资源目录，修复看板引用旧主包 404；保留 Luna 计价与敏感词提示，生产配置不变。 |

## 发布结果

- 时间：2026-09-25 15:37:10（北京时间，新容器启动时间）。
- 版本：`v0.13.2-carpool-r15.8-frontend-20260925`。
- 镜像：`hzqicoding/new-api:carpool-r15.8-frontend-20260925`。
- 镜像 ID：`sha256:c16a793e83ebe8cc38f36e50e784807ab352ec2dcfcd105f24a2c2abf89edec5`。
- 二进制 SHA256：`11a57e04b84dce7ea712c5afcf6e1d3ece5a2d836361e17f6b436f3b486b5c0f`。
- 生产发布目录：`serverB:/opt/carpool/releases/frontend-module-fix-20260925/`。
- 回退容器：`new-api-rollback-frontend-module-fix-20260925`，已停止保留。

## 修复方式

旧 Luna 补丁把 `index-D4VkjDT5.js` 改名后删除，但 Dashboard 等分包继续导入旧入口。现保留旧名称为 101 字节的命名导出转发模块，转到同目录 Luna 入口，避免加载第二份应用。

新首页统一使用 `/assets/frontend-fix-20260925/` 下的 JS、CSS 和 preload。完整复制 classic assets 到此目录，并同步 Vite 的 `assets/...` preload 路径；模块相对 import 自然解析到同一目录。新命名空间避免旧主包或旧 404 缓存干扰。原 357 个前端文件除 classic index.html 外保留，新旧路径的兼容入口及复制资源合计 474 项。

新的入口仍叫 `index-luna-0216aa8f9328.js`，但因 preload 路径改变，命名空间内该文件实际 SHA256 是 `e3e785a7a3d2ec1ff6b56677e0000919f30cba1d53b0edcb10a28954ae8b2479`。完整 URL 中的版本目录承担缓存隔离；不能用旧文件名推断其内容哈希。

## 源码与构建

以 r15.7 生产 `newapi-source.tar.gz` 为基线；585 项 Go 文件、go.mod、go.sum 哈希逐项匹配，没有改业务 Go 代码。构建需要的 `i18n/locales/*.yaml` 和 `common/limiter/lua/rate_limit.lua` 从当前本地仓库复制，并随发布包保存。未改动原工作区的未提交拼车和敏感词补丁。

构建隔离目录：`E:/tmp/newapi-frontend-fix-20260925/build`。Go 1.26.4，linux/amd64，CGO_ENABLED=0，trimpath；仅版本 ldflag 和内嵌前端改变。

维护脚本：

- `prepare_assets.py`：对生产归档生成独立资源目录、兼容入口和完整哈希清单。
- `../luna-pricing-20260923/patch-production-frontend.ps1`：已修复删除旧入口的问题，增加调用 `prepare_assets.py` 的发布步骤；以后这两个目录必须一起保留。
- `audit-frontend-deps.mjs` / `audit-vm-link.mjs`：静态依赖与 ESM parse/link 验证。
- `deploy.py`：服务器 prepare、stage、cutover、verify、rollback；复用已验证的容器配置复制流程，未修改生产 options。

## 已完成验证

- Dashboard 静态闭包 9 个模块、8 项 preload 目标及依赖全部存在，命名导出无缺失。
- Pricing 主入口闭包 6 个模块，无缺失；43 处旧入口引用经兼容模块解析。
- 真实产物 ESM parse/link 通过；不执行 DOM，未冒充浏览器视觉验证。
- Luna 价格函数 8 项回归通过。
- 修复后的完整 PowerShell 生产补丁流水线执行通过，输出 474 文件，命名空间主包哈希一致。
- 二进制提取出的全部 474 个前端文件与清单逐项一致。
- 独立 SQLite 容器初始化、登录、`/api/user/self`、数据看板查询和日志接口通过；关键 JS 与首页实际 HTTP 返回正确。
- 线上版本、二进制哈希、拼车 token 范围日志接口和拼车健康检查通过。
- 全部 production options 哈希前后一致；当前返回的 Luna 倍率覆盖项仍为 1，数量见发布回执。
- 其他运行容器 ID/启动时间未改变；观察窗口内正常 relay 200，没有为测试消耗真实上游额度。
- 用户 Windows 公网访问首页、状态、新主包、看板、兼容入口均成功且资源哈希相符；ServerA 独立公网访问也核对通过。

本地 urllib 请求受到公网入口 403 限制；改用 curl 后验证通过。大文件在用户机器上曾下载超时，重试后得到完整文件且哈希匹配，ServerA 下载正常。没有修改 Cloudflare、防火墙或网络配置。没有可用的浏览器会话，因此最终用户登录页面的视觉/交互验收仍需用户刷新确认。

## 已知范围外问题

`_basePickBy-pe694AoF.js` 和 `_baseUniq-j-5v1Cfv.js` 在此前生产归档中已经缺失，只出现在 Mermaid 动态图表依赖链，不属于 Dashboard/Pricing 静态闭包。本次未伪造补齐资源，详细来源核查见 [审计回执](./audit-report.md)。

## 运维与回滚

```powershell
ssh serverB "python3 /opt/carpool/releases/frontend-module-fix-20260925/deploy.py verify"
```

发布目录保存原容器完整受限配置、旧首页、production options 哈希、源文件清单、前端归档、新程序、镜像、stage 和最终验收回执。密钥与数据库连接信息未回传。

只有出现需要回退的问题时执行：

```powershell
ssh serverB "python3 /opt/carpool/releases/frontend-module-fix-20260925/deploy.py rollback"
```

回退切换旧容器，保留现有数据库，不回写价格快照。旧版本本身仍存在此次前端断链问题，回退不是常规刷新修复方法。发布脚本为本次固定版本编写；后续已有新版本或新配置时先重新核对，不能直接套用旧脚本。
