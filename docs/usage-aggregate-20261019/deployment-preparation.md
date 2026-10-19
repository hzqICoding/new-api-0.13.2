# r15.17 默认关闭发布脚本回执

2026-10-19。仅完成本地发布脚本编写、依赖源码审核和输入manifest验证；**本回执不代表已部署**。主代理负责线上基线核实、上传和执行。

## 发布入口与依赖

新脚本：`docs/usage-aggregate-20261019/deploy.py`。

serverB发布目录：`/opt/carpool/releases/newapi-usage-aggregate-20261019`。

保留并只复用现有链：

- `/opt/carpool/releases/sensitive-message-20260922/deploy.py`：`recover`、原健康/程序哈希/旧token-scoped日志验证。
- `/opt/carpool/releases/r15-20260906/release_r15.py`：受限文件保存、Docker/API/数据库只读查询、网络参数提取和有所有权校验的rollback。
- `/opt/carpool/releases/r15-20260906/extract_embedded_frontend.py`：候选二进制完整内嵌前端提取。
- `/opt/carpool/releases/newapi-failure-log-20261018/frontend-manifest.json`：r15.16全部956个内嵌资源基线。

旧`base.cutover`依赖`stage-results.json`，本轮**不调用它**。新脚本只复用其容器创建/校验/停止/更名/启动步骤，前置凭据改为真实生成的`build-checks.json`，不会伪造stage通过，也不提供stage或policy-off命令。

## 固定版本与输入

- 原版本：`v0.13.2-carpool-r15.16-failure-log-20261018`。
- 原镜像：`sha256:45a4d257a4f048bdca4c60b12e77881f2814425b9f0222ef5d8409ff1174fb18`。
- 原程序SHA256：`c987f75bb5db4a72119951da575351c34d7b2c17f7f5369ccdcc5c7d6064afd6`。
- 新版本：`v0.13.2-carpool-r15.17-usage-aggregate-20261019`。
- 新镜像tag：`hzqicoding/new-api:carpool-r15.17-usage-aggregate-20261019`。
- 已本地验证候选SHA256：`756970f5ee85edef29eaada69bad653303e1443cb1029b5d0e4b3d2277764e3d`。

发布目录须有`new-api`、`build-manifest.json`、`frontend-manifest.json`。build manifest字段：`version`、`baseline_image`、`baseline_binary_sha256`、`binary_sha256`、`baseline_source_files`、`source_files`。两份source map为相对路径→SHA256，覆盖完整Go/go.mod/go.sum。

脚本要求源码无删除、只有既有`router/api-router.go`变更，以及本轮2个业务Go文件和3个测试文件新增；`go.mod/go.sum`完全不变。此次实际输入596→601项，检查通过。`source_head`等附加记录可在prepare前更新；prepare后整个manifest SHA固定。

## 执行顺序

```bash
python3 /opt/carpool/releases/newapi-usage-aggregate-20261019/deploy.py preflight
python3 /opt/carpool/releases/newapi-usage-aggregate-20261019/deploy.py prepare
python3 /opt/carpool/releases/newapi-usage-aggregate-20261019/deploy.py cutover
python3 /opt/carpool/releases/newapi-usage-aggregate-20261019/deploy.py verify
```

- `preflight`只读核对运行容器、镜像、`/proc/1/exe`、API版本、环境关闭状态、候选manifest、回滚/候选名称和CP健康。
- `prepare`完整提取候选前端并比对956项；保存受限基线和非策略options/表结构/索引摘要；把精确旧镜像ID标到固定base tag并再次检查ID，再无网络/不拉取地构建仅COPY新程序的镜像。不编译或重构前端。
- `cutover`重新检查镜像/容器ID、Env/HostConfig/挂载/网络未漂移，核对build-checks及输入SHA、其他容器和非策略options/结构均未变，之后才创建候选并切换。候选Env完整复制；聚合开关只能absent或false。异常自动回滚旧容器。
- `verify`检查新版本/程序哈希/旧日志`token_filter_applied`/CP健康、首页及新旧代表资源哈希、授权POST聚合503/匿名401。授权POST使用空tokens且功能关闭，不运行日志聚合。非`CarpoolConcurrencyPolicy`的全部options摘要及schema/index定义必须不变；其他运行容器的ID/image/启动时间/重启计数必须完全一致。

因此new-api的完整verify须先于CP后端切换；不能在prepare和verify之间并行启停辅助容器。prepare已有记录或cutover已有状态时会拒绝覆盖，失败后由发布操作者核对状态，不自动抹掉回滚证据。

## 回滚

```bash
python3 /opt/carpool/releases/newapi-usage-aggregate-20261019/deploy.py rollback
```

旧容器保留名为`new-api-rollback-newapi-usage-aggregate-20261019`。回滚复用已有所有权/镜像检查，恢复原容器及网络，再核对r15.16版本和程序SHA。没有数据库恢复、业务写入或开关启用命令。

## 本地检查

- Python AST语法检查通过；未import/执行serverB部署入口。
- 从AST只抽取纯函数`require`、`sha_file`、`check_manifest`，以本地真实候选和manifest执行：`manifest=OK`、候选SHA等于上述值、内嵌manifest956项。
- `git diff --check`通过。
- 已审阅主代理下载的base-deploy.py与release_r15.py，确认旧stage门槛、配置复制、rollback所有权与网络恢复行为；新脚本未直接绕过旧函数内部条件，而是独立实现本轮批准的最小容器切换。
- 未SSH、未构建/启动服务器容器、未执行生产发布、未提交、未改业务文件。实际生产镜像构建和切换结果由主代理另录。
