# Luna Chat Completions 参数修复（2026-10-20）

客户反馈官 Key 分组的 Luna `max_completion_tokens` 未生效，GPT-5.6 Luna 在关闭思考时的 `temperature` / `top_p` 也未生效。没有客户原始请求；本次使用自构造请求、最终出站正文捕获及模拟上游回归定位，不把截图中的来源猜测视为事实。

## 已复现的源码问题

- Chat Completions 转 Responses 的通用转换器会把输出上限抬高到默认 128（或管理员配置的下限）。旧代码中 `max_completion_tokens=16/32` 实际都以 `max_output_tokens=128` 出站，违背显式上限语义。
- 转换器将 GPT-5.6 Luna 归入一律过滤采样参数的思考模型；即使 `reasoning_effort=none`，仍删除 `temperature` / `top_p`。GPT-6 Luna 已有对应例外。
- 官方 API Key 的原生 Responses 请求使用 `reasoning:{"effort":"none"}` 时，当前源码会保留该值；本次回归确认其没有被网关删除。没有证据把客户关于“Responses 关不了思考”的现象归因于同一问题。

官方契约：[Chat 参数上限](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create)、[GPT-6 Luna](https://developers.openai.com/api/docs/models/gpt-6-luna)、[GPT-5.6 Luna](https://developers.openai.com/api/docs/models/gpt-5.6-luna)。输出上限同时计入可见文本与思考 token；关闭思考用 Chat 的 `reasoning_effort:"none"` 或 Responses 的 `reasoning:{"effort":"none"}`，Responses 上限字段为 `max_output_tokens`。

## 修复范围

- 官方 `api.openai.com` API Key、自动协议模式、最终映射为两款 Luna 且显式 `reasoning_effort=none` 的 Chat 请求，走现有原生 Chat 转发路径，保留 Chat 参数和小于 Responses 最低要求的上限。复用原鉴权、模型映射、计费、快车策略、工具协议守卫和流式处理。
- OpenAI API Key 仍需转 Responses 时，正整数 `max_completion_tokens` 优先于 `max_tokens`，精确转换为 `max_output_tokens`，不再静默提高。若强制 Responses 且上限不符合上游最低要求，由上游校验，不能去掉或放大上限后重放。
- API Key 转换路径在两款 Luna 关闭思考时保留采样参数。开启思考时的过滤保持。
- 显式 `force_responses`、自定义供应商地址、OAuth/SetupToken、Responses 形状误发到 Chat 的请求及其他模型维持原协议选择；旧 `max_tokens` 转换下限保持。没有修改账号、分组、定价、迁移或生产配置。

## 验证与限制

- 修复前新增回归确实失败，捕获到 16/32 → 128 以及 GPT-5.6 Luna 采样参数丢失；修复后通过。
- `TestGeiliLuna*` 覆盖两款模型、Chat 流式/非流式、别名映射、16/32/256 上限与优先级、采样参数、原生 Responses `none`、官方域名/协议/凭证类型边界、显式配置下限和缺省上限。
- 定向 `-race -tags=unit` 同时验证新用例、原 GPT-6 工具协议守卫、`none` 过滤与客户端断开处理。
- 真实上游未验收：读取专用测试 Key 所需的 `OVHDe` 跳板和直连 SSH 均在 banner 交换阶段超时；没有复制供应商主 Key，没有使用客户 Key，没有发生真实模型调用。不把模拟结果写成上游实测。
- 候选身份、完整 CI 及部署状态以 `RELEASES.md` 顶部为准；本任务未获生产发布授权。
