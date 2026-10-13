# OpenAI 加密会话来源保护（0.2.15-geili.8）

本版本针对已知密文跨账号、跨官转/AZ线路导致的状态不兼容，不能修复供应商内部换号、供应商产生坏密文或已丢失的工具/压缩历史。部署默认 off，生产尚未启用。

## 控制与上线顺序

管理员 settings GET/PUT 中增加 `openai_encrypted_route`。省略字段保留原设置，非法 mode/重复或非正用户 ID 拒绝，null 拒绝。首次默认：

```json
{"openai_encrypted_route":{"mode":"off","protected_user_ids":[3885],"reasoning_recovery_enabled":false}}
```

1. 当前候选 CI 全部通过后，正式 stage 脚本固定不可变 digest，确认版本和新增迁移280/281；全程合成上游，禁真实付费模型探测。
2. 获得本候选当次生产授权后，用与 Stage 同一 digest 发布。先设 observe，覆盖所有用户仅记录来源，不约束选路，观察24小时。
3. 之后人工切 protect，名单仅3885，全部 Key 生效；开启 reasoning_recovery_enabled 才允许一次有限恢复。观察48小时且至少200次携带已追踪密文请求。
4. 放大范围须零误路由、零上下文误删、零工具重放和重复计费，并且已追踪密文路由错误较观察期下降至少50%。样本不足不能宣称成功。
5. 任一回归把 mode 设 off，最多5秒策略缓存刷新；已开始的请求/WS当前轮保持冻结策略。下一请求或下一WS轮取新设置。metadata不删除，回退镜像保留迁移280/281和业务数据，不整库回灌。

策略缓存刷新在每个进程内最多一个后台任务，单次加载预算1秒；冷启动调用者最多等待50ms，缓存仍未就绪时允许按默认off处理当前请求。后续请求使用加载完成的策略，不能把冷启动首次请求当成已进入protect的证据。Stage启动第二实例后，先用独立seed Key向两个进程各发送一次无密文的合成普通请求，等待1.1秒后台加载窗口，确认两次均200后才开始保护功能矩阵；源密文仍仅生成两次，失败检查不重试或改判通过。

## 来源与路由

来源主键是用户ID＋cipher SHA256，跨同用户Key查找，不依赖session猜测。只记录完整且已交付的 output_item.done、completed输出、完整成功JSON/compact；added、输入回显、错误正文不是权威来源。已经交付完整项后发生断流仍可保留来源。

PostgreSQL保存group/account/model/backend fingerprint/生成时间/冲突；Redis固定24小时，SQL按最后访问保持180天有效期，每日冷缓存回源刷新访问；过期记录不会参与选路，物理删除为写触发的增量回收，并非到期瞬间删除。只存摘要与元数据，不存密文、提示或凭据；账号端点/身份/API Key变化使指纹改变，OAuth access token常规刷新不改变指纹。不同account/model/backend来源标记冲突，不能最后写入覆盖。

Redis元数据只提示是否需要刷新访问时间，不作为来源或冲突的权威。每次携密文查找均在原50ms总预算内核对当前PostgreSQL记录：缓存命中用SELECT核对，未命中用UPDATE刷新最后访问时间并返回来源，混合请求在一次SQL往返中完成。这样数据库已提交的冲突、删除或过期不会被旧Redis值遮蔽；命中不延长Redis的固定24小时TTL或SQL访问时间，未命中才重新缓存，保留按日刷新行为。数据库不可用时不能把缓存提示升级为权威来源；protect 请求若已有缓存来源提示，返回503 encrypted_session_verification_unavailable，不能绕过核验换线路。未命中任何提示的旧历史密文仍沿用未知来源故障降级规则。

来源元数据使用独立克隆的Redis视图，启用context超时且关闭重试，不更改应用原共享Redis客户端的行为。MGET提示读取与来源缓存发布各受最多10ms子预算约束，并同时受父操作剩余预算限制；读取保留50ms总窗口给PG权威查询，发布计入protect请求累计500ms来源写入额度。Redis黑洞或断联不能按共享客户端默认超时拖住来源查找，也不能阻止已提交PG记录成为下一次读取的权威。

