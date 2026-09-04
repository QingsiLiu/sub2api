// @ts-check
/**
 * Vite 插件：按 `src/geili/overrides.ts` 把上游模块透明替换为 Geili 实现。
 *
 * 用 .mjs 而不是 .ts：vite.config.ts 所属的 tsconfig.node.json 是 composite 工程，
 * 只 include 了 vite.config.ts 本身；引入额外 .ts 会触发 TS6307，需要改上游 tsconfig。
 * JS 文件不进入该工程即可绕开，同时保留 JSDoc 类型与配套 .d.mts 声明。
 */
import { basename, resolve } from 'node:path'
import { GEILI_OVERRIDES } from '../overrides'

/**
 * 去掉 Vite/Vue SFC 的查询串（`?vue&type=style...`、`?raw` 等）。
 * @param {string} id
 * @returns {[string, string]} [文件路径, 查询串]
 */
export function splitQuery(id) {
  const index = id.indexOf('?')
  return index === -1 ? [id, ''] : [id.slice(0, index), id.slice(index)]
}

/**
 * @typedef {{ byOriginal: Map<string, string>, basenames: Set<string> }} OverrideTable
 */

/**
 * 把相对路径表展开成绝对路径表。
 * @param {string} frontendRoot `frontend/` 目录绝对路径
 * @param {Readonly<Record<string, string>>} [overrides]
 * @returns {OverrideTable}
 */
export function buildOverrideTable(frontendRoot, overrides = GEILI_OVERRIDES) {
  const srcRoot = resolve(frontendRoot, 'src')
  const geiliRoot = resolve(srcRoot, 'geili')
  /** @type {Map<string, string>} */
  const byOriginal = new Map()
  /** @type {Set<string>} */
  const basenames = new Set()
  for (const [original, replacement] of Object.entries(overrides)) {
    const from = resolve(srcRoot, original)
    const to = resolve(geiliRoot, replacement)
    if (from === to) {
      throw new Error(`[geili-overrides] "${original}" 的替换目标指向了自己`)
    }
    byOriginal.set(from, to)
    basenames.add(basename(from))
  }
  return { byOriginal, basenames }
}

/**
 * 纯函数形式的重定向决策，便于单测。
 * @param {OverrideTable} table
 * @param {string} resolvedId 已解析的绝对 id（可带查询串）
 * @param {string | undefined} importer 发起 import 的模块 id
 * @returns {string | null} 应当改写到的 id；不需要改写时返回 null
 */
export function redirectResolvedId(table, resolvedId, importer) {
  const [file, query] = splitQuery(resolvedId)
  const replacement = table.byOriginal.get(file)
  if (!replacement) return null
  const importerFile = importer ? splitQuery(importer)[0] : undefined
  // wrapper 模式：替换实现自己 import 原始模块时，放行到上游原文件
  if (importerFile === replacement) return null
  // SFC 子请求（`x.vue?vue&type=script...`）由原文件自己发起，必须留在原文件上
  if (importerFile === file) return null
  return replacement + query
}

/**
 * @param {string} frontendRoot `frontend/` 目录绝对路径
 * @returns {import('vite').Plugin}
 */
export function geiliOverrides(frontendRoot) {
  const table = buildOverrideTable(frontendRoot)
  return {
    name: 'geili-overrides',
    // 跑在 Vite 内建 alias 之前；但决策基于解析后的绝对路径，
    // 所以 `@/x.vue`、`./x.vue`、`../x.vue` 三种写法都能命中。
    enforce: 'pre',
    async resolveId(source, importer, options) {
      if (source.startsWith('\0') || source.includes('node_modules')) return null
      if (!table.basenames.has(basename(splitQuery(source)[0]))) return null
      const resolved = await this.resolve(source, importer, { ...options, skipSelf: true })
      if (!resolved) return null
      return redirectResolvedId(table, resolved.id, importer)
    }
  }
}
