# Classic 前端模块缺失审计回执（2026-09-25）

## 结论

候选产物 `E:\tmp\newapi-frontend-fix-20260925\build\web\classic\dist` 已解决 `/console` Dashboard 的阻断：

- `/console` 懒加载入口 `assets/frontend-fix-20260925/index-3m0hM-vt.js` 的静态闭包共 9 个模块，缺失引用为 0，静态循环为 0。
- Dashboard 对应的 Vite preload 目标及 7 个依赖全部存在于 `assets/frontend-fix-20260925/`。
- 43 个仍引用 `./index-D4VkjDT5.js` 的 namespace 分包可经同目录 shim 链接到 `index-luna-0216aa8f9328.js`。
- 全产物命名导入检查结果为 `unresolved named imports: 0`；`export *` 没有丢失分包所需导出。
- `vm.SourceTextModule` 对 namespace 主入口、shim、Dashboard，以及 root 回退 shim、Dashboard 均完成 parse/link，未执行应用代码。
- `/pricing` 没有独立懒加载 chunk，页面组件 `kje` 位于主入口中；其运行前静态闭包就是 namespace 主入口的 6 个模块，缺失引用为 0。

因此，当前候选可以作为 Dashboard/Pricing 的发布候选。剩余 `_basePickBy-pe694AoF.js` 与 `_baseUniq-j-5v1Cfv.js` 不阻塞这两个页面。

## 候选的静态闭包

namespace 主入口闭包（6 个模块）：

- `index-luna-0216aa8f9328.js`
- `react-core-D-iPSUlg.js`
- `semi-ui-BzZ1UhpZ.js`
- `i18n-RrXBN1CV.js`
- `tools-PDT6Ig-2.js`
- `react-components-BnylIsR_.js`

namespace Dashboard 闭包在上述基础上增加：

- `index-3m0hM-vt.js`
- `index-D4VkjDT5.js`（shim）
- `IllustrationConstruction-BfOYqVqj.js`

Dashboard 的 Vite preload 映射为：

| map index | 资源 | 结果 |
|---:|---|---|
| 50 | `index-3m0hM-vt.js` | 存在 |
| 6 | `semi-ui-BzZ1UhpZ.js` | 存在 |
| 7 | `react-core-D-iPSUlg.js` | 存在 |
| 8 | `semi-ui-yRXI6evF.css` | 存在 |
| 9 | `i18n-RrXBN1CV.js` | 存在 |
| 51 | `IllustrationConstruction-BfOYqVqj.js` | 存在 |
| 10 | `tools-PDT6Ig-2.js` | 存在 |
| 11 | `react-components-BnylIsR_.js` | 存在 |

namespace 主入口中的 56 个 quoted `assets/` preload 引用均已改为 `assets/frontend-fix-20260925/`，未发现仍指向旧 root 的 quoted preload 路径。`index.html` 的主入口、5 个 modulepreload 与 2 个 CSS 均指向新 namespace。

## shim 风险判断

shim 内容为：

```js
// Compatibility entry: one shared application module.
export * from "./index-luna-0216aa8f9328.js";
```

该方案在当前依赖图中风险可接受：

1. `export *` 只转发命名导出，不执行第二份应用代码；同一 URL 的目标模块由 ESM 模块缓存只实例化一次。
2. Dashboard 的静态方向是 `Dashboard -> shim -> Luna main`。Luna main 到 Dashboard 是运行时 `import()`，不是静态边，因此 parse/link 图中不存在循环。
3. 静态扫描解析了本地命名 import 与 export/re-export 的导出面，候选全产物没有缺失命名导出。
4. `vm.SourceTextModule.link()` 对真实候选闭包链接成功，能覆盖浏览器在执行前的模块解析、缺失导出和静态循环链接错误。

namespace 隔离同时解决旧 HTML/旧入口缓存混用风险。新 HTML 只加载 namespace 主入口；旧缓存 HTML 若仍请求 root `index-D4VkjDT5.js`，root shim 会转到 root Luna main。两套 URL 不会在同一份候选 `index.html` 中同时启动。

shim SHA-256：

- root：`93FE9A98577F4768EBEC8DCE99B2DFF732BC76D69CDBD56D37AC30D838949E88`
- namespace：`93FE9A98577F4768EBEC8DCE99B2DFF732BC76D69CDBD56D37AC30D838949E88`
- namespace Luna main：`E3E785A7A3D2EC1FF6B56677E0000919F30CBA1D53B0EDCB10A28954AE8B2479`

