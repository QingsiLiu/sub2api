# `frontend/src/geili/` — Geili 前端二开层

所有 Geili 自有前端代码都在这个目录里；上游 `frontend/src/**` 其余文件视为只读镜像，
合并上游 tag 时整目录随之更新，不需要人工介入。

## 目录

| 路径 | 作用 |
| --- | --- |
| `overrides.ts` | **唯一的覆盖清单**：上游文件 → Geili 替换文件；以及上游挂钩文件清单 |
| `vite/geili-overrides.mjs` | Vite 插件，按 `overrides.ts` 在解析阶段透明替换模块 |
| `App.vue` | 全站壳（替换上游 `src/App.vue`），骨架阶段 wrapper 上游 |
| `i18n/` | `geili.*` 命名空间词条与注入逻辑 |
| `styles/index.css` | 全站样式入口（替换上游 `src/style.css`），也在这里引品牌字体 |
| `styles/tokens.css` | 设计 token（CSS 变量），配色/字体/圆角/节奏的唯一真源 |
| `styles/geili.css` | 上游语义类的同名重定义 + `geili-` 前缀自有工具类 |
| `tailwind.config.js` | 包装上游 Tailwind 配置，把色板、圆角、阴影接到 token 上 |
| `design/` | 设计方向真源：`BRIEF.md` 规则、`STITCH.md` 出图台账、`stitch/` 设计稿、`preview.html` 实现对照页 |
| `__tests__/` | 覆盖表完整性、挂钩标记、重定向逻辑、i18n 注入的单测 |

## 上游挂钩点（改动上游文件的全部位置）

只有两处，均带 `geili hook` 注释，单测会校验标记是否仍在：

1. `frontend/vite.config.ts` — 注册 `geiliOverrides(__dirname)` 为第一个插件；
2. `frontend/postcss.config.js` — Tailwind 配置入口指向 `src/geili/tailwind.config.js`。

合并上游若冲突，通常只需要把这两行重新加回去。

## 覆盖规则

1. 在 `overrides.ts` 增加一行 `'components/layout/AppLayout.vue': 'layout/AppLayout.vue'`，
   所有对上游 `AppLayout.vue` 的 import（`@/`、`./`、`../` 任何写法）都会拿到 Geili 版本，
   上游其他文件一个字符都不用改。
2. **Wrapper 模式**：在替换文件里 `import Upstream from '@/components/layout/AppLayout.vue'`
   拿到的是上游原文件，可以先包一层、逐步替换内部。
3. 每加一个覆盖条目，把被覆盖的上游文件同时登记到 `.github/geili/watch-paths.txt`：
   上游改了它 git 不会报冲突，靠同步 PR 里的"需人工过目"清单提醒你。
4. 覆盖的粒度尽量落在组件级；不要为了改一个颜色去覆盖整个页面——颜色改 `styles/tokens.css`。
5. 新增的 Geili 独立组件放在本目录内（例如 `components/`、`views/`），不和上游文件混放。

## 样式约定

当前视觉方向是 **F · 素白 Atelier Blanc**（近乎无彩 / 强调色即近黑 / 圆角 4px / 零阴影 /
hairline 分割 / 等宽大写标签），规则见 `design/BRIEF.md`，设计稿见 `design/stitch/`。

- 配色、字体、圆角只改 `styles/tokens.css`；`tailwind.config.js` 把上游用到的色板
  （`primary`/`accent`/`dark`，以及 Tailwind 默认的 `gray`/`slate`/`emerald`/`red`…）
  全部重定向到这些变量，上游 2500+ 处工具类自动跟随，不需要改上游一行代码。
- 光靠 token 换不掉的「形态」（实心徽章、四边框输入、带底色的表头）在 `styles/geili.css` 里
  **同名重定义**上游语义类。三个已经踩过的坑：
  1. 上游 `bg-gradient-*` 的按钮要显式加 `bg-none`，否则渐变图层盖住新的实色背景；
  2. `.glass` / `.text-gradient` 在上游属于 `@layer utilities`，必须在同一 layer 才盖得住；
  3. **深色模式不要反转色阶**。上游已经用 `dark:text-gray-100`、`dark:bg-dark-800` 手工
     处理好明暗关系，再反转一次就是黑底黑字。所有色阶保持「数字越大越深」，
     自己的覆盖照上游的写法逐条加 `dark:` 变体。
- 自有工具类一律 `geili-` 前缀，且刻意写在 `@layer` 之外——`@layer` 里的类会被
  Tailwind 按 content 扫描结果 tree-shake 掉。
- **禁止**用宽泛选择器覆盖上游样式（`.bg-white`、`[class*="..."]`）。2026-06 Nginx 注入方案
  已证明这会拖垮图表页性能，且随上游改动随时失效。要改外观就替换组件。

改完样式用 `design/preview.html` 对照设计稿验证，深浅两套都要看：

```bash
pnpm dev   # 然后访问 http://localhost:3000/src/geili/design/preview.html
```

## i18n

上游切换语言时会整包替换词条；`i18n/installGeiliMessages()` 在 `App.vue` 里调用一次，
负责首次合并并在每次语言切换后重新合并 `geili.*` 词条。新增词条改 `i18n/locales/*.ts`。

## 本地验证

```bash
cd frontend
pnpm typecheck
pnpm test:run src/geili
pnpm build
```
