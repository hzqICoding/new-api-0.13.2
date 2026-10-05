# Luna 定价前端生产资源补丁回执

> 本文是2026-09-23初始回执。删除旧入口导致的看板分包404已在[09-25模块修复](../frontend-module-fix-20260925/README.md)解决；现有patch脚本会保留转发入口并调用该目录的prepare_assets.py。旧357项/旧哈希仅为历史证据，当前基线见[维护索引](../README.md)，不能照旧删除入口。

## 结果

`calculateModelPrice` 在完成选中分组或最低倍率分组判定后，若 `/api/pricing` 记录包含有限数值 `group_ratio_override`，仅用该值覆盖最终计算倍率 `usedGroupRatio`。`usedGroup`、`enable_groups`、可用分组和筛选逻辑保持不变；字段缺失或不是数值时完全沿用原逻辑。

源码变更：`web/classic/src/helpers/utils.jsx`。

## 生产前端保留方案

未从本地 `web/classic` 全量重建。生产定制前端取自：

- 远端归档：`serverB:/opt/carpool/releases/sensitive-message-20260922/production-frontend.tar.gz`
- 输入归档 SHA256：`360D9002DFEC6318A3074165E57BD01EBCD87D1943C39689666CE10AD1920558`
- 输入文件数：357
- 原主包：`web/classic/dist/assets/index-D4VkjDT5.js`
- 原主包 SHA256：`7BC7EAAC4050375465441171C54550262E0984849DA8545EC18E7BF2A916AA40`

补丁脚本 `patch-production-frontend.ps1` 对归档执行以下保护：

1. 校验输入归档 SHA256 和 357 个文件。
2. 从 `index.html` 解析真实主包，不猜测文件名。
3. 要求压缩后的 `calculateModelPrice` 函数片段恰好匹配一次，已存在 `group_ratio_override` 时拒绝重复修改。
4. 只在分组判定结束后插入 `Number.isFinite(e.group_ratio_override)&&(u=e.group_ratio_override)`。
5. 按补丁后内容生成新主包名并更新 `index.html`，避免旧 hash URL 的浏览器缓存。
6. 逐一核对除 `index.html` 和新旧主包外的文件 SHA256 均未变化。
7. 对主包运行 `node --check`，重新打包、重新解包并再次检查文件数和函数片段。

生成物：

- 补丁归档：`E:\tmp\luna-pricing-20260923\production-frontend-luna-group-ratio.tar.gz`
- 补丁归档 SHA256：`7CFC5802A4A552E4181FF7CFDC99705421B3F3583F37687F46073F3DBA699613`
- 解包目录：`E:\tmp\luna-pricing-20260923\patched-dist\web\classic\dist`
- 新主包：`assets/index-luna-0216aa8f9328.js`
- 新主包 SHA256：`0216AA8F93289ED8BA878BB67B83411A7C511A3C51DB8F68657A967332E17E53`

## 验证

`verify-patched-pricing.mjs` 直接从补丁后的 8.3 MB 生产主包提取并执行压缩函数，8 项用例通过：

- 无覆盖值时使用选中分组倍率。
- `all` 视图无覆盖值时使用最低可用分组倍率。
- 选中分组下数值覆盖生效，分组身份不变。
- `all` 视图下数值覆盖生效，最低倍率分组身份不变。
- 字符串覆盖值被忽略。
- 数值 `0` 保留指针字段的数值语义。
- 按次计费使用覆盖倍率。
- 动态计费返回覆盖后的 `usedGroupRatio`。

执行结果：`{"cases":8,"result":"passed"}`；补丁归档重新解包后仍为 357 个文件。

## 构建与限制

本机没有 Bun，且本地源码与生产定制 UI 不同，因此没有用源码构建物覆盖生产静态资源。源码改动用于后续定制前端源码合并；本次生产二进制应嵌入上述补丁归档内的 `web/classic/dist`。该脚本固定校验本次归档基线，输入变化时会拒绝执行，需要重新确认压缩函数及定制资源。

本子任务没有执行部署。父会话后续已完成二进制嵌入、发布和线上新主包 hash/API 字段验证。

## 会话身份

- Canonical task：`/root/luna_pricing_ui`
- Thread ID：`01a0cc83-5d5a-7bc3-be0e-88c80dff8fbd`
- Session ID：`01a0c79b-ddb9-74f3-bf1d-d3b590062af4`（环境提供；是否为父会话继承值未验证）
- 模型/推理等级：当前协作工具未向本线程返回可核验元数据，不猜测
- 恢复：当前线程可通过原生 follow-up 接续；跨客户端恢复未验证，可依本回执接续
