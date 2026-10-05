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
