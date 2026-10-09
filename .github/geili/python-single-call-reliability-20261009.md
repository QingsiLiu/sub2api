# Python 单次调用可靠性：原因、修复与验收（2026-10-09）

客户使用 Python 调用本站，未自行重试。最初根据错误正文中的 CLI 字样推断调用方式有误；正文或供应商 User-Agent 不能证明客户使用 CLI。本轮目标是在一次客户端请求内恢复可恢复的上游故障，同时准确返回不可恢复失败，保持原计费合同。

## 证据与原因

客户提供的 91 条错误是反馈样本，不能直接与网关每日错误日志汇总相加，也不是本站失败率。2026-10-08 的 5,684 条 Chat Completions HTTP 200 运维错误标为“已恢复”，不能按客户失败统计。

| 客户反馈 | 已确认的原因或边界 | 本轮处理 |
| --- | --- | --- |
| 58 条静默、断流、截断、空流、暂不可用（含 524） | 供应商故障确有发生；部分网关路径在交付内容前就结束请求，开场信息和心跳也可能阻止换路。转换路径曾把不完整 EOF 包装成完成。 | 每次账号尝试暂存开场事件和响应头；正向识别可恢复错误；无有效内容时在同一请求换路；有内容后明确失败，禁止混流。 |
| 18 条 Bedrock thinking signature 400 | Bedrock 已明确拒绝历史 thinking 签名；元数据只能确认拒绝，不能定位签名在哪层发生字节失配。一次现场链路先超时，再换账号后签名拒绝，与线路兼容性有关，但不是完整根因证明。 | 已有 Claude 5.5 签名修复改为请求级最多额外一次，跨分组不重置；保留管理员明确关闭、当前 thinking 模式和工具原始参数。 |
| 9 条 instant inference quota | 供应商当次即时推理容量不足；不是客户钱包余额不足。其他上游并发/排队错误也必须按实际来源判断。 | 识别 HTTP / SSE / WS / 池模式容量错误，优先换健康账号并保留配置冷却；本站余额、订阅、RPM、并发与权限仍是本地准入终态。 |
| 3 条 context_management | 接收线路拒绝旧字段；是此前兼容性修复前的历史记录。 | 保留既有兼容修复并回归旧参数及显式设置。 |
| 2 条超过 50 图片 | 供应商针对单次请求的图片块硬限制；52 个标记不能证明 52 张不同图片。 | 按实际块计数，重复图片也计数；不删图、不去重，不把该线路的 50 张限制推广为全站上限。 |
| 1 条 128000 输出 token | 触及输出上限，记录 max_tokens 与错误均为 128000；尚未在元数据中定位该完整实例或生成该正文的具体层。 | 保留真实 stop_reason / finish_reason / incomplete 或供应商错误；不自动续写、不提高客户预算。 |

生产只读元数据中的一条 524 对应约 125 秒后 Nginx 499，上游阶段约 11 秒、此前约 114 秒；这说明存在上游调用前的等待，但不能仅凭该记录断言客户上传速度慢。签名字节来源和 128000 正文来源仍未定案，未读取客户提示词或修改历史扣费。

## 实现边界

接口、鉴权与同步/流式方式保持现有合同。新增逻辑收拢在 `*_geili.go` 和必要小挂钩，不新增迁移。

