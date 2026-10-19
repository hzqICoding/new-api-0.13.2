# 一次性只读聚合HTTP验证

此程序只供受控验证，不是new-api部署入口。它注册真实AdminAuth与AggregateUsage到临时loopback httptest服务器，从stdin读取已有服务器连接配置/管理员身份/固定范围/期望汇总，逐项比较后退出。

- 不调用应用main、InitDB、迁移、定时任务、日志写入或真实模型接口。
- MySQL DSN在内存中强制session `transaction_read_only=1`、4秒SQL上限；先确认read_only=1才继续。
- 只支持最多4组样本，各3次，以及匿名401/关闭503检查；总context90秒，外部执行包装105秒强制停止。
- stdout只有脱敏结果/固定错误，不输出凭证、DSN或原始日志。不要把stdin输入保存进仓库或调用命令参数。
- 使用服务端既有运行镜像仅作为一次性程序的运行环境；不调用其中应用入口。受限容器退出自动删除，没有对外监听端口。

构建示例：设置 `GOOS=linux GOARCH=amd64 CGO_ENABLED=0` 后执行 `go build -o <临时路径> ./verification/usageaggregate`。调用由拼车仓库 `verification/usage_aggregate_http_probe.py` 完成，该包装绑定已经核验的程序SHA，重新构建须重新审核并更新SHA，不能跳过验证。

2026-10-19实测、程序SHA及边界见本项目docs/usage-aggregate-20261019/README.md与拼车docs/81。勿将这次候选loopback对照宣称为线上已发布或高峰压测。
