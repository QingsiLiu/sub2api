# Stitch 项目记录

通过 `.github/geili/stitch.sh`（官方 MCP，见 GEILI.md §6.6）驱动。以下 ID 供后续出图 / 编辑 / 生成变体时直接引用。

| 项 | ID |
| --- | --- |
| 项目 | `projects/10497566828730451080`（标题：给力 API · 视觉重塑 (Sub2API re-skin)） |
| 设计系统 A · 墨瓷 Ink & Porcelain | `assets/4960013968603462379` |
| 设计系统 B · 控制台 Console | `assets/206099516204492204` |
| 设计系统 C · 晴空 Clear Sky | `assets/16198369197617907286` |

三套设计系统共用 [BRIEF.md](BRIEF.md) 的硬约束（信息架构不可改、组件全状态、双模式、字体预算），差异只在调性：

- **A 墨瓷**：暖白纸底 `#F6F2EA`、墨线边框、朱砂 `#B7472A` 唯一强调色、衬线标题（Source Serif 4）+ Public Sans、4px 圆角。编辑感、安静。
- **B 控制台**：深色优先 `#0B0D10`、荧光黄绿 `#C6F135` 唯一强调色、Geist + JetBrains Mono（数字/Key/模型名）、6px 圆角。开发者工具质感。
- **C 晴空**：浅色 `#F5F7FC`、靛蓝 `#2F4BFF` + 珊瑚 `#FF6B4A`、Plus Jakarta Sans + Manrope、12–16px 圆角、深靛蓝侧栏。明亮商务 SaaS。

## 候选屏（P0 用户仪表盘，含应用壳）

| 方向 | screenId | 备注 |
| --- | --- | --- |
| A | `4dc0d81bc4f145ea9dcf71065aa96b6b`（首版 `485a49a1…` 只渲染了首行，已经 `edit_screens` 补全） | 表格缺“状态”列，选定后补 |
| B | `b7b49bca512540b7b8a88d141574f856` | 一次成型，标签 1:1 |
| C | `d30dab5e9d33419fa36f6c8e080a6132` | Stitch 截图里侧栏文字过淡是 Tailwind CDN 不认 `text-white/64` 的渲染问题，HTML 意图为 64% 白；表格右侧在 1440 宽下被截 |

候选截图与 HTML 在 [`stitch/candidates/`](stitch/candidates/)。选定方向后：其余两套只保留本记录，不再维护；选定方向按 BRIEF.md §3 逐屏出图，产出物入 `stitch/<screen>.html` + `.jpg`。

## 常用命令

```bash
S=.github/geili/stitch.sh; P=10497566828730451080
$S call list_design_systems "{\"projectId\":\"$P\"}"
$S screen $P <screenId>                                   # 取 HTML / 截图 URL（截图 URL 追加 =w2560 得全尺寸）
$S call generate_screen_from_text "$(jq -cn --arg p $P --arg ds assets/<id> --rawfile pr prompt.txt \
   '{projectId:$p, designSystem:$ds, deviceType:"DESKTOP", modelId:"GEMINI_3_1_PRO", prompt:$pr}')"
$S call edit_screens "$(jq -cn --arg p $P --arg s <screenId> --arg pr '…' \
   '{projectId:$p, selectedScreenIds:[$s], deviceType:"DESKTOP", modelId:"GEMINI_3_1_PRO", prompt:$pr}')"
```

注意：`generate_*` / `edit_*` 单次 2–4 分钟；`list_screens` 目前对本项目返回空对象，请以 `get_screen` 与生成响应中的 `outputComponents[].design.screens[]` 为准。
