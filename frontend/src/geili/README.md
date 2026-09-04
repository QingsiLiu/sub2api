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
| `styles/index.css` | 全站样式入口（替换上游 `src/style.css`） |
| `styles/tokens.css` | 设计 token（CSS 变量），配色/字体的唯一真源 |
| `styles/geili.css` | Geili 自有样式层，类名 `geili-` 前缀 |
| `tailwind.config.js` | 包装上游 Tailwind 配置，把语义色板接到 token 上 |
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

- 配色、字体只改 `styles/tokens.css`；Tailwind 的 `bg-primary-600`、`text-accent-500` 等会自动跟随。
- 自有样式类名一律 `geili-` 前缀。
- **禁止**用宽泛选择器覆盖上游样式（`.bg-white`、`[class*="..."]`）。2026-06 Nginx 注入方案
  已证明这会拖垮图表页性能，且随上游改动随时失效。要改外观就替换组件。

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
