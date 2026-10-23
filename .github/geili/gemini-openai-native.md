# Gemini 原生 OpenAI 协议适配

第二批候选`.20`（撤回有可空参数兼容回归的`.19`）继承`.18`供应商故障恢复；原生产基线`.17`保持祖先。Gemini Responses明确分流到Gemini原生接口，Chat文本请求共用转换。媒体生成保持旧入口，不把该能力扩张成媒体恢复或有状态会话服务。

## 请求

原生请求只包含`contents/systemInstruction/tools/toolConfig/generationConfig`等Gemini字段，实际认证、代理、模型映射、AI Studio/Code Assist/Vertex请求构建复用既有实现。Responses及Chat原始JSON均保留到共享原生请求表示；不经Anthropic Messages序列化，不把reasoning_content伪装成用户提示词。

支持文本、多轮system/developer/user/assistant、现有图片输入、函数声明、工具选择、function_call/function_call_output及Chat工具消息。Codex客户端工具复用既有降级及名称恢复。函数签名以`gemini_thought_signature`扩展保留在Responses函数调用项和Chat工具调用项；客户端需要在完整历史中原样回传。供应商提供的函数调用ID也必须无损往返，同名并行及逆序回传按ID关联。

### 历史转换与续传（0.2.17-geili.6）

工具降级及名称映射后，共用历史构建器按 `call_id` 验证调用和结果恰好一对一；支持同名并行调用与逆序结果，空字符串结果有效。缺失、孤立、重复结果及重复调用 ID 在供应商调用前返回现有 `400 invalid_request_error`，不删除调用或伪造结果。

Responses 历史 `reasoning.summary` 验证后仅作为展示元数据，不构造原生 `model/thought` 轮次，与 Chat 的历史思考处理一致；OpenAI 加密思考仍拒绝。函数调用项上的 Gemini 签名原样保留，不从思考摘要迁移或生成签名。空文本、纯空白和空内容数组不形成轮次，非空文本及图片保持顺序；system/developer 抽取到 systemInstruction 后才判断尾轮。

工具结果已全部返回且尾轮有助手文本时，保留历史并追加一次原生 user 指令：`Continue and complete the previous assistant turn from where it stopped. Do not repeat content already provided; use the tool results already present.`。这是新一轮语义续写，不是字节级 prefill、输出拼接或去重。有效 user 尾不追加；没有有效内容、缺少工具结果或只有模型媒体尾轮时出网前拒绝。

本修复仅覆盖 Gemini OpenAI 共用文本转换器，不改变原生 Gemini/Messages、工具生成模式、错误提示、等待预算、恢复边界或结算规则，不声称消除所有 MALFORMED_FUNCTION_CALL。Stage 使用主动拒绝非法尾轮/空轮次/不完整工具配对的自有模拟上游；不把合成用例当作用户历史请求的逐笔复现。

JSON object及已支持的JSON schema转换为Gemini MIME/schema配置，不用提示词模拟约束。严格schema仅接受可无损表达子集，不可表达的约束出网前400；工具参数schema与文本输出schema分开验证。工具`strict:true`在默认/auto选择下使用原生`VALIDATED`，required/指定函数保持`ANY`，none保持`NONE`；原生约束为请求级，混合声明也不放宽严格工具。Chat stop转换为原生stopSequences。预算按当前接口优先级解析，显式正整数原样发送，0/负数拒绝，缺省8192；不再把1抬为128。原有Responses的messages/prompt兼容保留，原生input优先。既有可空SDK顶层参数null按未提供处理，预算默认8192；原拒绝的零/负数、错误类型和无法表示的能力保持。

思考配置只按映射后的已知型号转换，费用标记来自实际转发参数。未知别名（含未解析至已知型号的argon）不猜基础模型；未给effort沿用供应商默认，无法确定支持的显式effort明确拒绝。

采用完整历史、无状态请求。`previous_response_id`、`store:true`、OpenAI加密推理、无法表示的工具、参数和输入类型明确拒绝；不静默丢弃、不创建会话存储。普通远程图片按原生fileData语义处理；原生FunctionResponsePart不能表示的远程工具图片不自动下载，明确拒绝。

## 响应与失败

直接从原生parts构建Responses输出与事件，再转换Chat消息/chunks。保留response/item/call ID、递增sequence、工具参数、签名和usage。STOP为completed；MAX_TOKENS为incomplete（Chat length）；内容过滤保留其明确终止语义；空STOP、缺finishReason、坏JSON、错误包或断流不伪造成功。

成功终止在完整EOF和终止校验后发送。无首内容故障可以在原请求预算内恢复，只有中性心跳不阻止切换；已有客户端正文、工具参数或不可重放副作用后禁止重放。非流式“上游已生成内容”不等于“客户端已收到内容”。显式关闭空闲检测、原长思考预算和较短客户端截止时间保持。恢复只结算最终有效结果；已交付部分输出沿用部分用量计费，不重复结算。

协议依据：[Responses事件](https://developers.openai.com/api/reference/resources/responses/streaming-events)、[Gemini generateContent字段](https://ai.google.dev/api/generate-content)、[结构化输出](https://ai.google.dev/gemini-api/docs/structured-output)、[原生函数模式](https://ai.google.dev/api/caching#FunctionCallingConfig)。供应商返回的modelVersion不作为真实基础模型身份的独立认证。

## 验收状态

候选CI及当前digest的Stage结果完成后填写RELEASES.md。新Stage runner仅访问namespace mock及合成夹具，覆盖双实例、双协议、流式/非流式、工具回放、schema/stop/预算和故障终止；业务、计费、双实例缓存及4×1000性能分别完整执行。真实供应商、支付、媒体生成未执行，不计通过。当前文档不是生产发布授权。
