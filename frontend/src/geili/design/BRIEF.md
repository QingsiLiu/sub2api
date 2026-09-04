# 给力 API 视觉重塑 · 设计简报与实施方案

> 给 Stitch（及后续实现）用的输入。目标：**功能与上游 Sub2API 100% 一致，只换视觉呈现。**
> 设计产出物（Stitch 生成的 DESIGN.md、关键屏 HTML/截图）放在本目录 `stitch/` 下，随仓库版本化。

## 1. 产品与受众

- 产品：给力 API（sub.geiliapi.com），面向开发者的 AI 模型网关 / 中转平台。用户购买额度、创建 API Key、
  在自己的应用里调用 Claude / OpenAI / Gemini 等模型，并在站内查看用量、账单、渠道状态。
- 受众：中文为主的独立开发者、小团队技术负责人、AI 应用创业者；英文界面同样要成立。
- 品牌气质关键词（供 Stitch 定调）：**静奢、编辑感、克制、可信、有记忆点**。
  灵感：Quiet Luxury / 时装站的少色纪律 + 2026 SaaS 的高密度与细边框；不是消费 App，也不是官方 teal SaaS 模板。
- 明确避开（已否决）：纸感衬线年报、荧光黄绿终端、靛蓝珊瑚商务圆角 SaaS。
- 现状问题：官方皮肤 = teal 渐变按钮 + 圆角卡片 + slate 深色，几乎每个同类站长得一样，没有记忆点。
- 双模式：**浅色与深色都必须完整实现**，跟随上游主题切换（不得只做默认模式 + 自动派生）。

## 2. 硬约束（Stitch 出图时必须遵守）

1. **信息架构不变**：左侧栏导航（用户区 / 管理区两套菜单）+ 顶栏（站点名、语言、主题、用户菜单）+ 内容区。
   菜单项、页面、按钮、表单字段一个都不能少、不能改名、不能合并。
2. **技术栈**：Vue 3 + Tailwind CSS 3。设计要能用 Tailwind 表达（色板、圆角、阴影、字号都落到 token）。
3. **深色 / 浅色双模式**，两套都要完整。
4. **数据密集**：管理端大量表格（账号、用户、分组、订单、审计日志）、图表（用量趋势、成本）、
   长表单（系统设置几十个开关）。设计必须在 1280–1920 宽度下高密度可读；移动端只需可用，不追求精致。
5. **中英双语**，中文按钮/标签普遍比英文短，英文长文案不能撑破布局。
6. 图表使用 ECharts，只能定义配色与网格风格，不能改变图表类型。
7. 不引入需要付费授权的字体；Web 字体总量控制在 300KB 以内（可用系统字体栈 + 一款品牌展示字体）。

## 3. 需要 Stitch 出图的屏（按优先级）

| 优先级 | 屏 | 对应上游视图 | 说明 |
| --- | --- | --- | --- |
| P0 | 设计系统 | — | 色板、字体、圆角、阴影、间距、按钮/输入/卡片/表格/徽章/弹窗/Toast 全部状态 |
| P0 | 登录 / 注册 | `views/auth/LoginView.vue` 等，`components/layout/AuthLayout.vue` | 含 OAuth 按钮区、验证码位、协议提示 |
| P0 | 用户 · 仪表盘 | `views/user/DashboardView.vue` | 统计卡（余额、今日用量、Key 数）、用量趋势图、公告 |
| P0 | 应用壳 | `components/layout/AppLayout.vue` `AppSidebar.vue` `AppHeader.vue` | 侧栏折叠态、分组标题、当前项、顶栏 |
| P1 | 用户 · API Keys | `views/user/KeysView.vue` | 列表 + 新建弹窗 + Key 明文复制态 |
| P1 | 用户 · 用量 | `views/user/UsageView.vue` | 筛选栏、图表、明细表、分页 |
| P1 | 用户 · 充值 / 订单 | `views/user/PaymentView.vue` `UserOrdersView.vue` | 套餐卡、支付方式、订单表 |
| P1 | 管理 · 仪表盘 | `views/admin/DashboardView.vue` | 更多统计卡 + 多图 |
| P1 | 管理 · 账号 | `views/admin/AccountsView.vue` | 最复杂的表格页：状态徽章、批量操作、抽屉/弹窗 |
| P2 | 管理 · 系统设置 | `views/admin/SettingsView.vue` | 分组 Tab + 长表单 + 开关 |
| P2 | 管理 · 运维面板 | `views/admin/ops/OpsDashboard.vue` | 监控风格的密集图表 |
| P2 | 模型广场 / 渠道状态 | `views/ModelPlazaView.vue` `views/user/ChannelStatusView.vue` | 公开页，可稍有营销感 |
| P3 | 其余 ~50 个视图 | `views/**` | 复用设计系统即可，不单独出图 |

## 4. 实施方案（怎样做到 1:1）

按"影响面从大到小、改动上游从少到多"四层推进，每层都能独立上预发验证：

| 层 | 做法 | 覆盖面 | 触碰上游文件 |
| --- | --- | --- | --- |
| L1 语义类重定义 | 上游 `style.css` 定义了 `.btn* .input* .card* .stat-* .table* .badge* .modal*` 等语义类，全站使用超过 2500 处。在 `styles/geili.css` 里**同名重定义**这些类（我们的样式层在上游之后加载） | 按钮/输入/卡片/表格/徽章/弹窗的形态、颜色、阴影、圆角 | 0 |
| L1 设计 token | `styles/tokens.css` 换掉 `primary/accent/dark` 色板与字体；上游所有 `bg-primary-600`、`text-gray-*` 之类工具类自动跟随 | 全站配色、字体 | 0 |
| L2 壳与通用组件 | 通过 `overrides.ts` 替换 `AppLayout / AppSidebar / AppHeader / AuthLayout` 与 `components/common/*`（StatCard、DataTable、BaseDialog、Toast、Pagination、Input、Select、Toggle、EmptyState、Skeleton…）。**props / emits / slots 与上游完全一致**，只改模板与样式 | 导航、页面骨架、所有通用控件 | 0 |
| L3 关键视图 | 对 §3 里 P0–P1 的视图做覆盖：复制上游 `<script setup>` 原样保留，只重写 `<template>` 与样式。每次同步上游时 watch-paths 会提示这些文件是否被上游改动 | 关键页面的版式 | 0 |

原则：

- 任何一层都不写宽泛选择器（`[class*=...]`、`.bg-white { }`）——只允许同名语义类与 token。
- 覆盖组件必须先通过上游对应的单测（`src/**/__tests__`）——这是"功能 1:1"的机械保证；再补 Geili 自己的视觉快照测试。
- 图表配色统一走 `styles/tokens.css` 暴露的 `--geili-chart-*`，通过覆盖 `utils/charts` 之类的配色入口注入。

## 5. 验收标准

1. 上游 `frontend` 全量 vitest 通过（覆盖组件套用上游用例）。
2. 逐页对照清单：每个路由在新旧 UI 下的可交互元素数量、表单字段、菜单项一致（脚本抓取 DOM 对比）。
3. 预发环境（`.github/geili/staging.sh`）冒烟通过，管理员手工走完：登录 → 建 Key → 调用 → 看用量 → 充值页 → 设置保存。
4. 关键页首屏渲染性能不低于上游（Lighthouse Performance 差值 ≤ 5）。
