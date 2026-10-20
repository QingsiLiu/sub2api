# Luna Chat Completions 参数修复（2026-10-20）

客户反馈官 Key 分组的 Luna `max_completion_tokens` 未生效，GPT-5.6 Luna 在关闭思考时的 `temperature` / `top_p` 也未生效。没有客户原始请求；本次使用自构造请求、最终出站正文捕获及模拟上游回归定位，不把截图中的来源猜测视为事实。

## 已复现的源码问题

- Chat Completions 转 Responses 的通用转换器会把输出上限抬高到默认 128（或管理员配置的下限）。旧代码中 `max_completion_tokens=16/32` 实际都以 `max_output_tokens=128` 出站，违背显式上限语义。
- 转换器将 GPT-5.6 Luna 归入一律过滤采样参数的思考模型；即使 `reasoning_effort=none`，仍删除 `temperature` / `top_p`。GPT-6 Luna 已有对应例外。
- 原生 Responses 对自定义上游地址一律把 `none` 当成目录占位值删除，即使最终模型是支持关闭思考的 Luna。带账号模型别名的两款 Luna 回归均捕获到 `reasoning.effort` 从 `none` 变成缺失；WebSocket→HTTP 的对象过滤使用相同规则。
- 官方 API Key 的原生 Responses 请求使用 `reasoning:{"effort":"none"}` 时，当前源码会保留该值；本次回归确认其没有被网关删除。没有证据把客户关于“Responses 关不了思考”的现象归因于同一问题。

官方契约：[Chat 参数上限](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create)、[GPT-6 Luna](https://developers.openai.com/api/docs/models/gpt-6-luna)、[GPT-5.6 Luna](https://developers.openai.com/api/docs/models/gpt-5.6-luna)。输出上限同时计入可见文本与思考 token；关闭思考用 Chat 的 `reasoning_effort:"none"` 或 Responses 的 `reasoning:{"effort":"none"}`，Responses 上限字段为 `max_output_tokens`。

## 修复范围

- 官方 `api.openai.com` API Key、自动协议模式、最终映射为两款 Luna 且显式 `reasoning_effort=none` 的 Chat 请求，走现有原生 Chat 转发路径，保留 Chat 参数和小于 Responses 最低要求的上限。复用原鉴权、模型映射、计费、快车策略、工具协议守卫和流式处理。
- OpenAI API Key 仍需转 Responses 时，正整数 `max_completion_tokens` 优先于 `max_tokens`，精确转换为 `max_output_tokens`，不再静默提高。若强制 Responses 且上限不符合上游最低要求，由上游校验，不能去掉或放大上限后重放。
- API Key 转换路径在两款 Luna 关闭思考时保留采样参数。开启思考时的过滤保持。
- 自定义地址的 OpenAI API Key 在最终账号映射为两款 Luna 时保留显式 `none`，HTTP 与 WebSocket→HTTP 一致；未知/非思考模型的目录占位过滤保持，不增加数据库或网络读取。
- 显式 `force_responses`、自定义供应商地址、OAuth/SetupToken、Responses 形状误发到 Chat 的请求及其他模型维持原协议选择；旧 `max_tokens` 转换下限保持。没有修改账号、分组、定价、迁移或生产配置。

## 验证与限制

- 修复前新增回归确实失败，捕获到 16/32 → 128 以及 GPT-5.6 Luna 采样参数丢失；修复后通过。
- `TestGeiliLuna*` 覆盖两款模型、Chat 流式/非流式、别名映射、16/32/256 上限与优先级、采样参数、官方与自定义地址原生 Responses `none`、对象过滤、官方域名/协议/凭证类型边界、显式配置下限和缺省上限。
- 定向 `-race -tags=unit` 同时验证新用例、原 GPT-6 工具协议守卫、`none` 过滤与客户端断开处理。
- 现网旧版真实复现已完成，见下节；候选尚未部署，不能把旧版真实复现写成候选真实上游验收通过。
- 候选身份、完整 CI 及部署状态以 `RELEASES.md` 顶部为准；本任务未获生产发布授权。

## 官转分组真实复现（生产 `.1`，8 次短文本）

SSH 访问及本地保存的旧登录凭据不可用后，经浏览器现有登录会话创建本人专用 `luna-param-check-20261020`，仅绑定“GPT 官转”、余额结算、额度 `$0.10`、2026-10-20 19:00（上海时间）过期。没有复制供应商主 Key，也未使用客户 Key。自造题目要求用英文列出 1～1000，所有请求均显式关闭思考，分别用 Chat `max_completion_tokens` 和 Responses `max_output_tokens` 控制上限。

| 模型 | 协议 | 请求上限 | 实际输出 token | 思考 token | 结果 |
| --- | --- | ---: | ---: | ---: | --- |
| gpt-6-luna | Chat | 32 | 128 | 未在兼容回包中提供 | length |
| gpt-6-luna | Chat | 128 | 128 | 未在兼容回包中提供 | length |
| gpt-6-luna | Responses | 32 | 32 | 32 | incomplete，正文空 |
| gpt-6-luna | Responses | 128 | 128 | 118 | incomplete |
| gpt-5.6-luna | Chat | 32 | 128 | 未在兼容回包中提供 | length |
| gpt-5.6-luna | Chat | 128 | 128 | 未在兼容回包中提供 | length |
| gpt-5.6-luna | Responses | 32 | 32 | 32 | incomplete，正文空 |
| gpt-5.6-luna | Responses | 128 | 128 | 128 | incomplete，正文空 |

全部 HTTP 200，单次约 1.2～1.9 秒。真实结果与两处已复现的网关改写吻合：Chat 32 被抬到 128；Responses `reasoning.effort=none` 没有实现关闭思考。仅凭下游回包仍不能断言供应商自身是否存在额外问题，也不能证明具体客户原请求的路由。

测试 Key 已立即禁用，页面用量显示 `$0.0030`；期限与 `$0.10` 上限保留。原始自造请求/响应、汇总与停用截图保存在忽略目录 `deploy/.secrets/luna-parameters-20261020/`；没有应用发布或客户配置改动。

补充对照曾临时恢复同一受限Key，尝试一次“Responses正文发到Chat URL”的既有兼容入口；未取得有效JSON，脚本解析失败后停止，没有重试，没有继续第二款模型，结果不计通过。随后再次确认Key已停用、当前并发0、页面用量仍为`$0.0030`，本地测试Key文件已删除。该对照不替代标准接口8次原始复现，也不作为候选真实上游验收。