元数据查找含账号分组补查总预算50ms；无缓存来源提示的故障与旧历史未知来源继续原选路，但保护名单内禁止旧自动密文删除路径。全部密文已知才允许自动推理恢复。已知来源优先原分组；原分组不被当前Key授权时，只能选同一原账号当前所属的其他授权分组，计价/额度/RPM使用实际分组，不能突破Key模型白名单/余额/订阅/权限限制。

protect请求来源写入使用累计500ms预算，多项输出共用同一额度；超过预算或存储故障记录诊断，不能无限阻塞已交付的输出。observe继续异步记录。写入额度与查找50ms、源账号等待15秒分别约束不同操作，不限制正常模型生成耗时。

已知源账号仅对明确短暂cooldown或已有队列等待，总预算15秒，同时遵守原请求deadline；生成不被15秒截断。永久停用、权限失败、身份或模型改变立即失败。不会因绑定来源而把工具密文随机切到别的账号。

## 恢复与协议

安全恢复必须纯reasoning密文，无previous_response_id、compaction、加密工具/agent/未知不透明项、内置工具；没有客户端输出、上游非零计量或已观察的服务端操作。只删reasoning密文和耦合id，保留summary、用户消息、工具定义/调用/结果、大整数、模型和effort；原始请求不修改。每轮一次，同账号优先；HTTP源账号不可用可在一次安全清理后按Key授权尝试其他分组。

HTTP、SSE、compact、WS原生/HTTP bridge/passthrough均覆盖。protect 下 compact 保持原模型，不执行自动模型切换。WS首帧在选号前解析来源，后续轮校验当前账号，连接内不切账号。WS自动恢复仅明确response.failed且尚未交付帧，passthrough只首轮允许同账号重新拨号；后续轮或裸error预告直接保留失败，避免丢后续终态usage。

明确解密失败对外400 invalid_encrypted_content；来源冲突409 encrypted_session_route_conflict；已知原线路不可用503 encrypted_session_route_unavailable。HTTP已提交200时以合法response.failed结束，不能伪造completed。失败尝试有usage则不自动重放，沿用部分结果计费提交一次。

## 验收与监测

新增测试覆盖同用户换Key、跨用户隔离、来源冲突、共享账号授权分组、未知来源不删除、默认off、精确400/502/SSE包装、heartbeat后失败、工具/压缩历史拒绝恢复、summary与大整数保留、WS一次重拨与使用量结算、SQL恢复/保留期/并发冲突、管理员输入验证与缓存竞争。真实PG/Redis使用隔离容器；模型请求仅假上游。

日志 `encrypted_route.lookup` 提供user_id/mode/model/cipher_count/known_count，辅以lookup_failed、record_failed、reasoning_route_recovery及队列丢弃计数。结合原API错误/usage记录统计已追踪请求占比与错误率；不把所有502/503计为密文错误。SQL来源记录不等于账单，计费仍查原用量表。

Stage须验证同镜像双实例、缓存故障、完整done后failed、HTTP/WS参数保存与计费；普通请求p95增量≤10ms、吞吐下降≤2%。本地热缓存微基准不是Stage性能验收。生产自然流量改善不能在上线前宣称，不能通过真实付费请求人为凑样本。

Stage保护矩阵另验证真实核验故障：只有来源表恰好包含本run两条来源时才允许对该表建立短暂排他锁，禁止存在其他Stage来源行。锁持有进程使用1秒lock/statement timeout、3秒idle transaction timeout，2秒就绪和释放期限；finally提交或只终止本run进程。先读取应用实际写入的精确owned Redis key（一次EXISTS，不写缓存、不补造、不重试），锁内第二实例携opaque密文请求必须503 encrypted_session_verification_unavailable、零上游调用、无新增用量；释放后独立请求必须恢复原账号并只增加一次实际原分组计费，再继续原撤权409检查。该测试只在新候选镜像执行，恢复旧fixture的外置runner不作功能证明。
