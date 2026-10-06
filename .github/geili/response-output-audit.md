# 响应输出审计（观察版）

## 2026-10-06 接受的边界

本次只观察、标记、统计，不改变任何请求的现有计费。即使确认没有可用输出，也保留原有客户扣费、Key/套餐额度和上游成本。历史那笔费用不处理；部分输出后失败继续原规则。账户恢复后的调度、重试与换号策略不在本次修改范围。

不把 output_tokens=0 当作免费或空结果依据。用量可能缺失，零 token 也可能有完整工具调用。可读思考属于有效输出。网关 Write/WS Write 成功只证明写入接口成功，不证明客户端应用收到或处理。

## 结果与证据

| 标记 | 证据 |
| --- | --- |
| 成功 success | 有文本、可读思考或完整工具调用，正常终止，无完成前写失败 |
| 部分输出后失败 partial_failure | 已写出上述可用输出，随后错误、断开、缺少正常终止或写失败 |
| 空结果 empty | 识别到正常结束，没有上述可用输出 |
| 输出前失败 failed | 没有上述可用输出，识别到错误/写失败/断开/缺少正常终止 |
| 结果未知 unknown | 新输出类型、加密思考、超出解析限制等，无法充分识别输出 |
| 未审计 | 没有记录、历史请求、落库缺失或账单关联不唯一；不补判成功/失败 |

观察 Messages、Responses、Chat Completions 的流式/非流式和协议转换后的实际下游字节；同时观察现有模型观察器接触到的上游证据。专用图片、视频、实时语音和计数端点不在此文本审计范围。主要 /v1 入口、根路径别名、Codex 与 Antigravity Messages 别名覆盖。

完整函数工具调用要求非空名称与调用 ID，参数为完整 JSON 对象。Anthropic 的空 input 对象在 content_block_stop 后有效；Chat 参数缺失不视为完整。仅有签名或密文思考不算可读输出。SSE 心跳和角色、元数据不算输出。

OpenAI WebSocket 按物理连接 UUID + 轮次记录。沿用现有 BeforeRequest/AfterTurn，OnTurnComplete 的结算回调顺序不改；审计在真正的下游 WriteFrame 之后独立结束。终止帧写失败可以显示 failed/partial_failure，而既有结算仍按原时机发生。连接在正常结束后关闭不改变该轮结果。

## 隔离与存储

新增迁移 275 只创建 gateway_response_audits 和索引；旧迁移不变。每个物理 HTTP 请求生成内部 UUID，客户端重复 request ID 不会覆盖审计。Save 以 UUID+turn 幂等，重试至多 3 次。单独有界任务池，最多 2 个 worker、2048 个排队任务、任务 2 秒（若既有配置更小则取较小值）；关停最多给审计 2 秒排空，随后取消数据库等待并丢弃排队审计。溢出直接丢弃并计数，不进入原用量池、不同步降级、不修改结算命令、指纹或凭证。

请求路径暂存 SSE/JSON 与工具片段用于判定，每种证据累计工具参数最多 1 MiB、64 个未完成工具；单个事件/非流式正文最多 1 MiB。持久化仅保存布尔证据、结果枚举、时序和标识，不保存提示词、输出正文、思考正文、工具参数、鉴权头或供应商密钥。新工具类型或超限应显示 unknown。进程崩溃可能丢失排队记录，不能把缺失记录判断为成功。

每小时在 5 秒预算内分批清理超过 30 天的审计，最多 100 批、每批 1000 行。只清理本表，不删除财务数据。统计中的观察起点是现存最早记录；写失败/丢弃计数只覆盖本进程启动以来，未代表完整历史覆盖。

## 管理员查询与财务关联

GET /api/v1/admin/usage/response-audits、/stats、/:id 沿用管理员认证。默认近 24 小时，单次时间范围最多 7 天，列表页 50 行、最大 100 行，支持状态/用户/Key/账号/模型/端点/网关请求 ID。没有 usage_log 的失败请求也保留在独立列表。

AdminUsageLog 可附加可空 response_audit，用户 DTO 与用户使用记录页面不变。按 API Key + 结算身份查询已有 usage_settlement_receipts；金额返回十进制字符串，只读且 nullable，not_found/unlinked 不等于零费用。一个客户端身份对应多次实际请求时，账单结果保持“未审计”，详情列表保留各次记录。空结果/输出前失败的收费凭证统计按凭证 ID 去重，只是核查线索，不是退款结论。

证据详情链接现有系统日志/错误记录，以网关 request ID、用户、Key 和请求前后 30 秒范围查询，可查看既有上游错误、重试、换号记录。监控关闭、权限或留存导致日志缺失时明确显示不可用。审计不复制敏感日志正文入新表。

