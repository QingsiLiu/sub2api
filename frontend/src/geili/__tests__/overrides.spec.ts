import { existsSync, readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import { GEILI_HOOKS, GEILI_OVERRIDES } from '../overrides'
import {
  buildOverrideTable,
  redirectResolvedId,
  splitQuery
} from '../vite/geili-overrides.mjs'

// vitest.config.ts 位于 frontend/，测试运行时 cwd 也是 frontend/
const FRONTEND_ROOT = process.cwd()
const SRC = resolve(FRONTEND_ROOT, 'src')

describe('geili overrides table', () => {
  it('每个条目的上游原文件与 Geili 替换文件都真实存在', () => {
    for (const [original, replacement] of Object.entries(GEILI_OVERRIDES)) {
      expect(existsSync(resolve(SRC, original)), `上游文件缺失: src/${original}`).toBe(true)
      expect(
        existsSync(resolve(SRC, 'geili', replacement)),
        `替换文件缺失: src/geili/${replacement}`
      ).toBe(true)
    }
  })

  it('替换实现不能指向自己', () => {
    expect(() => buildOverrideTable(FRONTEND_ROOT, { 'geili/App.vue': 'App.vue' })).toThrow()
  })

  it('上游挂钩文件仍然带着 Geili 标记（防止合并上游时被冲掉）', () => {
    for (const { file, marker } of GEILI_HOOKS) {
      const content = readFileSync(resolve(FRONTEND_ROOT, file), 'utf8')
      expect(content.includes(marker), `${file} 缺少标记 "${marker}"`).toBe(true)
    }
  })
})

describe('redirectResolvedId', () => {
  const table = buildOverrideTable(FRONTEND_ROOT, { 'App.vue': 'App.vue', 'style.css': 'styles/index.css' })
  const original = resolve(SRC, 'App.vue')
  const replacement = resolve(SRC, 'geili', 'App.vue')

  it('把上游模块改写到 Geili 实现，并保留查询串', () => {
    expect(redirectResolvedId(table, original, resolve(SRC, 'main.ts'))).toBe(replacement)
    expect(redirectResolvedId(table, `${original}?raw`, resolve(SRC, 'main.ts'))).toBe(
      `${replacement}?raw`
    )
    expect(redirectResolvedId(table, resolve(SRC, 'style.css'), resolve(SRC, 'main.ts'))).toBe(
      resolve(SRC, 'geili', 'styles', 'index.css')
    )
  })

  it('未命中覆盖表的模块不动', () => {
    expect(redirectResolvedId(table, resolve(SRC, 'router/index.ts'), resolve(SRC, 'main.ts'))).toBeNull()
  })

  it('wrapper 模式：替换实现 import 原模块时拿到上游原文件', () => {
    expect(redirectResolvedId(table, original, replacement)).toBeNull()
    expect(redirectResolvedId(table, original, `${replacement}?vue&type=script&lang.ts`)).toBeNull()
  })

  it('原模块自己的 SFC 子请求留在原文件上', () => {
    expect(redirectResolvedId(table, `${original}?vue&type=style&index=0&lang.css`, original)).toBeNull()
  })

  it('splitQuery 正确拆分查询串', () => {
    expect(splitQuery('/a/b.vue?vue&type=script')).toEqual(['/a/b.vue', '?vue&type=script'])
    expect(splitQuery('/a/b.vue')).toEqual(['/a/b.vue', ''])
  })
})
