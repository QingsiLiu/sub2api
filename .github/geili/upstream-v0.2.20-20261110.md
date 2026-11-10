# 上游 v0.2.20 合并（2026-11-10）

## 固定输入与范围

- 官方最新正式 Release：[v0.2.20](https://github.com/Wei-Shaw/sub2api/releases/tag/v0.2.20)，发布时间 `2026-11-07T06:31:24Z`，固定提交 `dab829a4e9b7d1cc307189aea8b0319eb29e29d0`。
- 上次官方基线 `v0.2.19` / `dece26153621959ccd7635814f4b2d08304e681a`；两标签之间112个提交、459个文件、29482行增加／1457行删除。[官方完整差异](https://github.com/Wei-Shaw/sub2api/compare/v0.2.19...v0.2.20)。不包含标签之后的 upstream/main 提交。
- 合并前主干 `2972bd0f578aecbee9f7eb3b00e02fffe70a0d55`，包含尚未部署的管理员开票前后端与 `285_invoices_geili.sql`。当前生产固定 `.19.5` / `336519afc63f932f6e23c65db95b41dff05989a5` 是该主干祖先，合并继续保留两者。不使用 fork 的归档 main。
- 新候选 `0.2.20-geili.1`，仅源码整合与候选构建；未部署 Stage／生产，不启用 MCP 或修改生产配置。

## 用户可见变化

| 范围 | v0.2.20 的变化 |
| --- | --- |
| 管理能力 | OAuth + PKCE 管理 MCP、授权确认与连接管理；`MCP_PUBLIC_URL` 显式配置后才启用。管理员 Key 增补统计、OAuth、邀请等权限和需确认文字的全权模式。 |
| 注册管理 | 精确邮箱黑名单，最多20000条，归一化 Gmail 点号／加号变体，可批量禁用命中用户。 |
| 图片 | OpenAI 按1K／2K／4K账号分流及偏好回退，修正会话粘性；GPT Image 2.6 原生生成／编辑及参数支持；修复异步图片内存租约过早释放。 |
| 稳定性 | 共享请求体预算、解压／补丁内存计量、上传／下行单次读写超时、有界 SSE 事件与队列、有界会话缓存及通知工作池；区分408／499／截断／解码错误。 |
| 协议兼容 | OAuth Responses 非流式 incomplete／cancelled 返回JSON、空instructions透传及回显、WS桥接指纹／首token、长会话cyber误拦截、reasoning及工具媒体保留、Cloudflare REST beta参数兼容。 |
| 调度与用量 | Kimi并发403短冷却，Antigravity图像500只惩罚模型，真实配额窗口／Argon展示，测试使用管理员提示词；用量导出1000条分页并遵守429 Retry-After。 |
| 模型与依赖 | Terra 500k目录／effort／缓存键，GPT Image 2.6价格回退，vue-i18n 11及依赖修复。 |

## 需要在同 digest Stage 重点验收的行为变化

- 新增 `gateway.request_body_memory_budget_bytes`（默认1GiB），容量不足返回503 + Retry-After；设0关闭新增预算。官方Release说明写作security，但实际源码、EDGE_SECURITY.md和示例配置均为gateway，部署以源码为准。
- 新增上传读取与下行写入单次停滞限制（默认各120秒，0关闭）；不是把整个长流截为120秒。已有Geili文本准入预算和长思考／恢复期限仍保留。
- 客户端断开后的上游回收有总时限，连续ping不能无限续命；可能只取得部分用量。保留已有观测用量唯一结算、派发不确定性及输出后禁止重放，不把截断改签成功。
- Terra OAuth／setup-token 不再收长上下文附加费，API Key越过272k仍按上游2x输入／1.5x输出规则；minimal effort返回400。自定义分组价卡、余额／订阅独立倍率及零倍率保持，但该上游自动计价行为需重新验收。
- 新增官方迁移 `249_mcp_oauth.sql`。合并前352份SQL（含主干待验开票迁移）全部字节不变，候选共353份；相对生产351份还包含未部署的开票迁移，不能只验MCP表。
- Codex使用配置采用上游新的API Key默认认证、live搜索与图片配置，保留Geili统一 `gpt-6-astra` 默认模型、远端目录发现和复合Key导出；兼容认证仍可切换。

## 冲突整合

- 29个冲突文件逐项处理。Wire从合并后的provider graph重新生成；审计、财务worker、开票、MCP和新的缓存／通知cleanup同时保留。
- 认证快照升至28，避免与上游／旧Geili同名27快照混用；包含分组privacy标记和RPM覆盖查询状态。
- 请求体共用上游有所有权的chunk／解压／组装路径，同时在分配前保留Geili准入与解压工作区预留；新增预算错误保持503，不能被Geili预读吞成400。
- SSE边界同时应用上游事件／队列预算与Geili首语义内容／完整终态检查；严格透传保留CRLF原始字节。Geili读取泵通过统一deliver释放队列预算，避免满预算后自锁。
- 调度保留加密历史所有者、compaction能力和订阅绑定，同时合入图片尺寸分层与粘性回退。
- HTTP派发边界继续先检查已取消Context，再记录派发，同时传递新增断连回收期限。恢复Context保留原期限，不让新的detach移除它。
- Antigravity内部探测适配新增prompt参数；Gemini原生转换复用已保留输出前缀计算增量，避免依赖被删除函数或另存一份完整文本。
- 通知工作池中已接受／排队工作纳入Geili自然排空计数。
- 内部缓冲读取器失败返回已观测用量但仍返回错误；公开缓冲请求在Geili转发守卫下继续保持原有无计费结果边界，由外层统一处理错误；首内容前的安全failover保持未提交HTTP响应。上游身份头测试使用完整response.completed夹具，API设置快照增补GPT Image 2.6；测试不再把只有[DONE]的非流式响应当成功。严格透传测试继续断言不自动加beta或应用兼容请求头覆写；普通转发／账号测试应用上游Cloudflare URL规则。
- vue-i18n升级后订单／收据测试按真实译文断言，避免依赖旧版运行时未编译消息时的裸key回退；业务页未作额外改写。
- 本地既有 `sub2api-strict-passthrough-patch/` 未跟踪目录保持，不提交；不提交设计稿、密钥或前端编译产物。

## 验证与制品

固定源码revision `a3f8e240e85bc97e92569c549a4ab98b2b1e56a9`，已推送 `origin/geili/main`；包含同步合并`8875da2de7b1e2e3c1b4f9a4d19b9735f6edd53f`及后续边界适配，合并提交的双亲为原主干与官方固定标签。本次最终源码验证全部通过：

- `go test ./...`、`go test -tags=unit ./...`、`CI=true go test -tags=integration ./...`。
- httputil／service／routes／middleware／repository五包专项竞态，覆盖内存／解压、SSE限界／用量、不可逆操作、严格透传、订阅准入／派发、图片尺寸等交叉边界。
- 前端428文件／3564项、生产构建与无旧视觉覆盖检查；5组离线Python共103项。
- 嵌入前端测试、带版本／提交信息的完整嵌入服务编译、自更新禁用守卫。

这些是本地测试结果，不是同digest Stage或远端13门禁凭据。

候选镜像未生成：GitHub Actions手动dispatch两次均返回HTTP422 `Actions has been disabled for this repository`。只读配置却显示仓库`disabled=false`、Actions `enabled=true`，workflow本身active；对本候选workflow执行enable后仍同样拒绝，没有修改仓库Actions权限配置。故记录为远端执行受阻，不猜测是否为账号／平台策略，也不标记CI门禁通过，digest为未生成。未继承任何历史Stage／性能／生产例外；正式发布仍须同digest Stage、当前资源／迁移兼容性验收及当次生产授权。


原始失败保留：首次接口签名／Gemini helper编译错误、旧身份头夹具仅[DONE]、设置模型快照、i18n裸key断言、缓冲读取器用量／严格透传与公开结算边界冲突，以及并行编译时两项10ms Redis presence hint测试失败均单独保留。Redis两项已单独复测通过；不扩大生产时限来迁就测试。最终修正版本三套全量重新执行全部通过；旧失败文件保持，不能改写为passed。原始及最终日志保存在被忽略的`deploy/.secrets/upstream-v0.2.20-20261110/`，`evidence.json`包含源码身份及逐文件SHA256。
