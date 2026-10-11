# 上游 v0.2.15 同步记录（2026-10-11）

## 同步范围

- 上游：Wei-Shaw/sub2api，最新正式版 [v0.2.15](https://github.com/Wei-Shaw/sub2api/releases/tag/v0.2.15)，2026-10-09 发布。
- 合并目标：`upstream/main` / `3a6fd1c9d`，包含发布 tag `f2669c8cf` 和随后 VERSION 同步提交。
- 当前上游祖先：`0363b8cdba8cec3e2ba4b2dbd49c4481143fa55d` / v0.2.14；差异 147 个提交（含 merge）、278 个文件。
- Geili 起点：`3a5b4716a`，现网 revision `a86c4778f063d2fa976c746731101a51f07b59a7` 仍是祖先。原目录未提交的加密路由工作单独保留，没有提交到本候选。
- 新对外版本：`0.2.15-geili.1`。2026-10-11已部署Stage并以同一digest发布生产。

## 上游变更

1. Cline / ClinePass 与 Command Code 平台，统一平台清单、provider profile、多协议转发和模型目录缓存。
2. 每请求输出 TPS、监控分位数、紧凑账号筛选和更准确的长上下文展示。
3. WS 新分组价格 / 图片用量，OAuth web_search 历史声明与 Responses Lite，加密 reasoning 签名恢复。
4. Chat / Responses 的 developer、具名 tool_choice、legacy function_call、refusal 转换；Anthropic TTL、signature、beta；Grok 空流与 SSO；OpenCode 模型、403 与 Retry-After。
5. 组合分组别名与 Codex 目录映射、裸 API 路径、监控采样、零倍率费用明细及价格指数。
6. 多处异步竞态、支付离页回调、轮询清理、资料草稿、选择器禁用和 Esc 弹窗栈。

## 冲突与二开适配

共 14 个冲突文件。逐块合并，保留现有 Geili 字段和完成校验，不使用单边树覆盖。

- VERSION 升为 `0.2.15-geili.1`；依赖保留现有 Go1.27.2、x/net0.60 与 x/tools0.51，不降级。
- ent runtime 合并上游 target_platform 双校验和 Geili target_group_id / profile_key 字段；由 ent 生成器复核。
- 分组请求保留 subscription_enabled、subscription_rate_multiplier、usage_panel、目标分组与 profile_key，并使用新平台验证标签。
- 四个缓冲转换路径保留 Geili 完成追踪器；它已拒绝负索引、验证块闭合与终态，不恢复上游旧的宽松聚合器。上游负索引测试适配为“无 panic、拒绝错误流、没有成功响应”。
- 流式心跳转换为 SSE comment；心跳写失败标记客户断开，继续读已发送请求的真实用量且禁止后续下游帧。上游心跳夹具补足 content_block_stop，适配 Geili 在 message_stop 即完成、不等待 EOF 的合同；仅心跳仍失败。
- 上游 thinking_signature_invalid 精确消息判断与既有 invalid_request_error 保守恢复共同保留。
- 协议规则编辑器保留官方 Select，接入首选与额外协议集合；配套测试用组件事件验证协议交换。
- 配额测试使用完整平台清单，保留 Geili Codex 配置断言与“今日”用量查询断言，加入 TPS 列断言。
- Grok 空流检测保存整理前的终态证据，避免终态整理器补出 output:[] 后掩盖不完整响应；完整、明确的合法空答案仍不重放。回归同时覆盖不完整空流切换、合法空答案及正常文本输出。
- 自动合并外额外修复 Geili explicitKeyRouting 的旧平台白名单，HTTP 回归覆盖 Cline / Command Code × Messages / Responses / Chat 三入口。
- 账号全选测试清理 wrapper 和模拟计时器，等待筛选防抖请求完成，消除异步任务跨测试泄漏。

## 迁移与保全

官方新增 `242_drop_platform_check_constraints.sql` 保留原名和内容。迁移账本按 filename 主键识别，与 Geili 同编号不同文件的倍率迁移互不覆盖。现有全部 SQL 文件按合并前 checksum 保全。新迁移删除两条数据库平台白名单约束，应用层 schema / repository / handler 继续校验平台；监控 provider 能力约束保留。

## 验证与候选

合并 revision：`da6b2fd9233fe3acfd9fbec84218aa2bacd160c2`；[候选 CI 38109660131](https://github.com/QingsiLiu/sub2api/actions/runs/38109660131)，候选镜像：`ghcr.io/qingsiliu/sub2api@sha256:f1b7e2b14ea8bbcc9f782d4d031b17ce10bc8c83b5e4829df0e94dcd9d008cbc`。13 项候选门禁全部通过；artifact 中的 revision / image 一致，336 份迁移校验与该提交逐文件匹配，旧 335 份与合并前一致，`production_ready=false`。未部署 Stage / 生产。首次全量前端 3198 通过、1 个 Select 操作不适配失败，并发现一个测试清理错误；修复后 390 文件 / 3199 用例全通过，ESLint 通过。首次后端全量受上游心跳夹具与 Geili 终态校验不兼容阻塞，主动中止后修正夹具，最终结果单列，不把中止运行记作通过。

- 本机模拟 HTTP 订阅 V2 验收 104 项通过，环境为独立 PostgreSQL / Redis，无真实支付或供应商调用；验收服务、容器已清理，私密原始报告保存在主工作区被忽略的 `deploy/.secrets/upstream-v0215-20261011/`。
- 335 个旧 SQL 校验和逐文件保全，仅新增官方 242 迁移；PostgreSQL 集成测试覆盖重复执行及保留监控能力约束。
- 兼容 lint2.14.0 的修改行增量为 0 issues；同一工具全量基线 68、合并后 52 条历史诊断仍保留，未宣称全量 lint 通过。由旧工具升级解决的导出格式解析错误不能解释为历史诊断已清零。

- `.github/geili/verify.sh`、前端构建 / ESLint / 3199 测试、embed 网关别名、Stage 恢复脚本 5 项通过；受影响的订阅 PostgreSQL、协议转换、WS 定价、多分组新平台路由及空流 race 检查通过。
- 最终同一份源码的 `go test ./...`、`CI=true go test -tags=unit ./...`、`CI=true go test -tags=integration ./...` 全量通过，增量 lint 复核仍为 0 issues。
- 原工作区 4 个修改文件与 5 个新增文件恢复为未提交；5 个新增文件逐个 SHA256 与合并前一致。重叠恢复条件保留加密会话保护，原工作区另行 `go build ./...` 与加密路由专项测试通过。私密备份保留原始差异及 stash，候选不包含这些实验改动。
- 远程 [Geili CI 38109660129](https://github.com/QingsiLiu/sub2api/actions/runs/38109660129) 与[安全扫描 38109660145](https://github.com/QingsiLiu/sub2api/actions/runs/38109660145) 通过。普通 [CI 38109660123](https://github.com/QingsiLiu/sub2api/actions/runs/38109660123) 的全量 lint 同样报告既有 52 项（errcheck29 / ineffassign2 / staticcheck14 / unused7）；前端、脚本及 Go unit / integration 全通过，唯一失败项是全量 lint，不能将本次 CI 宣称为全绿。

## Stage与生产发布

2026-10-11 22:04左右（UTC14:04）经 `CONFIRM=BUMP_PRODUCTION bin/release-geili-sub2api.sh prod` 发布 `0.2.15-geili.1` / `da6b2fd9233fe3acfd9fbec84218aa2bacd160c2` / `sha256:f1b7e2b14ea8bbcc9f782d4d031b17ce10bc8c83b5e4829df0e94dcd9d008cbc`，Stage与生产同一digest。上游新增Cline/Command Code、输出TPS、平台清单及协议/价格/异步修复，Geili二开保留。用户授权生产并限定仅模拟及非支付验收；候选CI13门禁通过，当前镜像Stage481项及生产保全6项通过，夹具、邀请、临时实例和原存储行均恢复，独立v11 proof及18个篡改拒绝测试通过。真实支付、付费供应商和真实媒体生成明确排除。应用健康恢复9.813秒，生产只读45、公网页面/同Stage资源24项通过，六核心容器healthy/restart0。生产PG/Redis及StagePG/Redis完整指纹保持；336个迁移执行器校验匹配，新平台两个白名单CHECK删除、两个监控能力CHECK保留，邀请表约束完整。Compose仅换应用镜像，env/config.yaml/Nginx及既有媒体开关保持；实际Compose私密回收并与带变量占位符的入库版本核对，rates候选镜像/revision同步，费率规则未改。

备份 `/root/backups/subscription-prod-image-bump-20261011T140423Z`（发布前实际Compose与数据指纹）及 `/root/backups/geili-upstream-v0215-preflight-20261011T134717Z`（配置、数据库完整结构、迁移ledger及受影响约束）。回退上一枚`.8` / `sha256:5d19236efa2e399652d47441fc49827bf49e1ead41c5dbce4b88187286d7e45f`，按runbook以当前rates候选镜像作基线执行既定回退入口；保留新增业务数据，不回灌旧账或盲目重加平台CHECK。未创建生产测试账号，没有生产历史用量整库dump。

首轮Stage媒体沿用既有R2写入合成PNG的失败及恢复保留；修正mock存储隔离后完整108项通过，未做真实生成或支付。执行版源文件SHA与原始回执匹配，维护版额外异常清理分支五项隔离测试通过，旧v8路径保持。原所有失败留私密目录。正式证据在运维仓 `docs/reports/upstream-v0215-20261011/`；独立contract v11排除项不能伪填passed。