## 剩余缺失资源

候选 classic dist 的全量扫描仅剩两个逻辑资源（root 与 namespace 各一份路径，共 4 个缺失目标、50 个引用）：

- `_basePickBy-pe694AoF.js`
- `_baseUniq-j-5v1Cfv.js`

它们被 Mermaid 图表相关动态 chunk 引用，也出现在主入口的 Vite dependency map 中。仅定义在 map 数组中不会触发请求；Dashboard 的 preload index 列表不包含这两项，Pricing 也不进入这些动态图表 chunk。

已核查以下恢复来源，均不存在这两个文件：

- 仓库 `web/classic/dist`
- `E:\tmp\luna-pricing-20260923\patched-dist\web\classic\dist`
- `E:\tmp\luna-pricing-20260923\baseline-check\web\classic\dist`
- `production-frontend-luna-group-ratio.tar.gz`
- `production-frontend.tar.gz`
- `baseline-source.tar.gz`
- `newapi-source.tar.gz`
- `E:\tmp` 文件名扫描
- `E:\OtherPro` 文件名扫描

`main.go:44` 使用 `//go:embed web/classic/dist`。Go 对目录 embed 默认忽略 basename 以 `.` 或 `_` 开头的文件，这与两个 chunk 在生产归档和二进制恢复产物中同时缺失相符。后续若要修复 Mermaid 图表，应从原始前端构建产物恢复这两个文件，并将 classic embed 改为能够包含 underscore 文件的 `all:` pattern，或在前端构建阶段避免生成 underscore 开头的 chunk 名。不能从当前生产/备份归档可靠重建其精确内容。

## 可重复验证

静态依赖、preload、闭包、循环与命名导出审计：

```powershell
node docs/frontend-module-fix-20260925/audit-frontend-deps.mjs `
  E:\tmp\newapi-frontend-fix-20260925\build\web\classic\dist `
  --entry index.html `
  --entry assets/frontend-fix-20260925/index-luna-0216aa8f9328.js `
  --entry assets/frontend-fix-20260925/index-D4VkjDT5.js `
  --entry assets/frontend-fix-20260925/index-3m0hM-vt.js
```

关键输出：

```text
missing refs: 50; missing targets: 4
unresolved named imports: 0
entry index.html: 68 static files, 0 missing refs, 0 cycles
entry assets/frontend-fix-20260925/index-luna-0216aa8f9328.js: 6 static files, 0 missing refs, 0 cycles
entry assets/frontend-fix-20260925/index-D4VkjDT5.js: 7 static files, 0 missing refs, 0 cycles
entry assets/frontend-fix-20260925/index-3m0hM-vt.js: 9 static files, 0 missing refs, 0 cycles
```

定向 ESM parse/link（不 evaluate）：

```powershell
node --experimental-vm-modules `
  docs/frontend-module-fix-20260925/audit-vm-link.mjs `
  E:\tmp\newapi-frontend-fix-20260925\build\web\classic\dist `
  assets/frontend-fix-20260925/index-luna-0216aa8f9328.js `
  assets/frontend-fix-20260925/index-D4VkjDT5.js `
  assets/frontend-fix-20260925/index-3m0hM-vt.js `
  assets/index-D4VkjDT5.js `
  assets/index-3m0hM-vt.js
```

实测输出：

```text
LINK_OK assets/frontend-fix-20260925/index-luna-0216aa8f9328.js modules=6 status=linked
LINK_OK assets/frontend-fix-20260925/index-D4VkjDT5.js modules=1 status=linked
LINK_OK assets/frontend-fix-20260925/index-3m0hM-vt.js modules=2 status=linked
LINK_OK assets/index-D4VkjDT5.js modules=7 status=linked
LINK_OK assets/index-3m0hM-vt.js modules=2 status=linked
TOTAL_MODULES=18
```

## 审计身份

- canonical task：`/root/frontend_dependency_audit`
- `CODEX_THREAD_ID`：`01a0d771-0936-71b2-b7cb-15a42f8cc8c1`
- `CODEX_SESSION_ID`：`01a0c49e-8bd9-7510-ad13-7a8e0763e458`
- 模型 / reasoning effort：运行时未提供可核验元数据
- 跨客户端恢复：未验证；可依据本回执及两个审计脚本接续

## 验证边界

本审计没有启动浏览器，也没有执行应用模块。`vm.SourceTextModule` 只验证模块 parse/link；主代理后续已验证接口与资源响应，DOM 初始化和用户页面交互仍待浏览器验收。
