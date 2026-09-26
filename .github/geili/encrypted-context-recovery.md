# 加密上下文错误包装兼容（2026-09-26）

## 已确认的问题

部分 Responses 上游把加密校验失败包装为 HTTP 400、`error.code=invalid_request_error`，但保留文案：

```json
{"error":{"code":"invalid_request_error","type":"invalid_request_error","message":"The encrypted content [redacted] could not be verified. Reason: Encrypted content could not be decrypted or parsed."}}
```

普通 HTTP 转发此前仅对 `invalid_encrypted_content` 精确错误码执行一次恢复。通用错误码因此绕过恢复和会话失效密文摘要登记，客户端再次携带旧历史时可能继续失败。该错误本身不能证明客户端切换供应商，也不能判定供应商内部是否切号或更换密钥。

## 本次改动

在 `openai_gateway_forward.go` 增加一个最小 geili hook，实现集中在 `openai_encrypted_content_recovery_geili.go`。仅扩展普通 HTTP Responses 路径：

- 上游必须返回 HTTP 400，错误码为空或 `invalid_request_error`，且错误消息同时包含加密内容、校验失败、无法解密或解析三段明确特征。
- 请求必须含非空的 `reasoning.encrypted_content`；没有可清理密文则不重试。
- 请求含 `compaction` / `compaction_summary`、其他类型的非空加密项或非空 `previous_response_id` 时，不启用新文案兜底，保留原错误。不能通过删除唯一历史载体来伪装恢复成功。
- 满足条件时复用已有同账号、最多一次的清理重试及会话摘要机制。保留有效推理摘要、用户消息、工具定义、调用和结果、模型及推理设置；原始入站 body 不被修改。
- 同会话重发旧密文时会按摘要预清理；新密文和其他会话不受影响。摘要存储沿用现有进程内 TTL / 容量限制，跨进程或重启后可能再次需要一次恢复。

这不改变原先精确 `invalid_encrypted_content` 错误码的处理语义，也不修改 WebSocket、透传、调度、账号配置、计费或数据库。精确码旧路径原有的压缩项清理行为不在此次扩大处理范围内。

## 验证

新增 `openai_encrypted_content_recovery_geili_test.go`，使用合成内容、假上游和不可访问的示例地址，不发送真实供应商请求或产生费用。

先用原代码运行新增转发测试，流式和非流式均复现 HTTP 400；应用修复后通过。覆盖：

- 流式与非流式失败后成功，恢复前不向客户端泄漏错误。
- 重试最多一次；下一轮原始历史、新密文和其他会话的隔离。
- 工具与用户内容保留，大整数精度不变，原始 body 不变。
- 加密压缩历史保留且不登记为后续待删项。
- 普通 400、上下文超限、认证/限流/服务端错误、含糊文案、无密文、未知加密项和服务端历史引用不误触发新兜底。

本地 `go test ./...`、下列定向回归及相同范围的 `-race` 均通过。CI 仍须验证完整候选及带标签的集成门禁。

定向回归：

```sh
cd backend
go test ./internal/service -run 'TestOpenAIEncryptedRecoveryGeili|TestOpenAIGatewayService_Forward_HTTP.*(Encrypted|Recovery)|Test.*InvalidEncryptedContent|Test.*EncryptedContentDigests|TestTrimOpenAIEncryptedReasoningItems' -count=1
```

## 发布与边界

本次是源码修复，沿用正在准备的版本，未单独提升版本或发布生产。以包含本提交的候选 CI revision / digest 为制品身份；不能使用旧镜像宣称含此修复。Stage 和生产上线仍须按运维仓固定流程执行，生产须当次授权。主干还有其他待发布改动，不能把本修复的定向测试当作整枚镜像完成验收。

此修复不能保证修复供应商内部所有会话问题，也无法还原已经不可解密且仅存在于 compaction 中的历史。对压缩历史失败，应核对上游会话/密钥连续性，或由客户端从可读历史重建上下文；不要自动清空历史或盲目切换线路。
