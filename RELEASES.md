# Geili 版本说明

每次改 `backend/cmd/server/VERSION` 并准备发布时，必须在本文件顶部追加一条。没有这条说明，不算完成发版。

每条至少写：版本号、相对上一版的用户可见变化、revision、镜像 digest（候选构建完成后补上）、当前部署到哪（仅源码 / Stage / 生产）。不要把密钥、用户邮箱或生产数据写进来。

## 0.2.8-geili.2

- 日期：2026-09-24
- revision：待本次候选构建绑定。
- 镜像 digest：`ghcr.io/qingsiliu/sub2api@sha256:bffed9aa18e37d8f27f1f92cd9b75770c49cb4bf2ee98178eeadbbc7cbb87f47`。
- 部署：2026-09-24 已完成 Stage 验收并按用户授权完成生产 app-only 镜像切换；PostgreSQL/Redis 未变。

相对 `0.2.8-geili.1`：

- 新增 NewAPI 账户余额监控，读取账户 quota，显示原始额度与查询错误，耗尽时标记降级；V2 下继续定时查询。
- 支持填写 NewAPI 用户 ID 与加密保存的个人访问令牌，阻止跨检测类型复用凭据及余额请求重定向。
- 包含已合入的原生模型广场功能：分组模型目录与余额价格统一展示及管理。

## 0.2.8-geili.1

- 日期：2026-09-24
- 上游基线：`upstream/main` `a3eb7ef302961cba716dc78b39b93b60c467db0e`（远程 v0.2.8 版本同步提交）。
- revision：`ebe7dba0d44d28c9fe8d8833b226c559eac6ee77`。
- 镜像 digest：`ghcr.io/qingsiliu/sub2api@sha256:2974fe88f0c6eaff44fcbe61cf876cefff428ff49454ccce9f8b296e29d8ddc6`。
- 部署：Stage 281 项综合验收、真实支付/文本/图片/视频验收通过；2026-09-24 已按同一 digest 部署生产，生产 health 200，PG/Redis 指纹未变。

相对 `0.2.7-geili.8`：

- 合入上游 v0.2.8 的 GPT-6 Sol/Luna、Claude Opus 5.5、Grok 4.7、OpenCode Go 用量窗口、推理力度计费倍率、Claude Code 版本同步、简易模式 Key 消费窗口、备份归档、线下提现、日志保留、Codex 推荐积分、TypeSafe 内容审核和 HostService 结构化账号元数据。
- 合入工具 Schema 清洗、Antigravity/Codex/OpenAI/Grok/Vertex/图片/流式连接修复及前后端交互修复，并保留 Geili 订阅 V2、权益份额、复合 Key、billing_source 和在线更新禁用守卫。
- 兑换订阅冲突保留 Geili 多权益人工复核边界，同时加入 legacy 负数兑换的并发加锁和不足一天余量修复；AUAPI 图片请求继续保留 resolution 映射。
- OpenAI 管理员同步目录扩展 GPT-6 Sol/Luna，移除过时的“恢复默认 7 个”按钮；已有自定义目录不会被自动覆盖。

## 0.2.7-geili.8

- 日期：2026-09-22
- revision：`f6f14f12aba420de3e926f630dadc21ca8d6ab87`。
- 镜像：`ghcr.io/qingsiliu/sub2api@sha256:f7e8238ec483132384c10b015d79f0deeca2f0f8bc4ffa708a158af98c89c8d8`。
- 部署：2026-09-23 已部署生产。Stage 281 项验收、真实支付/文本/图片/视频验收通过；生产 health 200、匿名 API 401，PG/Redis 指纹未变。生产备份见 `/root/backups/subscription-prod-image-bump-20260923T055507Z`。

相对 `0.2.7-geili.7`：

- 顶部订阅浮层的“今日剩余”补上 `dark:text-gray-400`，沿用项目现有灰色文字token，提高深色背景上的可读性；浅色文字、字号、布局、额度计算不变。

## 0.2.7-geili.7

- 日期：2026-09-22
- revision：`436768186247cb9ea487a3976ab7afe561daac2e`。
- 镜像：`ghcr.io/qingsiliu/sub2api@sha256:835362d5a5256f5526abefed976125e9f2b94ade47509b496ff1d8c5dc508267`。
- 部署：Stage 已通过 281 项验收并恢复配置，生产未切换。

相对 `0.2.7-geili.6`：

- 修复复合 Key 批量换组时额外重排其它面板路由优先级，前后端换组行为保持一致。
- 模型未配置价格的本地拒绝仍保留详细错误日志，但不再虚构上游端点或账号归因。
- Gemini 原生流式请求在路由失败时准确记录为流式请求。
- 修复购买页当前订阅把总日额度标成“每份日额度”的文案错误，例如45美元×2份应显示总日额度90美元。
- 余额分组倍率和用户专属倍率允许明确的0值；负值及非有限值拒绝为400，保留订阅倍率独立含义。
- 补充 `.2` 至当前版本的整体验收矩阵、非订阅差异HTTP检查和独立Redis测试环境，避免历史缓存污染验收。

## 0.2.7-geili.6

- 日期：2026-09-22
- revision：`fc34c54a00708aae8e5b6db3565055e505c3ccc8`
- 镜像：`ghcr.io/qingsiliu/sub2api@sha256:a6ecdfb88608511a063846bd81e9426b3a26c90b1bc8768d448b6481ad1f541a`
- 部署：已上测试环境。`https://staging-sub.geiliapi.com/health` 返回 200。生产未切换。

相对 `0.2.7-geili.5`：

- 修复复合余额密钥批量更换分组、以及密钥列表里单独换分组时整批失败的问题。
- 以前只提交一个 `group_id`，结算校验会拒绝并提示 composite keys cannot set group_id，选中的密钥一个都不会改。
- 现在这个操作表示替换所选分组所在用量面板上的分组，同面板旧分组去掉，其它面板保留。例如 GPT 面板从官转换成 AZ 渠道时，Claude 和国产面板不动。
- 单分组密钥、以及还没有明确计费来源的旧密钥，仍然直接更新 `group_id`。
- 复合密钥如果同时提交 `group_id` 和 `group_ids`，仍然拒绝，避免两种写法互相覆盖。
