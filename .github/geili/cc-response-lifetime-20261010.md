# Chat Completions 上游响应生命周期修复

`0.2.14-geili.4` 的隔离 Stage 双实例缓存验收在切换备用账号后返回 502。原始失败没有重写或标为通过；源码修复进入 `.5` 候选，所有候选门禁需要重新运行。

## 实际故障

2026-10-10T05:57:47Z 的原始报告第 8 条失败，前 7 条通过。A 和 B 各一次推理，自动协议探测也各一次，因此没有重复推理或遗漏 B。B 的协议探测于 05:57:30.312Z 完成并记录 `responses_supported=false`；A 于 05:57:34.319Z 进入指定模型冷却，随后切换到 B。05:57:35.019Z 的真实 B 日志为 `read upstream body: context canceled`。

失败原始 JSON SHA-256：`1560888c73ca11831548de98a27283ae63722993c8ad5d21b23f845d885fc6f4`。完整 Stage 日志保留在运维私有证据目录，不公开账户标识、Key、请求内容或客户信息。正常恢复完成，额外实例和卷已删除，未执行后续原生媒体或付费供应商测试。

## 源码原因与修复

`sendCCUpstreamRequest` 使用 `detachUpstreamContext` 构造请求，却在返回响应的 helper 内延迟调用上下文释放。返回时仅收到响应头，三个调用者仍需读取正文：

- `forwardAsRawChatCompletions`：Chat Completions 直转。
- `forwardResponsesViaRawChatCompletions`：Responses → Chat Completions。
- `forwardAnthropicViaRawChatCompletions`：Messages → Chat Completions。

初次请求没有恢复时限时，释放函数通常无操作。第一次可恢复故障启动共享恢复时限后，`detachRecoveryDeadlineGeili` 为备用请求创建真实 `WithDeadline` 上下文；helper 返回时取消该上下文，导致正文尚未读取就失败。这段逻辑与 `.3` 的 `7dd2a801…` 相同，独立 Seedance 白名单没有修改它。

修复仅调整共享 CC 管线的释放所有权。请求构建或传输失败时仍在 helper 返回前释放；成功响应使用已有 `openAIRequestContextReadCloser`，由正文关闭释放上下文。该包装器通过 `sync.Once` 保证取消及底层关闭各一次，保留底层关闭错误，先取消再关闭以解除阻塞读取。恢复时限仍传递到上游，超过时限仍终止读取；不延长或删除时限，不修改账号选择、重放规则、定价、结算或数据库。

## 验证边界

新 `openai_cc_response_lifetime_geili_test.go` 在修复前的真实 `.4` 源码先运行失败：响应头先到达的实际 HTTP 流式/非流式请求与三个 CC 调用点均出现 `context canceled`，预期自然到期的测试也提前取消。修复前 stdout、stderr、执行时间、源码及测试 SHA 均原样保存在源码仓忽略的 `deploy/.secrets/cc-response-lifetime-geili/`。

修复后的回归已验证：

- 响应头返回后、正文完成前上下文保持有效；正文关闭后释放。
- 三个协议调用点均获得完整内容和真实用量。
- 流式与非流式延迟正文仍受原共享恢复时限约束。
- 传输失败及时取消；无效请求地址不会调用上游。
- 并发关闭仅关闭底层一次，且如实返回关闭错误。

本地普通与 race 检查已通过：受影响恢复、缓冲读取、首输出及本次生命周期共 42 个顶层测试（含子项 75 项）分别通过普通和 race 检查。随后只将两个新测试的无检查类型断言改为检查 `http.NewResponseController(w).Flush()` 错误，并在最终测试字节上重新运行本次 6 个顶层回归（含子项 11 项），普通和 race 均通过。第一次官方 `golangci-lint 2.14.0` 增量检查实际发现这两处测试 `errcheck`，失败日志保留；修复后的 `--new-from-rev=7dd2a801d8048da2a2824d35ec7ad78617d97ba6 ./...` 检查退出 0，stdout 为 `0 issues.`，stderr 为空。没有修改原常规 CI 的旧 lint 工具或将其失败改记通过。

所有执行记录都标明未提交工作树及精确源码、测试 SHA。本地检查不能替代新候选的 CI、安全扫描和同镜像 Stage 51/17/108 项验收，也不能替代三项受控真实模型的生成、单次结算与自有归档验证。生产继续运行 `.3`，本文件不是发布完成证明。
