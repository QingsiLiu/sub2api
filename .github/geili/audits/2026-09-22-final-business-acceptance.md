# .2 至 .7 整体业务验收（2026-09-22）

## 范围与当前结论

生产基线 `fa3ecfb1dc3b9a684afb5df2fc6682688c0195b2` / `0.2.7-geili.2`。
本轮在原 `.6` 上补充全业务矩阵，发现并修复5项问题，候选升级为 `.7`。
本地最终源码门禁通过；原 `.6` Stage全套241项通过。`.7`候选CI、镜像与Stage复验结果将在生成后补齐。
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

最终`.7`嵌入式前端与镜像页面复验将随Stage结果补齐。

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