- 有效交付包括文字、thinking、工具参数/调用。已经观察到的内置工具执行单独禁止重放，即使转换后没有客户端输出。心跳、响应头、`message_start`、`response.created`、空 reasoning item 不算答案。失败尝试的 ID 和响应头不会泄漏；心跳可以保活，但不会阻止安全恢复。
- 连接错误、空流、异常 EOF、缺终态、静默超时、暂不可用、明确的容量/并发错误在未交付内容时可恢复。HTTP 200 里的 JSON/SSE 错误同样识别。明确管理员透传及本地错误合同优先，不凭一个错误名称猜来源。
- 已交付有效内容后禁止换路，输出协议对应的失败事件。同步 Responses 的真实 failed 状态保留；Responses completed/incomplete、Chat finish_reason、Messages stop_reason 不伪造。`[DONE]`、usage 和 EOF 单独不构成完成；明确终态的空答案即使缺 usage 仍合法。
- Anthropic 兼容 EOF 只有在 message_start、停止原因、全部块闭合、工具 JSON 完整且无错误时才允许完成。真实终态及时返回，避免供应商继续保持连接造成多等一次空闲预算。
- 同一请求只准入一次；安全重放前失败不制造结算结果。部分输出、客户端断开后的有界 usage drain 沿用原合同，有观察到的用量按真正账号/分组结算一次。取消不增加新尝试，写入检测到断开后不继续给客户端发心跳。

## 预算与配置兼容

| 设置 | 默认 | 行为 |
| --- | --- | --- |
| `gateway.stream_data_interval_timeout` | 180 秒 | 普通模型上游读静默预算；下游心跳不重置上游读时钟。 |
| `gateway.long_thinking_stream_data_interval_timeout` | 600 秒 | Claude 5.5 / GPT-6 Astra 独立静默预算；保留禁用与显式自定义。 |
| `gateway.request_recovery_timeout_seconds` | 600 秒 | 从第一次可恢复故障起计，跨账号、同账号重试、分组、选账号/并发等待共享；已有更短截止时间优先。 |
| `gateway.key_group_request_timeout_seconds` | 600 秒 | 普通模型复合 Key 默认整体预算；已有显式配置（含显式 600）仍是硬上限。 |
| `gateway.key_group_long_thinking_request_timeout_seconds` | 1800 秒 | 仅未显式配置普通整体预算时用于长思考复合 Key，避免一条线路等待 600 秒耗尽所有恢复时间。实际有效别名映射在首次尝试前参与判断，无关模型或禁用账号不扩大预算。 |

配置默认与零值兼容见 `backend/internal/config/request_recovery_geili.go`。管理员换号和同账号重试上限继续生效。无账号时不反复清空排除列表；仅已知冷却会在剩余预算内结束才等，最多三轮。不设置统一短首输出期限，不重置预算以延长请求。

## 可复跑链路与判定

`.github/geili/python-reliability-acceptance.py` 创建独立、有所有权标签的 Nginx / PostgreSQL / Redis，启动完整 Go 应用，生成隔离用户、分组、Key 和 A/B 模拟供应商；清理不碰其他环境。运维 `bin/accept-python-reliability-stage.py` 则绑定候选 revision/digest，默认只读，执行前验证 Stage、生产指纹及结构，使用 Stage 应用网络命名空间的 loopback 模拟上游和私有 Nginx，记录并恢复自建夹具。

固定依赖：requests 2.34.2、httpx 0.28.1、openai 3.26.1、anthropic 1.12.1；这两版 SDK 的底层依赖 httpx2 固定 2.13.1。所有推理客户端重试为零，推理等待显式设为 900 秒以覆盖真实长等待；不声称客户自行设置的更短 timeout 会被服务器延长。分别测试同步、异步、原始客户端、普通 SDK 迭代和最终响应获取。管理端夹具限额等待不计为推理重试，账号创建探测与推理请求分别记录。

每个案例记录客户端入口请求数、推理上游尝试数/账号、终态、用量、耗时和结算。成功恢复要求客户端只有一次入口请求、结果完整、失败尝试未混入、凭证/用量日志各一条、账单金额/余额/Key 用量及实际账号/分组正确。部分输出和取消已有用量时也必须有准确的单笔结算，不允许以零凭证掩盖漏结算。

## 验收结果

源码实现已提交并推送 `geili/main`，验收 revision 为 `d5aa99ddcb9bf376589ebc2746fe6279de9a2519`，版本 `0.2.14-geili.2`。本机嵌入版本和 commit 的完整程序 SHA256 为 `aea09b8d3a2d1ebafe2121d4826fe93404674c629c4e4cf080441f67d3551345`；所有完整链路报告退出时确认源码、二进制和 runner 均未改变，独立资源已停止。

