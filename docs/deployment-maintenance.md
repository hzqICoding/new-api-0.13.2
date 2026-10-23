# 定制版本归并与升级检查单

## 变更记录

| 日期 | 版本 | 变更说明 |
|---|---|---|
| 2026-10-23 | v1 | 用户授权归并已发布功能到自有main并推送；记录r15.21生产源码标签、升级保留项与精确回退。 |

当前生产r15.21-ultra-20261023运行源码27f222a7，发布标签deploy/r15.21-ultra-20261023指向此源码；主分支main包含后续维护文档。自有origin为https://github.com/hzqICoding/new-api-0.13.2，普通push main，不强推。主分支有文档更新不等于线上已升级。

升级前读取本仓AGENTS、docs/README、计费/并发/失败日志/聚合/资源/Ultra各专题以及主工程93维护手册。当前main需要保留r15.14–r15.21所有定制，不可用官方版本覆盖后再只补最新Ultra字段。

必须保留：Luna/拼车倍数及计费冻结、已有Key/分组权限、并发队列/失败请求日志、座位用量聚合的token和水位过滤、Codex快速配置与956个前端资源、请求关联日志、可信Ultra元数据和attempt清旧。Ultra只改显示元数据，不改请求effort、费率或金额计算。

生产构建要求Go1.26.10或经独立验证的更高修复版本；不要直接运行当前仍旧工具链的Dockerfile/CI后当作安全构建完成。保留实际生产内嵌资源和模块清单；缺dist时从受信的服务器源码/旧程序与manifest恢复，不用占位或官方前端替换。程序/Go/module/资源SHA都要核对。

当前发布目录serverB:/opt/carpool/releases/ultra-metadata-newapi-20261023-1/new-api/。当前精确回退是在serverB运行其父目录newapi_metadata_release.py ultra-metadata-newapi-20261023-1 rollback，仅回r15.20，保留数据库；旧security-go12610脚本不是当前相邻回退。metadata-verified.json才是本轮完整验证回执。

每次修改完成：提交→测试/审核→按用户授权发布→更新版本/备份/回退/验收边界→fetch自有origin→归并main→push main与本次显式发布标签→ls-remote核对。远程分叉先读真实diff并补回归，绝不用force绕过。

跨仓关系与最终同步记录：[拼车93维护手册](<E:/OtherPro/carpool-platform/docs/1-项目文档/93-三仓库版本归档与升级维护.md>)。此维护轮不重新部署服务，真实Ultra=true自然样本/浏览器验收仍保留待观察。

## 本轮归档结果

已将已发布分支fast-forward归并自有main并推origin/main；注释标签deploy/r15.21-ultra-20261023已推送，解引用为运行源码27f222a7992af0c3b447fb4da0300c694dd6c789。运行Go/module代码与该提交完全一致，工作区干净、无冲突/强推。后续main的文档提交不代表线上重新发布；最终远程同步以交付时ls-remote核对为准。
