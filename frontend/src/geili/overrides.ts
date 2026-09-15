/**
 * Geili 模块覆盖表（Module Override Table）
 *
 * 这是 Geili 二开前端的核心机制：把上游任意一个源码模块，在构建/开发期透明替换为
 * `src/geili/` 下的自有实现，**不修改上游文件本身**。
 *
 * - key   ：被替换的上游模块，路径相对 `frontend/src/`
 * - value ：替换实现，路径相对 `frontend/src/geili/`
 *
 * 规则：
 * 1. 替换实现必须与上游模块保持相同的对外接口（组件 props / emits / slots，
 *    模块的 export 名称与类型），因为 `vue-tsc` 仍按上游文件做类型检查。
 * 2. 替换实现里 `import` 自己覆盖的那个上游路径，会拿到**原始**上游模块
 *    （wrapper 模式：先包一层再逐步替换内部实现）。
 * 3. 被覆盖的上游文件即使被上游修改，git 合并也不会报冲突。因此每加一个条目，
 *    要把对应上游文件同时登记到 `.github/geili/watch-paths.txt`；同步 PR / issue 会列出
 *    这些文件在本次上游版本中的改动，由人来决定是否吸收进替换实现。
 * 4. 只加条目，不改上游文件。要改上游文件请先想想能不能改成覆盖。
 *
 * 该文件同时被 `vite.config.ts`（Node 侧）与单元测试引用，保持无副作用、无运行时依赖。
 */
export const GEILI_OVERRIDES: Readonly<Record<string, string>> = {
  // 全站壳：注册 Geili i18n 词条、标记 <html data-geili-ui>，其余委托给上游 App.vue
  'App.vue': 'App.vue',
  // 全站样式入口：先引入上游 style.css，再叠加设计 token 与 Geili 层
  'style.css': 'styles/index.css'
}

/** `frontend/src/` 下需要保持“挂钩”的上游文件，及每个文件里必须存在的标记。 */
export const GEILI_HOOKS: ReadonlyArray<{ file: string; marker: string }> = [
  { file: 'vite.config.ts', marker: 'geiliOverrides(' },
  { file: 'postcss.config.js', marker: 'src/geili/tailwind.config.js' }
]