| 验收 | 结果 | 证明范围 |
| --- | --- | --- |
| Python 故障快测 | 187/187 通过 | 12 个基础组合、转换/直转/别名、同步/异步 SDK、普通事件迭代和 final 获取；可恢复故障一次入口调用完成，有输出后准确失败；HTTP 200 错误、图片、输出上限、签名、取消、真实 usage 和精确单次结算。 |
| 真实秒数等待、边缘模拟和上传 | 7/7 通过 | 普通模型 180.080 秒、Astra 600.194 秒静默后换 B 成功，各 2 次上游尝试、1 次结算；181 秒长思考/SSE 保活、125 秒同步代理限制、120 秒完整慢上传、30 秒上传中断。 |
| 生产 Nginx 原配置 → 完整网关 | 15/15 预期断言通过 | 原 API/fallback 配置的心跳、同步 JSON、别名与慢上传；Messages/Chat 同步约 300 秒代理超时如实记失败，未把该 HTTP 504 算成生成成功。 |
| Nginx 候选补丁 → 完整网关 | 15/15 通过 | 三入口同步在 305 秒得到完整结果，流式约 5 秒收到真实网关心跳；模拟供应商提前不发送数据。补丁延长 API read/send 到 1800 秒并禁用缓冲，留出恢复时间。 |
| 真实 TLS HTTP/2 RST 补测 | 18/18 故障 + 12/12 能力断言通过 | Astra 三入口 sync/SSE 及 Opus 的 OpenAI-compatible Responses/Chat 在输出前 RST 后换 B；8 个已输出后 RST 不重放。真实 TLS1.3/ALPN=h2、读写 RST_STREAM 帧各一次；Opus 原生六条为 HTTP/1.1，OpenAI 分组 Messages 两条为受控 404，不冒充 H2 成功。 |
| 两实例计费故障恢复 | 9/9 通过 | 真正锁表、两进程 SIGKILL、SQL 中断/WAL 补写；128 并发 HTTP 调用，最终 131 笔凭证/明细各一次，金额之和均 0.1572。SIGKILL 明细恢复时间见最终凭证摘要，未宣称立即恢复。 |
| 两实例缓存与订阅回归 | 15/15 通过 | 独立 PG/Redis、Redis 中断及空缓存恢复、撤销/恢复/重置跨实例生效，无丢失或重复扣费。既有本机 runner 含隔离数据库中的合成签名回调夹具，没有实付。 |
| Go 默认 / unit / integration 全量及受影响 race | 通过 | 原生/转换、其他模型、Key/OAuth/池模式/WS、错误规则、模型映射与工具回归；另有真实 TLS HTTP/2 RST 的完整 Python 链路补测；本轮 Messages 公共入口新增 31 个边界子场景，错误读取在真实上游 body 边界分类，取消和过长行仍不可重试。 |
| 内嵌前端完整测试 / 网关别名 / lint 增量 | 通过 | `/chat/completions` 不再被 SPA 吞掉；完整静态资源测试更新到现存 logo.svg、原断言保留；lint 新增问题 0，既有全量 lint 诊断仍保留。 |
| 前端全量测试、构建、lint | 通过 | 362 个测试文件、2974 个用例；前端产品代码未改。 |
| Stage / HTTP2 执行脚本离线防护 | 55/55 通过 | 隔离数据库/Redis/网络、候选与生产身份、资源所有权和恢复状态；拒绝用 HTTP 200 错误对象或供应商 403 假装成功/本地拒绝，也拒绝无界冷却。离线 guard 不等同 Stage 业务验收。 |
| 旧候选 CI / 不可变镜像 | 通过 | `0e0b98093…` / CI 37950669046 / 镜像 `5a5dabef…`；属于已发现冷却缺陷的旧候选，不能替代新修复。 |
| 旧候选 Stage | 部分通过、冷却失败 | Python 192、Nginx 两配置各 15、非支付业务 51 通过；双实例配额 A→B 恢复通过，但管理员 60 秒冷却未生效。夹具均恢复，生产未改变。 |
| 原 HTTP 冷却规则修复 | unit 通过，race/新 CI/Stage 待执行 | 旧代码在规则匹配前把 400/502 改成 429，错过显式规则；新代码保留原状态，规则优先、兜底后置。八个类型/池/状态组合验证模型范围 60 秒冷却，禁止全账号兜底覆盖。 |

