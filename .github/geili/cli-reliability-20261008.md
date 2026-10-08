# 2026-10-08 原生 CLI 可靠性排查与修复

## 范围与证据边界

对客户反馈的 91 条 CLI 失败正文，在生产应用、只读数据库、响应审计、Nginx 日志和当前源码中交叉排查。生产程序为 `0.2.14-geili.1`，revision `7187d0912ded884b4ef6b52b8224f82af07cf785`，镜像 `sha256:ebc09e916b0cf9cbdd0d8d2be5074872126112af8d9bb2600b5571d49b3b4bef`。源码主干仍包含该 revision，无数据库写入、客户配置写入或历史退款。

已搜索本机 code / Documents / Downloads 的任务名与 CLI 日志文件，未找到对应原生测试产物。生产记录可确认下列实例与错误链，不能把网关内部重试次数等同于 91 个最终任务，不能据此计算客户失败率。长思考本身与供应商挂起在静默期间不可仅凭没有字节区分；延长超时是有界缓解，不证明所有超时都由正常思考造成。

## 分类结论

| 反馈类别 | 实际证据与原因 | 本次处理 / 剩余边界 |
| --- | --- | --- |
| 27 条上游 180 秒静默 | 网关普通上游读 watchdog 默认 180 秒，keepalive 只保下游连接，不制造上游数据。长思考模型适用同一预算；供应商第一数据超时也可触发换号。 | Claude 5.5 / Astra 独立默认 600 秒，上游计时不被心跳重置，检查步长最多 10 秒，仍保留挂死上限。普通模型保持 180 秒；配置 0 可以取消扩展，普通 watchdog=0 仍禁用。 |
| 21 条 Codex 缺完成事件 | native Responses 的 Codex 错误终态处理只对 OAuth-like 账号启用；API Key 的 bare error 可直接透出，CLI 随后报告缺 `response.completed`。这证明存在协议缺口，不证明 21 条全由此造成。 | 所有 native Responses 按协议合成 `response.failed`；保留原错误规则、成功权威终态和 usage。裸 error 后等待最多 2 秒，供应商注释心跳不能延长；部分输出后不换号重放。 |
| 截断、暂不可用、空事件、泛化上游失败 | 供应商响应没有正常终态或传输失败；仅错误正文不能判断哪个网络跳点断开。生产可见内部 failover/first-data timeout。 | 保留输出前有界 failover；输出后以真实失败终态结束，不伪造成功。供应商持续不可用仍会失败，需要后续线路容量/网络观测。 |
| 1 条 524 | 精确匹配 CF Ray 与生产 Nginx，见下一节。 | 提供关闭 Messages / CC / Codex 别名请求及响应缓冲的运维补丁；尚未修改生产。 |
| 18 条 Opus thinking signature | 精确匹配代表错误中的 Bedrock RequestID，网关先超时换号，再被聚合供应商返回 400 signature 校验失败。该供应商以 API Key HTTP 访问，不能误认网关走原生 Bedrock 适配。生产没有 rectifier 设置记录，默认 APIKeySignatureEnabled 实为 false。 | 默认开启 API Key 签名恢复，显式 false 保留，旧 JSON 缺字段采用新默认。5.5 收到明确校验错误后移除整个历史 thinking/redacted_thinking 链并仅额外重试一次，保留当前 thinking 模式和工具 JSON。不能仅凭截断后的 Bedrock 消息确认签名为何不匹配。 |
| 9 条 Astra instant quota | 同一线路出现该精确供应商错误，包装为 HTTP 400/502 或流内 error；原分类漏掉即时推理配额语义，裸 error 未必切号，也没有统一 429 冷却。同期该线路另有大量 upstream concurrency-limit 429；本地并发设置为 8，不能凭它推算供应商实际容量。 | 只匹配 error 字段里的精确即时配额消息，HTTP/SSE/WS 统一可恢复 429。输出前切号，禁止同号重试，池模式及 OAuth 也使用有界临时冷却（沿用管理员 429 fallback 设置；整数 Retry-After 有上限）。不标永久 auth、不修改用户余额。 |
| 3 条 context_management | 属于此前修复前记录；当前兼容剥离已有实现，本次查询的当日窗口未再匹配。 | 保留现有兼容逻辑与回归，不重复改参数语义。 |
| 2 条超过 50 张图 | 精确找到两条该客户请求的上游 HTTP 400，见下节。属于单请求输入限制；52 个日志标记不能证明 52 张不同图片。 | 当前 invalid_request 错误不应触发配额冷却或无意义重试。GUI 客户端需按图片块计数、压缩旧截图历史；网关不静默删除用户有效图片，无法从本仓修改客户运行器。 |
| 1 条 128000 输出上限 | CLI 自己报告 OutputTokenExceededError；请求 max_tokens 与 CLI 限制一致，代表一轮生成触及输出预算。 | 保留 max_tokens/真实 stop reason，不能加账户余额或盲目重放解决。客户端应分轮完成任务、续写或减少单轮输出；单纯提高请求参数不保证上游允许。 |

## 可复核的生产实例（均为北京时间）

### 签名实例

