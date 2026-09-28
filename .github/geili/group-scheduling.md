# S2A 真实组内优先级（默认关闭）

## 生效规则

首批仅 OpenAI 分组4/27/126。`groups.group_scheduling_enabled` 默认false，`account_groups.priority_mode` 默认inherit，旧的组内数字不会自动生效。启用时版本0的组从账号全局优先级初始化：API Key为auto，其他账号inherit。再次启用保留已配置模式。

组内auto/fixed使用绑定priority；inherit及未启用分组使用accounts.priority。0有效。请求实际目标组在选号前得到账号值副本，原始账号与共享缓存的全局优先级不被修改。普通/负载/高级引擎按优先层级选择，已满层级回退下一层；健康粘性会话按原契约保留。所有原有模型、额度、协议、隐私、利润等门禁仍适用。

## 管理接口

管理员认证及审计中间件内：

- `GET /api/v1/admin/group-scheduling?group_ids=4,27,126`：最多64组批量快照，返回api_version=1、observed_at、groups。组含enabled/version/members_version/rows；行含全局、配置及缓存实际生效优先级、来源、pending、可用性及原因。
- `PUT /api/v1/admin/groups/:id/scheduling-priorities`：expected_version、expected_members_version、source(manual/automatic)、可选enabled、rows[{account_id,mode,priority}]。启停与改值必须分开。首启只做初始化；automatic仅可更新既有auto行，不能启停或覆盖fixed/inherit。组版本和成员摘要不匹配返回409。每次修改在同一事务内落库、审计及scheduler_outbox。
- `POST /api/v1/admin/groups/:id/scheduling-preview`：model、transport、capability、require_compact。返回preview_only和同口径snapshot，不请求上游、不抢并发、不创建粘性。复用核心资格检查，读取负载；无API Key/会话上下文，利润条件标为需上下文，不保证下一请求选中某一行。

调度缓存尚未应用版本时返回cache_pending；S2A不得把配置值/本地计划当成实际生效值。普通group_ids编辑保留仍存在绑定的auto/fixed值；新绑定inherit。

## S2A 接入

S2A继续健康/成本排序。托管组三组分别计算，fixed/inherit不修改；全局写回仅保留非托管组账号。共享账号可以继续为非试点组改全局值，试点auto/fixed值独立。接口超时、快照不完整或缓存pending时冻结该组；冻结记忆持久化，重启或404不丢失。旧核心从未启用过本功能的404兼容旧全局调度。

只有核心真实接口启用组时才切写回作用域。旧的面板本地固定只显示为建议，不自动导入；用户确认保存fixed模式才生效。UI显示实际值、来源和待生效标识；默认按实际值排序。账号整体近3分钟成功率独立主列，不用健康系数或IQ正确率冒充。

## 验收及发布

验证组间隔离、0/继承/固定、初始值、原子冲突、成员更新、缓存字段及待同步、两个调度引擎、满载回退、粘性保留、失败冻结与旧核心兼容。真实PostgreSQL+Redis集成测试不使用生产数据；前端类型检查及小屏视觉检查。

先核心候选与Stage，默认关闭；S2A先显示真实状态，试点启用为单独动作。生产核心必须当次授权、Stage与生产同digest；不在本次源码实现里直接启用生产三个组。回退关闭组内能力恢复全局优先级，保留绑定和审计记录。
