# 文本思考模型等待预算

`IsLongThinkingRequestModel` 只决定等待预算，不决定模型授权、协议、签名或计费。流式空闲检测与复合 Key 总请求预算共用此判断，避免只扩大其中一个后仍被另一层提前结束。

## 覆盖规则

- Claude：Sonnet 3.7、Opus/Sonnet 4 及以后、Haiku 4.5 及以后，以及现有 effort 目录中的 Fable/Mythos 等模型。
- OpenAI：GPT 5 及以后（含 Sol、Luna、Astra、Codex、mini/nano 和日期变体）、o 系列，以及 Codex 文本别名。
- Gemini：2.5 及以后文本模型；Grok：3-mini、4 及以后文本模型。
- DeepSeek：reasoner/R1、V3.1 及以后；GLM：4.5 及以后；Kimi：K2.5 及以后；MiniMax：M2 及以后；Qwen：Qwen3 及以后，显式 coder/instruct 名称保留普通预算。
- 其他明确带 `thinking`、`reasoning`、`reasoner` 或 `r1` 分段的文本模型也使用思考预算。
- 识别大小写、供应商路径前缀、Bedrock 地区前缀和日期变体。版本族按数字比较，无须每发布一个补丁版本再添加固定名单。
- 图片、音频、视频、embedding、rerank 等媒体名称及显式 `non-reasoning` / `non-thinking` 不进入此规则；名称判断不验证或授予模型能力。

自定义公开别名仍按已有路由解析其渠道和账号映射；空闲检测使用实际发出的上游模型，不能拿同组其他模型推断。Antigravity 同步设置本次上游模型，覆盖 Gemini、Claude、原样透传和 Chat/Responses 转换，以及流转非流收集。

## 时间边界

- 普通上游读空闲默认 180 秒；思考模型取普通值与 `gateway.long_thinking_stream_data_interval_timeout` 的较大值，后者默认 600 秒。上游每次有可读取的流数据才刷新空闲计时，下游心跳不刷新。
- 普通检测设为 0 仍保持关闭，思考检测设为 0 仍退回普通预算。Grok 原有强制有界检测例外保持，不产生无限连接。
- 复合 Key 普通总预算默认 600 秒，思考模型默认 1800 秒。显式配置的普通总预算（包括显式 600 秒）和更短的父/客户端截止时间继续优先；恢复窗口及部分输出禁止重放保持原规则。
- Antigravity 兼容流的默认首内容截止时间同步覆盖思考空闲预算，避免旧的 120 秒首内容上限先行中止；内部调用者显式传入的首内容上限仍保留。

## 回归入口

`long_thinking_models_geili_test.go` 覆盖版本族、命名变体、媒体/非思考排除和配置边界；`long_thinking_stream_regression_geili_test.go` 以延迟管道覆盖八种实际读取入口、别名、心跳下的有界超时及 Antigravity 首内容截止时间。路由和恢复测试验证总预算、显式配置与渠道/账号映射。无需真实供应商请求或费用。
