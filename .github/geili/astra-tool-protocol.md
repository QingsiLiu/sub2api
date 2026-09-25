# GPT-6-Astra 工具协议保护

## 已确认的故障

2026-09-25 14:13:26（北京时间），生产请求入站为 `/v1/responses`，账号 `6098` 的 `openai_responses_supported=false` 使其被降级到 `/v1/chat/completions`。上游以 `Function tools with reasoning_effort are not supported for gpt-6-astra` 返回 400。因此要求客户改用 Responses 无法解决问题，客户已经使用该入口。

此前经用户授权，账号 6098 于 14:52:26 暂停调度。2026-09-25 16:52:06 的只读复核显示，该账号仍为不可调度；暂停后受影响客户已有 2,680 次 Astra 成功用量，实际出站全部为 `/v1/responses`；同一时间窗全站同类协议错误计数为 0。此为观察窗口证据，不代表源码已部署或所有其他错误消失。同期生产容器为 healthy，版本 `0.2.8-geili.5`，revision `9982f97619f65d1b8ff92aca4bd90f3ac8868dc0`，已确认是候选源码的祖先。

## 保护边界

实现集中在 `backend/internal/service/openai_tool_protocol_geili.go`，官方调用点保留 `geili hook`：

- 三个 HTTP 入口在策略处理后记录工具及推理要求；支持顶层工具、旧版 functions、Codex 动态 additional_tools。
- 调度按最终上游模型判断，渠道别名、账号别名及 Messages 默认映射不能绕过规则。普通/高级调度及粘性选路共用保护。
- Astra 工具请求要求 Responses 能力；Sol/Luna 延续已有的显式 `none` 例外。纯文本、其他模型及非 OpenAI 平台保持既有行为。
- 共享 Chat 出站在网络请求前复核最终请求体。协议不匹配返回可切换账号的结构化错误，不改工具或推理、不提前写出响应、不在同一账号重试、不惩罚账号健康。
- 只剩 Chat-only 账号时返回服务暂不可用（503），不会给客户返回原来的上游参数错误。
- WebSocket 入口原有 Responses 能力约束保持不变。本次不修改第三方上游内部路由，也不把未验证的能力标记强制改为 true。

## 验证

```sh
cd backend
go test -tags=unit ./internal/service ./internal/handler ./internal/pkg/apicompat ./internal/pkg/openai ./internal/pkg/openai_compat
go test -race -tags=unit ./internal/service ./internal/handler -run 'TestGeiliToolProtocol|TestGPT6RawChatRejectsReasoningToolCalls|TestOpenAIGatewayHandlerResponses_AstraPro' -count=1
```

HTTP 回归覆盖三个入口 × 流式/非流式 × 真实模型名/渠道别名，确认最终 URL 为 `/v1/responses`、工具名称及推理力度保留，并在账号切换预算为 0 时仍能跳过不兼容账号；无兼容线路时确认 503 及零上游调用。账号映射、普通/批量/高级调度器、粘性会话、Messages 分组策略、共享出站和健康计分有独立回归。

本地上述五个后端包完整单元测试及 Astra 专项 race 检查均通过；补充的批量调度 race 回归也通过。GitHub 全仓候选门禁另行记录在发布说明中。

验证中的无线路测试补齐 `ListModelAvailabilityCandidates` 仓库夹具，明确排除 panic 兜底响应误算通过。

## 发布与恢复

版本、revision、不可变镜像 digest 和部署状态以 `RELEASES.md` 为准。本候选 revision `9d51db957954ddc01c25f4eca5ab3b19e8167f25`、镜像 `ghcr.io/qingsiliu/sub2api@sha256:22187d5bb250ab20b36a7814ceefe612d028faa697af7f9adbebc993ab12b750` 已通过前端、Go 默认/单元/集成、订阅竞态和 PostgreSQL 支付门禁；2026-09-25 17:05 已部署 Stage，55项 Astra 真实HTTP模拟验收和281项通用业务全部通过：三个入口的原名/账号别名、流式/非流式均跳过高优先级 Chat-only 账号，保留工具和 high 推理；无兼容账号503且零上游请求，纯文本保持兼容。配置已恢复、合成夹具停用、生产指纹未变。候选仍标记 `production_ready=false`，本候选源码新编译的本地双实例/Redis故障恢复15项也全部通过，待外部沙箱/限额联调和生产授权；详细记录在运维仓 `docs/reports/astra-tool-protocol-stage-20260925.md`。生产目前仍靠账号暂停止血；永久保护须走候选 CI → Stage 验收 → 当次用户授权后同一 digest 上生产。

不自动恢复账号 6098。先验证其 Astra Responses 工具与推理组合，再按运维仓的变更记录及用户授权恢复调度。历史其他模型的 Responses 成功记录不足以证明 Astra 兼容性。
