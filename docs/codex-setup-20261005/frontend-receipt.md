# Classic Codex 快速配置前端回执

工作包：`/root/codex_setup_scripts`（复用已有会话）。本轮仅修改授权的 classic tokens 组件、对应 hook、新增 `src/lib/codex-setup/`、定向验证脚本和本回执。未修改依赖/lock、后端、其他文档、生产制品；未提交或部署。原有 new-api / QuantumNous 标识和原文件许可证均保留。

## 交付

- 密钥行内增加 `Codex 快速配置`，使用真实 `record.id`；非启用状态按钮禁用，后端权限和配置元数据始终是最终依据。
- `EditTokenModal` 从每次创建响应的 `data.id` 收集本次新令牌；单个创建直接打开，批量提供 ID 选择，不查列表第一条或按名称猜测。关闭/切编辑会话后不接受迟到的自动引导。老后端若不返回 ID，保留原创建结果，不假猜 ID。
- 独立向导提供 Windows/macOS/Linux/WSL、有效模型下拉、管理员 HTTPS 线路、ZIP、手动 config/auth 双文件、脱敏预览及本机执行/报告指引。模型默认为空时要求手选；没有任意 host 输入框。
- 导出前重新拉取该 ID 的 `/api/token/:id/codex-config`，校验选中模型及线路，再即时 POST `/api/token/:id/key`。不使用 `resolvedTokenKeys` 或其他旧 Key 缓存；`sk-` 前缀只补一次。线路在导出前发生改变则停止，要求刷新。
- Key 只在当前异步调用的局部变量/Blob 中短暂存在，不入 URL、状态、localStorage、日志或 query/mutation cache。GET 禁用请求去重，HTTP 使用 no-store；错误只显示固定文案，避免打印含响应/请求配置的异常对象。
- 关闭、换 Key/系统/模型、换用户、同页撤销/禁用/编辑、180 秒会话超时均中止请求并清理 Blob URL。A→B→A 以 generation 区分，用户身份和 token ID 在每个异步边界再次核对。跨设备撤销以每次即时 metadata/reveal 为最终判断；已下载文件不能远程收回。
- 脚本复用 carpool 上轮版本，保留锁、journal、备份原始存在状态、ACL/模式恢复、幂等、FULL 授权、隔离 doctor、90 秒限制及中文本机报告。仅把模板版本改为 `newapi-codex-v1`、配置 provider 改为 `new-api`，两固定域名改为严格 HTTPS 地址验证。内部 carpool 事务/备份名保留，避免另造并发锁。

## 独立生产接入面

```jsx
import CodexSetupHost from './components/table/tokens/CodexSetupHost';
import { openCodexSetup, invalidateCodexSetup } from './lib/codex-setup/session';

// request 为 axios 兼容实例；t 为现有翻译函数。无需 TokensPage context。
<CodexSetupHost request={API} t={t} userIdentity={currentUserId} />

openCodexSetup({ tokenIds: [actualTokenId], userIdentity: currentUserId });
// 批量使用本次创建响应中获得的 ID，不传 initialTokenId，用户自行选择。
openCodexSetup({ tokenIds: createdIds, userIdentity: currentUserId });
invalidateCodexSetup([changedTokenId]);
```

也可独立挂载 `CodexSetupModal({visible, tokenIds, initialTokenId, userIdentity, onClose, request, t})`。Host 监听 `newapi:codex-setup-open`，事件 detail 只包含上述 IDs / 用户身份字段；失效事件为 `newapi:codex-setup-invalidate`，detail `{tokenIds}`。事件不携带 Key。主代理可以在保留现有生产资源的增量 entry 中挂载 Host，不必重建整页。

生成器：`buildCodexSetupZip({metadata,target,model,rawKey,runCliTest})`，返回 `{filename,blob}`。固定包结构为脚本入口、`payload/config.toml`、`payload/auth.json`、`payload/manifest.json`、`payload/settings.tsv`、`payload/checksums.sha256`、`README.txt`。Windows PS1 有 UTF-8 BOM，两个配置文件无 BOM；POSIX 脚本 LF、ZIP 执行权限元数据。

元数据空 base_url 仅回落到当前 **HTTPS origin** 的 `/v1`。URL 支持合法 ASCII 域名、IPv6、端口、子路径；拒绝 HTTP、userinfo、query、fragment、控制字符、空白、shell/TOML 注入字符、`.` / `..` 路径及非法端口。为保持三端一致，百分号编码路径和非 ASCII 路径不在首版地址子集中；需管理员使用规范 ASCII HTTPS 基址。

## 验证

从 `web/classic` 执行：

```text
node scripts/verify-codex-setup.mjs
powershell.exe -NoProfile -ExecutionPolicy Bypass -File scripts/verify-codex-setup-scripts.ps1
pwsh.exe -NoProfile -File scripts/verify-codex-setup-scripts.ps1
```

- JS **11 组通过**：前缀幂等与拒绝掩码/注入；HTTPS URL 合法和非法案例；精确 token/template/模型交集；TOML/auth 分离；A→B→A 与关闭失效；四系统 ZIP、哈希、模板、执行入口和 BOM；Host 与真实 TokensPage JSX/import graph 编译。
- PowerShell **5.1.26100.9444 / 7.6.6 各 14 个 URL 谓词案例通过**，并解析整个模板验证语法。只执行从固定模板 AST 提取的纯 URL 函数，没有执行配置安装或真实 CLI。
- Git Bash **14 个 URL 谓词案例通过**，另做 `bash -n`；只执行固定 URL 检查段，无网络、CLI、用户配置目录访问。
- 所有 ZIP 测试只用明确假 Key；不联网、不调用真实模型、不重新执行维护者用户目录安装。原生 macOS/Linux/WSL、浏览器功能和 Windows 完整在线 doctor 未在本轮新做实测，上轮脚本覆盖范围不扩张宣称。
- 未全量构建覆盖生产 classic。本轮 JSX 验证使用 esbuild `write:false`，保留生产归档补丁制品的发布策略由主代理处理。