`ops_error_logs.id=5420722`，2026-10-08 12:31:46.491，网关 request `46ea053c-8006-400f-ade8-ebba4258a661`，Claude Opus 5.5 `/v1/messages`。

1. 账号 6315：first-data timeout，`protocol_completed=false output_started=false`，发生 failover。
2. 账号 5899：HTTP 400，Bedrock RequestID `ce19cbfe-1fba-23f9-fc43-60e48abdcd11`；与反馈的两个供应商 request id 一致。

这能确定「前一线路超时 → 换线路 → 带历史签名的请求被校验拒绝」，不能推出跨 Bedrock/Anthropic 的签名天生不兼容，也没有证据证明网关改了签名字节。官方说明签名与内容、会话前缀有关，修改历史后原样重试不能修复绑定；采用官方建议的 thinking 历史剥离恢复。参考 [thinking troubleshooting](https://platform.claude.com/docs/en/build-with-claude/thinking-troubleshooting) 和 [thinking signatures](https://platform.claude.com/docs/en/build-with-claude/thinking)。

### Cloudflare 524 实例

CF Ray `a47166f0ed571314-NRT` 精确匹配 `/var/log/nginx/sub.geiliapi.com.access.log`：

```text
ts=2026-10-08T09:36:21+08:00 status=499 req_time=125.007
upstream_status=- upstream_time=11.356 upstream_addr=127.0.0.1:8080
method=POST uri=/v1/messages?beta=true
req_id=d6c5caaa85280b9b573aa903e6b5909f
ua="claude-cli/2.1.287 (external, sdk-cli)"
```

499 是 Nginx 观察到 Cloudflare 关闭连接，不等同用户主动退出。125 秒中约 114 秒不在 upstream 计时内。`nginx -T` 确认生产 `/v1/messages`、`/v1/chat/completions`、`/backend-api/codex/responses` 落入默认 `location /`；只有 `(v1/)?responses` 明确关闭请求/响应缓冲、gzip。该结构会让慢上传在应用入口保活启动前先消耗边缘窗口。

同一客户 09:36:09.688 开始的一条 Opus 响应审计与应用计时相近，但没有共用请求 ID，未将它断言为这一条 524。无法仅凭这些元数据区分客户端上传速度、边缘传输耗时和其他应用前等待。

本机 Nginx 对照实验的上游在收到第一个请求字节后发送 ping，故意延迟最后一个上传字节。旧配置三条入口拿不到 ping；补丁后三条入口都能收到，既有 Responses 行为保留。它证明缓冲机制和补丁有效，不保证应用读取完整 JSON 前能响应；极慢的大请求仍可能触及边缘限制。Cloudflare 现行文档为默认 125 秒，客户收到的包装正文写 120 秒，以实际日志为准。参考 [Cloudflare 524](https://developers.cloudflare.com/support/troubleshooting/http-status-codes/cloudflare-5xx-errors/error-524/)。

### 图片实例

- `ops_error_logs.id=5397955`，2026-10-08 00:12:22.891，request `9b65648e-76ad-4a21-8067-c2779adfae06`。
- `ops_error_logs.id=5414078`，2026-10-08 09:43:13.417，request `0caa0962-850a-4bb4-954c-82c0f9dc572d`。

均为 Astra、账号 6316、上游 400、`invalid_request_error`、`param=input`、50 图限制。没有读取客户提示词、截图内容或私密工具参数。

## 实施与验收

主要新增实现收拢在 `*_geili.go`，官方文件只加调用钩子。没有新增迁移或计费写路径。回归覆盖 HTTP/SSE/WS 即时配额、无 code 的 WS 错误、API Key/池模式/OAuth 临时冷却、输出前切号与部分输出后禁止重放、旧错误透传规则、bare-error 短窗口、签名恢复最多两次总调用、工具大整数及 current thinking 模式、长思考延迟完成和闲置上限。

复现命令：

```bash
cd backend
go test -tags=unit ./internal/service ./internal/config -run 'Test(InstantQuotaGeili|APIKeyResponsesGeili|Claude55SignatureGeili|ClaudeSignatureGeili|LongThinking|OpenAIStreaming|Sonnet55Retry|OpenAIResponseFlush)' -count=1
cd ..
python3 .github/geili/check-cli-stream-nginx.py --ops-config /Users/tedliu/code/geili/subscription-lab-ops/deploy/ovh/sub.geiliapi.com.conf
```

入口补丁：[cli-reliability-nginx.patch](cli-reliability-nginx.patch)。它对运维仓 `deploy/ovh/sub.geiliapi.com.conf` 扩展既有无缓冲流式 location，未应用到服务器；静态页面、图片路由及 WebSocket 头保留。应用发布仍走候选 CI → 固定 digest 的 Stage → 用户当次授权的生产。Nginx 修改另需运维仓回收、`nginx -t` 和 reload 授权。

验证状态与候选制品记录在根目录 [RELEASES.md](../../RELEASES.md)。供应商全部线路同时不可用、长上传超过边缘窗口、输入/输出硬限制仍可能导致真实失败；本次改动改善恢复和错误语义，不能承诺消灭所有 91 条错误。
