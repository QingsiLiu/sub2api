# .2 至 .7 整体业务验收（2026-09-22）

## 范围与当前结论

生产基线 `fa3ecfb1dc3b9a684afb5df2fc6682688c0195b2` / `0.2.7-geili.2`。
本轮在原 `.6` 上补充全业务矩阵，发现并修复5项问题，候选升级为 `.7`。
本地最终源码门禁通过；原 `.6` Stage全套241项通过。`.7`候选CI（35735007268）通过，固定镜像已完成281项Stage检查；证据与后续`.8`视觉修复见文末。
生产未切换；真实支付/供应商/微信支付宝真机SDK没有执行，`production_ready=false`。

## 修复内容与复现

1. 复合Key批量换非首面板时，前端重新按面板排序，可能改变其他面板路由优先级。前端现在与服务端一致，选中组前置，其余组保持原相对顺序；新增非标准顺序回归（修复前失败）。
2. `MODEL_PRICE_NOT_CONFIGURED`属于本地拒绝，却记录了推导上游端点。现在标记本地模型配置错误，保留普通Ops详细日志，清空虚构上游归因；模型不支持仍走入口聚合。路由回归修复前失败，HTTP修复后验证JSON/SSE均正确。
3. Gemini原生流式由路径`:streamGenerateContent`表明，旧逻辑只看JSON stream字段。现在兼容路径信号，新增同步/流式路由失败回归。
4. 购买页当前合同45×2显示“每份日额度90”，实际是总日额度。改为“日额度90”，组件及真实浏览器验证。
5. 分组与用户专属余额倍率0被后台拒绝500，违反零有效规则且前端允许0。统一有限非负校验，零可保存与实扣，非法值返回400；订阅倍率保持独立。HTTP验证原始成本>0而实际扣费0，余额与订阅账本均不被误扣。

## 最终本地结果

验证时源树SHA（包括业务代码、测试和当时脚本，不包括后续本报告）为 `542f50eb88bdfce143c716d51f6db38504fc59e2ab7c4cdbfb5f94f0ac445ed6`。前后相同。

| 门禁 | 通过项 | 跳过 | 结果 |
|---|---:|---:|---|
| go_default | 11776 | 12 | exit 0 |
| go_unit | 20289 | 16 | exit 0 |
| go_integration | 12540 | 14 | exit 0（环境失败后完整重跑） |
| subscription_payment_postgres | 10 | 0 | exit 0 |
| subscription_race | 477 | 0 | exit 0 |
| go_utc | 477 | 0 | exit 0 |
| 前端全量 | 2534（311文件） | 0 | 通过 |
| 前端类型/构建、改动文件ESLint | — | — | 通过 |
| Geili verify（官方视觉与自更新守卫） | — | — | 通过 |
| 全生命周期/多协议HTTP | 240 | 0 | 通过 |
| 新增非订阅差异HTTP | 41 | 0 | 通过 |
| 专属Redis故障恢复/双实例 | 15 | 0 | 通过 |

Go计数含子测试，不同标签之间重复，不合并成独立用例总数。既有skip详见私密report和前次comprehensive审计；本轮订阅与支付专项无skip。

## 环境失败与证据保留

- 第一次HTTP使用全新DB但复用旧Redis，账号ID缓存指向另一轮本地模拟失败端点，Gemini超时。改为每次专属Redis，不清理或复用其他进程缓存。全量240重跑通过。新增final-business-acceptance.py固定隔离规则。
- 第一次最终Go integration在TestRateLimiterFixesMissingTTL启动临时Redis时Docker API超时，尚未进入业务断言。未改源码，完整integration重跑12540项通过、14项既有skip。
- 新增HTTP套件接续全量后命中20次/分钟登录限流。测试遵守限流等待窗口重试，不修改服务端限流；最终专项通过。
- 扩展测试初次夹具错误地通过用户通用编辑接口设置余额（该旧接口未写余额），产生余额不足403；已改用专用balance接口。该次不计业务通过，不把空模型错误误归为定价路径缺陷。

## 页面检查

使用本地隔离`.6`后端和最终`.7`前端进行交互，未使用真实支付渠道：
- 45×2总额度90；叠加1份变135且原到期不变；续期2周期金额40.04、全份延期60天。
- 报价刷新时禁止付款；跨类型操作禁用。
- 390px响应式稳定后clientWidth=scrollWidth=382；console error为0。
- 混合余额/订阅复合Key批量换组成功2把，DB确认保留其它面板与SID。
- Codex默认gpt-6-astra，CC Switch预选Codex并保留Claude/Gemini；不触发真实外部导入。
- 使用记录默认今天、重置后仍今天。

`.7` Stage浏览器确认版本、管理页可加载。新建标签最初出现一次Auto-refresh user failed，未定位根因，不将其称为已修复；局部业务交互的零错误结果不代表全站无运行时错误。`.8`目标浮层的实际镜像深浅色复验见文末。

## 数据与发布约束

