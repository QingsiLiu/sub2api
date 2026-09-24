# NewAPI 站点余额监控

渠道监控新增 `newapi_balance` 检查方式。它使用监控自己的 HTTPS 上游地址和 API Key，请求同站点的 `GET /api/user/self`，读取 NewAPI 账户的 `data.quota`，不发送模型探活请求。上游地址通常填写 `https://站点/v1`；程序会自动去掉末尾 `/v1`。

如果对方站点要求 `New-Api-User`，在监控高级设置里添加这个请求头。余额会沿用现有配额快照、历史和低余额告警；NewAPI 的 quota 原始单位会标记为 `quota`，避免把它误显示成美元。
