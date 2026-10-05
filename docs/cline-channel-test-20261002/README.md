# Cline渠道55检测参数修复

> 此文记录r15.9当时的实施和用户确认，当前版本见[维护索引](../README.md)。2026-10-05仅复核归档记录，没有重新发起渠道55真实检测；旧发布脚本不可直接覆盖当前版本。

## 变更记录

| 日期 | 版本 | 变更说明 |
|---|---|---|
| 2026-10-02 | r15.9 | 仅调整渠道55的DeepSeek Chat检测参数为max_tokens=256、reasoning.enabled=false；保留手动选择的stream，发布及New API真实检测通过。 |

## 原因与范围

GPT-Load rc.40专用Cline渠道已解决非流式success/data包装，但New API内置检测仍构造16 Token预算，实测可触发上游empty response content。修改 `controller/channel-test.go`：共享testChannel路径构造请求后应用限定参数，手动及定时检测共用；正常relay入口不调用该函数。

适用条件：Channel ID=55、GeneralOpenAIRequest、模型名以deepseek/开头。其他渠道、非DeepSeek、Responses等非Chat请求不修改；不强制改变stream或普通用户请求。此规则是本安装实例的专用兼容处理，渠道55未来若换供应商/用途须重新评估，不能无条件复制到其他实例。

## 验证

- 2026-10-02 用户在实际New API管理后台重新测试，明确反馈“确实恢复正常”，完成用户侧渠道检测验收。

- `go test ./controller -run 'TestClineChannelTestParameters|TestRelaySensitiveWords' -count=1`通过，覆盖默认/显式OpenAI路径、两种stream、相邻渠道不变、非目标模型/请求类型不变及false字段序列化。
- 当前生产r15.8源码归档逐项对比仅controller/channel-test.go发生业务代码变化；新增测试另列。隔离build中保留全部既有业务补丁及474个前端资源，内嵌文件hash逐项一致。
- 隔离实例通过真实 `/api/channel/test/55` 和 `/56` 调用mock，捕获出站请求确认55预算256/关闭思考、56预算16/不添加思考参数；登录/看板API和前端资源验收通过。
- 线上New API自带检测，vision-exp：非流式success=true约0.466秒，流式success=true约0.986秒。仅代表当次测试，上游限流或波动仍可能导致真实失败。
- 手动与定时共用函数已经代码确认；本次未等待下一次定时轮询，不把其写成定时任务已实测。
- 线上options摘要、Luna倍率覆盖及其他运行容器ID/启动时间保持一致；拼车健康和日志接口正常。

## 发布

- 版本：`v0.13.2-carpool-r15.9-cline-test-20261002`。
- 镜像：`hzqicoding/new-api:carpool-r15.9-cline-test-20261002`。
- 发布目录：`serverB:/opt/carpool/releases/cline-channel-test-20261002/`，含586项源码归档、程序、资源manifest、原容器配置、隔离请求回执、生产检测回执及部署脚本。
- 固定旧基线：r15.8镜像`sha256:c16a793e83ebe8cc38f36e50e784807ab352ec2dcfcd105f24a2c2abf89edec5`。
- 回滚容器：`new-api-rollback-cline-channel-test-20261002`，停止保留。
- 未修改数据库配置或New API渠道参数覆盖；无需回滚价格。旧版本恢复后渠道测试会回到16 Token。

```powershell
ssh serverB "python3 /opt/carpool/releases/cline-channel-test-20261002/deploy.py verify"
# 仅在需要回退本次版本时执行，后续已有新发布则先核对状态：
ssh serverB "python3 /opt/carpool/releases/cline-channel-test-20261002/deploy.py rollback"
```

后续升级需保留本次检测补丁，以及Luna和r15.8前端修复。不要用全局max_tokens覆盖代替检测修复，也不要将上游真实429认定为参数修复失效。