## 给用户的实际验收

1. 已有启用令牌点快速配置，核对所选 ID/模型/线路；下载不新增令牌。
2. 单个与批量创建分别验证自动引导及实际新 IDs 选择；取消向导不影响已创建令牌。
3. 切系统、模型、令牌；导出中关闭/换用户/禁用令牌，迟到结果不得继续下载。
4. 下载 ZIP 与两份手动文件，在真正运行 Codex 的电脑验证；本机 FULL 测试可能产生少量用量，由用户自行选择。

## 2026-10-05 追加：配置阻断原因不再被泛化

本次只修改 `config.js`、`CodexSetupModal.jsx`、`verify-codex-setup.mjs` 和本回执。未修改后端、权限、脚本模板、发布脚本，未提交或部署。

- 修复 `validateMetadata` 丢弃 `reason_code` 及 Modal 两处 catch 再次泛化的问题。首次加载与导出前元数据刷新都使用同一套本地白名单提示，受控 reveal 失败也使用安全错误映射。
- 支持 `token_disabled` / `token_expired` / `token_exhausted` / `user_disabled` / `invalid_server_address` / `group_unavailable` / `group_forbidden` / `group_retired` / `no_compatible_model`。`group_forbidden` 明确显示：**“此令牌指定的分组不在当前账号可用范围内。请编辑令牌选择可用分组，或恢复对应订阅后重试。”** 保留原权限拦截，不根据前端错误自行放宽分组。
- HTTP 401、403、404、429、5xx、网络异常和超时分别显示操作建议；本地模板版本、令牌归属、URL、系统/模型、Key 格式和线路变更错误也保留安全明确提示。
- 显示内容始终来自本地字符串白名单；不转录服务端 `reason` / `message`，不显示或记录原始 Axios error、响应体、异常 message 或 Key。URL 构造器等异常被转换为安全错误；未知 reason_code 使用静态兜底。
- 最新 `node scripts/verify-codex-setup.mjs`：**15 组通过 + 14 个 Bash URL 谓词通过**。新增覆盖已知/未知 reason_code、初次与导出阶段复用、HTTP/网络/超时区分、本地校验信息保留、响应/异常夹带假 Key 不泄漏、修改异常 message 仍不被显示。仅假 Key，无真实网络或模型调用。三个源码/测试文件已按现有 Prettier 格式化，可供主代理构建增量 addon。

## 2026-10-05 追加：参照拼车已认可界面优化

实施前已阅读拼车项目的 `CodexSetupModal.tsx` 与 `output/codex-setup-preview.html`。本轮只修改 new-api 的 `CodexSetupModal.jsx`、新增同目录 `CodexSetupModal.css`、JS 验证器 CSS loader 和本回执；不修改后端、配置生成器、脚本模板或生产发布脚本。

- 改为真正的两步向导：第一步选择环境，第二步下载并运行；横向第三阶段“本机检查”仅表示后续在用户电脑完成，不能点击进入虚假的网页检查结果。
- 第一阶段沿用青绿配色与四系统图标卡，选中态带勾；模型与管理员 HTTPS 地址为双列摘要，下方为实际目录规则提示和可选 CLI 测试卡。批量创建仍只使用真实响应 IDs，要求明确选择。
- 第二阶段包含配置包卡片、系统对应的本地命令、解压/备份替换/查看报告的步骤、下载主操作、手动双文件折叠与脱敏预览，以及明确标注“待本机运行”的检查项目说明。下载提示仅表示下载已发起，不声称已配置或调用通过。
- Header 收拢为图标、标题、令牌 ID badge；Footer 统一为“稍后配置 / 下一步”与“上一步 / 完成，关闭”。错误、超时和加载状态采用轻量卡片，保留上一轮明确的原因提示。
- 桌面宽约 700px；窄屏系统卡 2×2、模型与地址改单列。颜色结合 Semi 主题变量适配暗色。CSS 仅作用于 `.newapi-codex-setup`，明确清除 Semi 默认双层间距；content 限高、body 使用 `min-height:0` 与 flex 收缩滚动，header/footer 不随正文滚动。保留 Semi Modal 的焦点管理与 Esc 关闭，原生按钮/选择器有文字或可访问名称。
- 所有鉴权与保密逻辑保留：双阶段元数据校验、即时 reveal、epoch/generation、外部身份同步门禁、最终 save 门禁、rawKey 清理和 Blob 回收。切步骤、返回上一步、切系统/模型/令牌均清理在途导出；仅第二步能够触发导出。过期、加载、忙碌、校验错误状态不能继续下载。

验证：现有 `node scripts/verify-codex-setup.mjs` **15 组 + 14 个 Bash URL 案例通过**，CSS loader 改为真实 CSS 解析，JSX/import graph 编译通过；Prettier 与 `git diff --check` 通过。未新增镜像式 UI 测试，未做浏览器实测、真实 Key 或模型调用。实际视觉和交互由用户验收；生产 addon 的 CSS 产物与显式链接由主代理负责，本工作包未发布或提交。

交付后主代理接手两项收尾：保留原有 URL `copyable` 便捷操作并局部适配等宽/青绿样式，CLI 标题去掉重复“可选”而保留 badge。核心文件已交由主代理冻结，其最终格式化与构建由主代理完成。
