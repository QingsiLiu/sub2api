# Claude 官方 Key 思考参数兼容修复

## 生产取证（2026-10-13，北京时间）

只读查询生产数据库，未修改账号、代理、优先级或生产镜像；未调用真实供应商。

- 错误记录 `5683977`：账号 `6381 / enterprise-k1`，`claude-opus-5`，User-Agent 为 `node`，官方上游 400：`thinking.adaptive.effort: Extra inputs are not permitted`。
- 新账号创建于 00:02:52，上游地址为 `https://api.anthropic.com`，账号模型映射保留 `claude-opus-5`，未开启自动透传。因此实际路径为普通 Anthropic API Key 转发。
- 查询时最近三小时，同一账号的此类错误共 124 次（00:12:34 至 00:34:35），均来自同一下游 Key、同一模型和 `node` 客户端；该账号同期有 549 次成功调用，凭据本身可用。
- 同一下游 Key 的 `node / claude-opus-5` 在旧中转账号 `6315` 上同期有 3508 次成功调用。官方上游采用严格参数校验；旧中转的成功记录可以解释接入官方 Key 后才暴露兼容问题，但不能证明两条路径的请求体完全相同。
- 当前生产数据库已移除请求体存储，完整网关请求调试日志未开启；无法从留存证据确定客户端包名、版本或原始 effort 值。上游错误足以证明进入 adaptive 校验分支的 thinking 对象带有不被接受的 effort 字段。代码没有生成 `thinking.effort`，且没有纠正该错误位置，最可能来自客户端或其适配层。

## 修复

官方文档将思考强度定义为顶层 `output_config.effort`：
https://platform.claude.com/docs/en/build-with-claude/effort

count_tokens 同样支持 `output_config.effort`：
https://platform.claude.com/docs/en/api/messages/count_tokens

在模型映射解析后、请求校验前执行 Geili 兼容函数，随后通过 `ParsedRequest.ReplaceBody` 同步原始 JSON 视图和 effort，用于计费与用量记录。普通/流式/自动透传消息与 count_tokens 共用相同规则。

```json
{"thinking":{"type":"adaptive","effort":"high"}}
```

转换为：

```json
{"thinking":{"type":"adaptive"},"output_config":{"effort":"high"}}
```

已有 `output_config.effort` 优先。仅实际 `claude-*` 模型的 adaptive 字符串 effort 被处理，错误类型及错误值仍交给上游校验，第三方模型不受影响。使用局部 JSON 修改，保留工具输入大整数、display 与结构化输出格式。

## 验证

- 11 个参数与协议边界用例，6 个公开 Forward/ForwardCountTokens 出站用例（普通/透传 × 非流式/流式/count_tokens），检查实际 wire body、模型映射、大整数及 `ParsedRequest.OutputEffort`。
- 干净源码快照从 `geili/main` 导出，不包含已有 OpenAI 工作区改动；旧转发代码下 6 条出站用例均因仍发送 `thinking.effort` 失败，加入两处挂钩后 17 个用例全部通过。相关 race（AnthropicThinkingEffortGeili / ClaudeOpus5 / AnthropicAPIKeyPassthrough / Claude55 / MidConversation / ContextManagement）通过。
- 将已推送 revision `da193f309961d9f393a96c74b01eaf9b0d655f03` 单独导出后，`go test -tags=unit ./internal/service -count=1` 全部通过（223.252 秒）。首轮完整单测误用了旧转发对照代码，其 6 条新增回归用例失败；以精确候选源码重跑后的通过结果为准。
- 不通过真实官方 Key 发送请求。生产效果需发布同一候选镜像后观察现有自然流量。

## 发布边界

源码候选版本为 `0.2.15-geili.7`，revision 为 `da193f309961d9f393a96c74b01eaf9b0d655f03`，已推送 `origin/geili/main`，[候选 CI 38279121683](https://github.com/QingsiLiu/sub2api/actions/runs/38279121683) 已通过。用户当次条件授权后经正式Stage→prod入口发布同一digest，Stage244项、生产45项复核通过。目标Key发布后暂无新调用，现有Claude/OpenAI自然流量成功；未主动调用真实供应商。具体回执见运维仓 `docs/reports/claude-effort-20261013/production-receipt.json`。
