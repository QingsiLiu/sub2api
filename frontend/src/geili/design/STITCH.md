# Stitch 项目记录

通过 `.github/geili/stitch.sh`（官方 MCP，见 GEILI.md §6.6）驱动。以下 ID 供后续出图 / 编辑 / 生成变体时直接引用。

| 项 | ID |
| --- | --- |
| 项目 | `projects/10497566828730451080`（标题：给力 API · 视觉重塑 (Sub2API re-skin)） |

## 已定调：F · 素白 Atelier Blanc

第三轮把「高级」拆成十条硬性规则写进设计系统的 designMd（去卡片化 / 圆角 ≤4px / 零阴影 /
90% 中性色 / 极端字号对比 / hairline 分栏 / 表格无框 / 图表 1px 细线 / 线性图标无底块 /
禁玻璃拟态与渐变），三套方向只在色彩纪律与材质上分野。**2026-09-04 用户选定 F。**

设计系统 ID：`assets/9863882573956697786`

| 屏 | screenId | 归档 |
| --- | --- | --- |
| 用户仪表盘（浅色，定调屏） | `b4c83a16d6c54007bcecb6325ba30a61` | [`stitch/dashboard.jpg`](stitch/) |
| 设计系统总览 | `038a0a73e3dc453dac5935d7f11cac3e` | [`stitch/tokens.jpg`](stitch/) |
| 登录 | `557b02dd44d84ac49258d23caaf116c9` | [`stitch/login.jpg`](stitch/) |
| 用户仪表盘（深色） | `9131ea3ca3344b209ec3b4d9e7835c35` | [`stitch/dark.jpg`](stitch/)（表格与环形图渲染不全，仅取配色） |
| API 密钥（含新建弹窗） | `bbcfc50f8cad46aead800f4504be23d3` | [`stitch/keys.jpg`](stitch/) |
| 使用记录 | `a9c35ff7bb724b82bfccb030660a48fc` | [`stitch/usage.jpg`](stitch/) |
| 管理端账号管理 | `81ff2dad8b1b4b4b8a682296c999a78e` | [`stitch/admin.jpg`](stitch/) |

出图为英文示意，实现时按上游 i18n **1:1 锁死中英文案**。

**从设计稿到代码的真源**是 `stitch/*.html` 里的 tailwind.config 与 hex 值，已提取进
[`../styles/tokens.css`](../styles/tokens.css)。实现效果对照页见
[`preview.html`](preview.html)（`pnpm dev` 后访问 `/src/geili/design/preview.html`，不需要后端）。

> 注：早期重试因 `stitch.sh` 解析缺陷重复创建过同名设计系统，以上表 ID 为选用项，其余忽略。
> 落选方向：G · 夜丝绒 `assets/11859685962158185669`、H · 青瓷 `assets/12305910844357327277`。

## 第二轮（已否决）

灵感：Quiet Luxury / 时装站少色纪律。用户评价不够高级，已被第三轮取代。

| 方向 | 设计系统 | 仪表盘 screenId | 气质 |
| --- | --- | --- | --- |
| **D · 静奢 Quiet Signal**（推荐） | `assets/3882855587534489052` | `fb12f4434d3e4882ba0d236e5cd34717` | bone 底 + 暖铜 `#9A5B3C` 单强调；Syne + Outfit；浅色默认 |
| **E · 黑匣 Black Atelier** | `assets/10500502744680945671` | `a88e073863a44b0795fb91719aa91f97` | 近黑 + 朱红 `#FF2D55` 极省用；Space Grotesk；深色默认 |

候选截图 / HTML：[`stitch/candidates/dashboard-D.*`](stitch/candidates/)、[`dashboard-E.*`](stitch/candidates/)。

## 第一轮（已否决，仅归档）

| 方向 | 设计系统 | 备注 |
| --- | --- | --- |
| A · 墨瓷 | `assets/4960013968603462379` | 纸感衬线年报 |
| B · 控制台 | `assets/206099516204492204` | 荧光黄绿终端 |
| C · 晴空 | `assets/16198369197617907286` | 靛蓝珊瑚商务 SaaS |

## 常用命令

```bash
S=.github/geili/stitch.sh; P=10497566828730451080
$S call list_design_systems "{\"projectId\":\"$P\"}"
$S screen $P <screenId>
$S call generate_screen_from_text "$(jq -cn --arg p $P --arg ds assets/<id> --rawfile pr prompt.txt \
   '{projectId:$p, designSystem:$ds, deviceType:"DESKTOP", modelId:"GEMINI_3_1_PRO", prompt:$pr}')"
```

注意：`generate_*` / `edit_*` 单次 2–4 分钟；`list_screens` 对本项目可能返回空对象，以生成响应中的 `outputComponents[].design.screens[]` 为准。