相对生产仅增加253/254/255迁移，既有迁移文件未改；`.7`不增加迁移。生产revision仍为主干祖先。
按正式发布入口仅Stage；保留旧库之外新增订单与账本，不能覆盖旧备份假装回滚。
验收矩阵见 `../final-business-test-matrix.md`，订阅详细边界见 `../subscription-v2-test-matrix.md`。

## 证据

- `deploy/.secrets/subscription-v2-comprehensive/20260922T130315Z-all/report.json`；SHA256 `efbb3ec5d3f44c2c708c8dc1f8eaa5793fad04e29678dd84d30204b88efb76ba`。
- `deploy/.secrets/final-acceptance-20260922/integration-final-retry.json`；SHA256 `9063468aec74056f755e22fa86fe912a2e776534f4600d9da0e8dd361211988c`。
- `deploy/.secrets/final-business-acceptance/20260922130256_40a548/report.json`；SHA256 `e688496a7ef53d020c25ca7931a64e55af2191c50e18317b18750f4528a292cb`。
- `deploy/.secrets/subscription-v2-acceptance/acceptance_final_20260922130256_40a548-efe7f40e/report.json`；SHA256 `f78e9d78de5cf49cc4fe68440d4fe44ed19e2de21dc5e10a4d01b9986b8c7294`。
- `deploy/.secrets/subscription-v2-acceptance/acceptance_v2_resilience_20260922133234-58ebe157/report.json`；SHA256 `c5f9277514138d89dfc1a4fb945aebe0623b7182a6969f3cae789425ce72f8b0`。
- `deploy/.secrets/final-acceptance-20260922/browser-local.json`；SHA256 `b056fa6b7ba7b4da546f26e494460b591783f5a4559aa594669cadfffaf5fe77`。
- `deploy/.secrets/final-acceptance-20260922/geili-verify.log`；SHA256 `d3374d3e5967e0122b3c5282e9cad31110cf3b9f12051f8e800c339e9cf7e645`。

原`.6`Stage报告：`/opt/sub2api-subscription-lab/backups/v2-synthetic-20260922T124534Z-f90b66e2/report.json`；241项通过，配置恢复、夹具停用、生产指纹不变。

## Stage最终结果与视觉收尾（2026-09-23归档）

- `.7` revision `436768186247cb9ea487a3976ab7afe561daac2e`，digest `835362d5a5256f5526abefed976125e9f2b94ade47509b496ff1d8c5dc508267`；281项通过。报告`/opt/sub2api-subscription-lab/backups/v2-synthetic-20260922T142355Z-ca9442d3/report.json`，SHA256 `bf5197ab10fa93260e6b7943cb04deebbff5270befd62e300001050e5efe8f60`。
- Stage异步错误日志的ID顺序可能不同于发起顺序。原断言要求[1,2]导致一次失败；实查两条记录的类型、模型、候选列表与无上游字段均正确。改为类型集合排序比较，仍严格要求恰好两条、同步/流式各一条。此测试脚本修订独立于镜像业务代码，在`.7`和`.8`验收中使用。
- `.7`有一轮SSH断连导致恢复不完整（模拟provider 10残留启用）。已用最早快照`v2-synthetic-20260922T141322Z-4e321d15/snapshot.json`执行--restore。最终启用provider=0、未完成订单=0、生产指纹一致；运维适配器新增同revision有未恢复运行则拒绝开始下一轮的守卫及测试。
- `.8` revision `f6f14f12aba420de3e926f630dadc21ca8d6ab87`，digest `f7e8238ec483132384c10b015d79f0deeca2f0f8bc4ffa708a158af98c89c8d8`。仅给顶部订阅浮层“今日剩余”增加dark:text-gray-400；无新增迁移、无业务变更。
- `.8`候选CI 35746696212全绿，前端完整、Go默认/unit/integration、订阅race、PostgreSQL支付竞态通过；本地6项组件测试、typecheck、build通过。
- `.8`canonical Stage备份`/opt/sub2api-subscription-lab/backups/20260922T155237Z-candidate`。Stage再次281项通过（241基础+40差异；本地41项含额外provider夹具保护，故不是同一计数）。报告`/opt/sub2api-subscription-lab/backups/v2-synthetic-20260922T155442Z-93997a04/report.json`，SHA256 `44a69f666ea1c786197414c6e79c62859de59a0009acf84ab19c2a7327851c5e`。
- `.8`stage-result SHA256 `d8503281bcad41471e6ddf988e6082a0c2e6d835a5e2a0c39b108fca757c697b`，passed/restored均true；额外核对provider=0、未完成订单=0、cleanup_errors为空、生产app/PG/Redis指纹一致。
- 实际Stage顶部浮层浅色rgb(107,114,128)、深色rgb(156,163,175)，字号同为12px，目标文字的x/y/宽/高完全相同；检查后恢复原浅色主题。视检证据位于`deploy/.secrets/final-acceptance-20260922/stage-v8-visual.json`。
- Stage和生产health正常。生产仍为`.2`，未切换；真实支付/供应商/真机SDK没有由本轮覆盖，production_ready仍false。
