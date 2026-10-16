# Anthropic 严格透传

来源是外部渠道提供的 `sub2api-strict-passthrough-patch`（官方基线 `4726bdd08`，补丁提交 `8080ed85e`）。原补丁按官方旧版写成，这里按 Geili 现网主干移植。下文的“Geili 取舍”是与原补丁的差异。

## 开启

- 账号 `extra.anthropic_passthrough=true` 才是严格模式。只认显式 true：存量账号缺这个字段时仍是兼容模式，上线不改变任何现有账号的行为。原补丁让直连 `api.anthropic.com` 的存量账号默认变严格，未采纳。
- 后台新建 Anthropic API Key 账号时，表单默认勾选严格透传；取消勾选会显式保存 false。
- 严格模式忽略账号模型映射、头覆写、工具名混淆、预热拦截和 Web Search 模拟。组级 reasoning effort 策略或渠道模型映射会改写正文时，请求直接返回 400，不静默降级。要使用的真实模型名需加入准入和计价配置。

## 字节边界

- 请求：上游收到客户端原始字节。选号和计费仍按兼容视图解析（lenient JSON 修复、`[1m]` 后缀归一），选中严格账号后再换回原始字节。query 原样转发，去掉 `key` / `api_key`；业务扩展头原样转发；认证头、Cookie 和 hop-by-hop 头不透传；`Accept-Encoding` 固定为 identity；不跟随 3xx。
- 响应：状态码、正文、业务头原样返回（去掉 `Set-Cookie` 和 hop-by-hop 头）。SSE 保留 CRLF、注释、`id:` 和末尾未换行的内容。不还原工具名，不改写 usage。
- Models / Files：分组只配置了一个账号且该账号是严格账号时，直连上游的 Models、模型详情和 Files（列表、上传、详情、下载、删除）。多账号池的 Models 仍由本地汇总，Files 返回 404。Files 是上游账号的共享命名空间，不按站内 Key 隔离。

## Geili 取舍

1. 账号级错误（401/403/429/529/5xx）仍走同账号重试和换号。只要还没向客户端写出内容，就用同一份字节重发；全部失败后，原样回写最后一次上游的状态码、正文和业务头（`WriteStrictAnthropicFailoverGeili`）。400、404 等请求级错误直接原样返回。
2. 流式响应按完整事件转发。上游静默时，在两个事件之间补发 Anthropic 协议自带的 `event: ping`（`gateway.stream_keepalive_interval`，设 0 关闭），不会拆开事件。空闲超时沿用长思考模型的预算。经 Cloudflare 的请求仍由边缘心跳兜底：静默 45 秒后会提交 200，此后的错误只能在流内返回。
3. 首个语义输出之前，断流、空闲超时、缺少终止事件和流内错误仍由 `text_forward_guard` 换号重放。这一阶段上游的 ping 和注释原样放行，不再改写成 `: keepalive`。
4. 兼容模式（OAuth 账号、未开严格的 API Key 账号）保持不变：历史思考预过滤、签名失败降级重试、count_tokens 签名重试、`[1m]` 归一和 lenient JSON 修复都照旧。
5. 工具名还原改为只改协议里的 name 字段（来自原补丁）。原补丁只能处理单行 `data:` 或整段 JSON，而 OAuth 流传入的是完整 SSE 块（`event:` + `data:`），会导致假工具名漏还原；这里改为逐行还原。
6. Anthropic 分组在排队等槽时不再由 handler 发 ping（来自原补丁）。用户槽最多等 30 秒，之后由边缘心跳覆盖，Cloudflare 不会因此 524。

## 前置反代

`/v1/messages`、`/v1/messages/count_tokens`、`/v1/models` 及详情、`/v1/files` 及子路径要直接交给网关。设置 `proxy_intercept_errors off`，关闭响应缓冲，不要对业务 JSON/SSE 做 `sub_filter`。若链路还经过另一套 Sub2API，对方也必须开启严格模式。

## 回归与验收

- 单元测试：`internal/service/gateway_anthropic_strict_test.go`、`gateway_anthropic_strict_geili_test.go`，以及 handler、routes 中的 `StrictAnthropic*` 测试。
- 上线前必须用真实第一方 Key 验收：thinking/signature 双向回放；篡改一个字符或改 system 前缀后被上游拒绝；工具名和参数字面量不变；CRLF；3xx/4xx/5xx 的状态码、正文和头；Models 分页；count_tokens；Files 上传和下载；流式与非流式计费入账。只看到 200 不算通过。

2026-10-16 的 `.17` 发布例外：用户当次明确“我没有这个第一方 Key，要这个干啥？目的是啥，这个 patch 打进去了就行，不用再重复测试验收”，因此本候选不再执行上述真实第一方验收，记录为 `excluded_by_owner`，不记 passed。本任务不为任何账号开启严格透传；现有 CI 和合成证据保持原结论。该例外仅绑定 `c058cf8d277d3e9725eb3b1329e36eefdeb79ca7` / `sha256:2fb6369c592b2bb37856239eacf8c87c32eecf4741f99fe23346f8cedbc52547`，不证明真实上游的签名回放行为，也不单独构成生产发布授权。完整记录见运维仓 `docs/reports/supplier-strict-v17-20261016/`。
