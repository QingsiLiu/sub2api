import type { Plugin } from 'vite'

export interface OverrideTable {
  /** 绝对路径 -> 绝对路径 */
  byOriginal: Map<string, string>
  /** 文件名预筛集合 */
  basenames: Set<string>
}

export function splitQuery(id: string): [string, string]
export function buildOverrideTable(
  frontendRoot: string,
  overrides?: Readonly<Record<string, string>>
): OverrideTable
/**
 * 返回应改写到的 id；以下情况返回 null：未命中覆盖表、替换实现自己 import 原模块
 * （wrapper 模式）、原模块自己发起的 SFC 子请求。
 */
export function redirectResolvedId(
  table: OverrideTable,
  resolvedId: string,
  importer: string | undefined
): string | null
export function geiliOverrides(frontendRoot: string): Plugin