这 224 项 Python/代理案例全部只发一次推理入口请求，按各场景验证成功、准确失败或真实代理限制，结算 oracle 均通过。30 秒上传案例客户端实际 HTTP 状态为空、传输 EOF，唯一关联 Nginx 日志为 408/30.032 秒，上游尝试和凭证均为零；不能称客户端收到了 HTTP 408。原配置代理超时后是否计费取决于实际观察到的 usage：Messages 无用量则无凭证，Chat 的有界 drain 观察到真实用量则按原合同单笔结算。

追加真实 HTTP/2 后，旧 `cbc567ca6` 候选的 Astra Messages 同步/流式两条路径确实失败：收到原始 RST 后只调用 A，直接 502；流式还生成了一条零用量凭证。修复了转换代码漏用 typed body-read 分类，并观察被转换器吞掉的内置工具执行与未闭合 SSE 帧，防止重复执行。无输出、无用量的失败读取不制造空凭证，真实已观察用量保留；输出前弃用尝试仍不结算。本轮新 revision 完整重跑，旧 CBC 通过报告不能代替新增缺口的验收。

验收发现并修复了内嵌前端吞掉已注册 `/chat/completions` 根别名的问题，Python 此前会得到空 HTTP200、上游尝试为零；现已验证真实内嵌中间件下未鉴权401、鉴权后正确转发。既有内存测试的全局堆统计受同包后台工作影响，本轮让原测试在独立子进程执行，仍保留原20,000次工作量、8MiB阈值和全部断言，没有调高阈值。全量集成测试曾因并行运行多个全量命令争用 entc 临时目录失败，最终依照 CI 顺序独立重跑通过；调试失败报告没有删除或混作最终通过。

