import { describe, expect, it } from 'vitest'
import i18n from '@/i18n'
import { installGeiliMessages, mergeGeiliMessages } from '../i18n'

// vitest.config.ts 未开启 __INTLIFY_JIT_COMPILATION__，运行时构建无法编译字符串词条，
// 所以这里直接断言词条树而不是 t() 的返回值。
function messages(locale: 'en' | 'zh'): Record<string, any> {
  return i18n.global.getLocaleMessage(locale) as Record<string, any>
}

describe('geili i18n', () => {
  it('把 geili 命名空间合并进当前语言，且不覆盖已有上游词条', () => {
    i18n.global.setLocaleMessage('zh', { upstream: { hello: '你好' } })
    i18n.global.locale.value = 'zh'
    mergeGeiliMessages('zh')

    expect(messages('zh').geili.brand.name).toBe('给力 API')
    expect(messages('zh').upstream.hello).toBe('你好')
  })

  it('上游整包替换词条后，语言切换会重新合并 Geili 词条', () => {
    const stop = installGeiliMessages()
    try {
      // 模拟上游 loadLocaleMessages 的整包替换，再切换语言
      i18n.global.setLocaleMessage('en', { upstream: { hello: 'hello' } })
      expect(messages('en').geili).toBeUndefined()

      i18n.global.locale.value = 'en'

      expect(messages('en').geili.brand.name).toBe('Geili API')
      expect(messages('en').upstream.hello).toBe('hello')
    } finally {
      stop()
    }
  })

  it('非受支持语言不做任何事', () => {
    expect(() => mergeGeiliMessages('fr')).not.toThrow()
  })
})
