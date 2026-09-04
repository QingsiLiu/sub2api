# Stitch 项目记录

通过 `.github/geili/stitch.sh`（官方 MCP，见 GEILI.md §6.6）驱动。以下 ID 供后续出图 / 编辑 / 生成变体时直接引用。

| 项 | ID |
| --- | --- |
| 项目 | `projects/10497566828730451080`（标题：给力 API · 视觉重塑 (Sub2API re-skin)） |

## 第二轮定调（当前）

灵感：Quiet Luxury / 时装站少色纪律 + 2026 SaaS 高密度细边框。深浅色都要完整实现。

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
