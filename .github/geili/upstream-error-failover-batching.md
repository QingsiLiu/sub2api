# 上游错误内部重试归集（2026-10-17）

## 背景

2026-10-17 排查用户 3885 的 502：其中"流式输出中途断开"（`上游流式响应意外中断`、`upstream stream disconnected`、`Upstream HTTP/2 stream failed`、`Upstream response did not complete`）属于**已向客户端输出内容后**才断，SSE 无续传，网关不能替用户重发。65 次该类最终失败中，57 次在几秒内由客户端（Codex / Claude Code）原样或近似重发并成功，2 次真正影响（14:48:40 Claude 无重发、15:12:21 gpt-官-oobb 小请求后无续）。该结论不改变代码；本批处理的是另一类**可以内部消化**的错误。

## 归集结论（72h 生产最终错误，按可内部解决排序）

1. 欠费/额度账号还在调度：`882 plus-biuapi`、`5147 tj-safeai`、`5188/5189/6241 ecoapi`、`4730 plus-kedaya`、`5807 gm-ttx-02`、`5873 bjz-kedaya`（`Insufficient account balance`），`6275 gm-ttx-03`（`API Key 所属分组已删除`）。每次失败把它们**同号重复 4～6 次**再换号，换号上限 10 个很快耗尽；它们仍按优先级在大部分请求里被先选中。已于 2026-10-17 经 admin API 置 `schedulable=false`（未停用、未清分组）。用户要求：这些"欠费"可能是上游的上游造成的，不能自动持久停号，充值后应能马上回到调度。
2. Anthropic 兼容中转把**供应商 key 额度用完**报成 400（`You have reached your specified API usage limits` / `workspace API usage limits` / `credit balance is too low`），把**不支持该模型**报成 404 `model_not_found`。原实现 400/402/404 不换号，直接透传给用户。ps 等自有链路会轮换自己的 key，重试即可成功。
3. OpenAI 路径/chat 路径的**账号槽位等待超时**（`Concurrency limit exceeded for account`）：多来自粘性会话或池模式同账号重试落到已饱和的账号。原实现直接 429/502，不让这次请求换号。
4. 单次尝试即最终失败且客户端有正确反应、不改：容量降载 `Our servers are currently overloaded`（已改写成可重试码，Codex 自己会退避重试）、流中途断开（无法重放）、`xAI 400`、`cyber_policy`、参数/schema/上下文窗口错误、`Request payload is too large`、Gemini 原生 `anthropic_version` 参数错误（属原生适配批次）。

2026-10-17 下半天该用户 502 流中断只剩个位数；`specified API usage limits` 当天 26 次，均来自已停用的 6315。

## 第一批（本批）

- **Anthropic 账号相关失败换号**（`gateway_anthropic_account_failure_geili.go`）：只识别结构化 `error` 字段里的已取证模板，任何提示词/HTML/纯文本正文都不作证据。400/402 的额度文本与 400/404 的 `model_not_found` 包装成账号级、请求内排除的 failover：当前请求立即换号，不触发同号临时封禁，账号状态仍走既有 `HandleUpstreamError`（手动欠费管理不变，池模式照旧不写状态）。严格透传账号同样先把**原生字节**换号重发，耗尽后仍原样回写最后一次上游错误。
- **OpenAI 槽位饱和换号**（`openai_slot_busy_failover_geili.go`，挂在 `/v1/responses` 与 `/v1/chat/completions` 循环）：账号槽位等待超时或等待队列满时，不立即写 429，而是把该账号加入本次请求排除集重新选号，最多 3 次。全部耗尽时写出的仍是原来那句并发错误（同一状态码与正文）。客户端断开不触发；图片/embedding 等其他端点不开启。
- 未改动：换号上限、同号池模式重试次数、恢复预算、计费、账号持久状态。

## 验收

- 本地：新增分类负例（普通 400、其他 404、仅提示词里出现额度文本、纯文本、404 额度不误判）、严格/非严格透传换号与耗尽回写、槽位饱和换号/预算/队列满/客户端取消/准入清除，均通过。
- `go test -tags=unit ./internal/service/ ./internal/handler/... ./internal/server/...` 与无 tag 的 `./internal/service/ ./internal/handler/` 全部通过；`gofmt` 无差异。
- 未执行：Stage/生产部署、真实供应商与支付调用。版本号未分配，由发布排期统一编号。

## 后续候选建议（未实现）

- 换号耗尽时若失败账号里含"本轮被跳过的饱和账号"，可对它们做短冷却，避免下一个请求再次先选中。
- 中转欠费文本若出现新的固定模板，按同一"结构化字段 + 精确模板"方式扩展，不做模糊匹配。
