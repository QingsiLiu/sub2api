# Geili 版本说明

每次改 `backend/cmd/server/VERSION` 并准备发布时，必须在本文件顶部追加一条。没有这条说明，不算完成发版。

每条至少写：版本号、相对上一版的用户可见变化、revision、镜像 digest（候选构建完成后补上）、当前部署到哪（仅源码 / Stage / 生产）。不要把密钥、用户邮箱或生产数据写进来。

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