## 验证记录

本机已验证：
- 前端全量 362 文件、2974 项测试通过；TypeScript、构建与 .github/geili/verify.sh 通过。
- Go default 全量回归、服务/处理器审计 race、PostgreSQL 审计集成 race 通过。实际余额、Key 用量、上游成本、财务凭证写入前后不变；重复关联、无记录、保留期与幂等验证通过。
- 本机 Go unit 全量的旧 TestInflightEstimate_AccountMappingNoDBAndBoundedMemory 在两次运行中全局 HeapAlloc 增量超过 8 MiB，单独复测通过；该测试与计费估算代码本次未修改。候选 CI 的全量 unit 门禁仍必须实际通过，不豁免或放宽阈值。
- 模拟协议覆盖文本、仅思考、完整/不完整工具、零输出 token、空结果、HTTP 200 流内失败、缺少终止、短写/写失败、逐字节 UTF-8、未知类型、超限、WS 多轮。
- 独立审计池满载/已停止不阻塞；数据库瞬时错误限次重试，持续错误计数。

HTTP 实测入口：先 go build -o bin/acceptance-server ./cmd/server，再在两个终端运行 .github/geili/response-output-audit-acceptance.py serve 与 verify。该脚本复用独立本机 PostgreSQL/Redis，以 synthetic-test-key 连本机 Mock，创建独立测试用户/分组/Key，不调用真实付费供应商。私密状态与原始报告仅存 deploy/.secrets/acceptance/，公开记录只保留脱敏汇总。

最终源码 `ae51c485a34f6656a011c773a5796ecbc44c8392` 的本机 HTTP 25 场景已通过：成功 15、空结果 6、部分输出后失败 1、输出前失败 2、结果未知 1。真实等待 50 秒的模拟上游场景证明：45 秒保活后 HTTP 200 仍可携带客户端可解析的 response.failed；审计记录实际 HTTP 200 / failed，而非内部拟返回的 502。覆盖带正文和仅设错误状态两种处理器回归；不声称网关写入成功证明用户应用收到，也不声称全部 SDK 版本已验证。

候选 CI 新增审计 race 门禁。最终候选 [37364064985](https://github.com/QingsiLiu/sub2api/actions/runs/37364064985) 已 completed / success，全量 unit 等门禁实际通过，无豁免。此前 GitHub runner 分配故障作为历史证据保留。镜像 `ghcr.io/qingsiliu/sub2api@sha256:601733f3bd1b2d9b8f72c9537eb09e51a4ad527fa43bd224313b2b37c30e7bc1` 已通过运维仓 canonical 入口固定 Stage `0.2.13-geili.4`，部署备份 `/opt/sub2api-subscription-lab/backups/20261006T015156Z-candidate`。

Stage 25 场景已通过，分类和本机一致；50 秒静默模拟实际 45 秒保活后解析 response.failed、HTTP 200 / failed。管理员后台核对五类结果、历史未审计、只读金额、筛选、详情、上游 502/换号关联；1280px 桌面、390px 手机审计视觉 18 项通过，390px/320px 订单布局 10 项通过。订单通用金额美元符号仍是既有未修复项，不称为币种修复通过。SDK 全覆盖与真实外部供应商长流没有伪称通过；WebSocket 多轮等由当前源码 CI/race 覆盖，本轮 Stage 25 场景为 HTTP。

另有 Stage 综合 284、赠礼 52、跨场景 26、计费可靠性 31、Composite Key 36 项通过（加审计共 454 项）；当前源码独立双实例 6 项通过。334 个迁移与 Stage 校验一致，原迁移未改。全部合成夹具停用、配置恢复、正式活动仍 draft、未冻结/无人领取；生产三容器指纹不变、health 200。本机专用数据库/Redis已停，数据保留。

公开报告和截图在运维仓 `docs/reports/response-output-audit-20261006/`，原始配置快照及凭据留于私密目录。生产证明草案已绑定 revision/digest、迁移与报告 SHA256，保持 production_ready=false。本版后端已变，不能套用 `.3` 实付/外联证据或旧例外授权；用户本次已明确授权 ¥1 实付、文本≤$0.05、图片≤$0.30和图片规格/Stage视频路由/订单币种三项暂缓；专用订单1197已创建，12:21仍未支付、12:47过期，套餐下架、无Key/上游调用；真实回调/首次购买精度、限额文本/图片证据及实际生产切换授权仍待完成。不执行历史退款，不停生产账号调度，不将 output_tokens=0 当作免费条件。