逐案例公开 JSON、原报告校验和、Go/前端日志校验和及计费摘要见运维仓 [本轮完整证据](https://github.com/QingsiLiu/geili-sub2api/tree/main/docs/reports/python-single-call-20261009)。四份原始 Python 报告 SHA256 分别为 `a5d8e7290899563f8a732fd65cc68efef9356d9e33ef9f23e04404dc334d08d3`、`1faa77bd2afb48f5dcb6e5062c684591d3da2f14fb2fa6cbeb2410f792481596`、`507d04e72fcd437f4f7a58165c7d3f8b51a1de1e48273dcee193a82888930d4b`、`0550d47fceb7daf396f8fc365b8b116b1c76181910f6ae32823e010f9c5db553`。真实 HTTP2 原报告 SHA256 为 `2ca39810318beddf10dd9b92229e7e94eb82e667f168d10d752dbd3f7f106d96`，Linux arm64 binary 为 `b8cfc510d058b16c9ce58dc1f64413128e4679a3ab05bdce6c2e8de594687b33`；同运行代码、不同平台构建，均不是候选镜像。早期调试报告、旧 binary 和上一枚候选均不是本次通过证据。

2026-10-09 的服务器只读核验：Stage/生产仍为 `.1` / `7187d091…` / `ebc09e91…`，六个核心容器 healthy、restart 0，Stage health 200，Stage PG/Redis/网络隔离 guard 通过。334 个当前源码迁移在 Stage 和生产逐项校验相同；Stage 实际还有一条历史 `275_account_group_scheduling_geili.sql` 记录（共 335 行），生产为 334 行。本轮无迁移变更，未删除或改写历史记录。这只是旧运行版本的基线检查，不是新候选 Stage 验收。

生产发布准备期间通过已登录的 GitHub 页面确认实际原因：fork 页面显示 “Workflows on this fork have been disabled”，正文注明因 Actions usage 停用，并提供恢复按钮。恢复既有工作流后显示 “Actions Enabled”，新候选 CI 正常完成；没有扩大凭据权限、绕过门禁或在服务器编译。仅凭此前 enabled/active API 值不能判断 fork 运行许可，原“必须支持审核”的推断已纠正。

## 未执行与已知限制

- 未调用真实付费供应商、支付或媒体生成。用户已当次明确授权生产发布；旧候选 Stage 冷却验收发现真实缺陷，修复后的新候选完整验收与生产切换待执行。Stage/本机模拟不能证明供应商容量已改善。
- 同步 JSON 经代理长等待仍可能超时，不能向 JSON 插入 SSE 心跳。Cloudflare 官方当前默认读超时为 125 秒、写超时为 30 秒；客户错误包装写 120 秒属于该条正文，不可推导全站统一值。本轮独立记录 125 秒同步失败、SSE 保活与慢上传边界，不将 Nginx 模拟冒充真实 Cloudflare 验收。[Cloudflare 官方 524 说明](https://developers.cloudflare.com/support/troubleshooting/http-status-codes/cloudflare-5xx-errors/error-524/)
- 已产生部分输出的断流必须由客户收到准确失败，无法安全地在原流拼接另一条生成。输入硬限制也无法靠服务端重试消除。
- Stage 程序更新不会自动修改生产 Nginx。本轮对生产原配置和候选无缓冲配置做隔离对照，不执行生产入口配置变更。
- 完整 Python OAuth/WS/CLI 链路、Stage 冷却到期等待尚未执行；对应已有 Go 回归不能替代这些场景的端到端证据。旧候选的通过证据保留原身份；修复后的新候选门禁仍待执行，`production_ready=false`。

## 本机复跑入口

先对选定 revision 建立 clean 检出，构建包含真实版本/commit 的内嵌前端程序；准备独立 Python venv 并安装 `.github/geili/python-reliability-requirements.txt`，Docker 必须是本机 Unix socket。所有报告和数据库凭据写入被忽略的 `deploy/.secrets/`。

- 主矩阵：`python .github/geili/python-reliability-acceptance.py --binary <Darwin-program> --idle`，包含 185 基础/故障及 2 个实际 30 秒静默场景，共 187。省略 `--idle` 只有 185，不能报成 187。
- 真实时间：单独使用同一入口的 `--slow-only`（不带 `--idle`），单独运行 7 个 180/600 秒、边缘模拟和上传场景。
- Nginx 对照：`python .github/geili/python-nginx-full-gateway-acceptance.py --binary <Darwin-program> --profile original --original-config <OPS>/deploy/ovh/sub.geiliapi.com.conf`；再用 `--profile candidate`。每个 profile 15 项，真实 305 秒供应商等待与 35 秒完整上传，不用假上游提前 ping。
- 真实 HTTP2：运维 `bin/accept-python-http2-local.py` 提供 `--source-root`、`--binary <Linux-arm64-program>`、`--expected-revision`、`--expected-runtime-sha`、`--main-binary-sha`；参数必须来自本轮精确源码和两平台构建。只有私有容器通过 `SSL_CERT_FILE` 信任夹具 CA，不修改系统 Keychain。
- 计费/缓存复跑及最终源码/二进制冻结校验结果见运维公开摘要；其底层已有 `.github/geili/billing-runtime-acceptance.py` 和 `.github/geili/acceptance.py` 本机入口，纯隔离模拟。

最后检查每例入口次数、完整终态、usage/归属/金额以及所有 `*_unchanged` 和资源回收字段；保留原报告及 SHA256。CI/Stage 使用新镜像与新 revision 时重新执行，不能给本机旧报告换标签。
