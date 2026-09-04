/**
 * Geili i18n 词条注入
 *
 * 上游 `src/i18n/index.ts` 在切换语言时用 `setLocaleMessage` **整包替换**该语言的词条，
 * 因此 Geili 词条不能只在启动时合并一次，必须在每次语言切换后重新 merge。
 * `installGeiliMessages` 负责：立即合并当前语言 + 监听 locale 变化再合并。
 */
import { watch } from 'vue'
import i18n from '@/i18n'
import en from './locales/en'
import zh from './locales/zh'

type GeiliLocale = 'en' | 'zh'

const GEILI_MESSAGES: Record<GeiliLocale, Record<string, unknown>> = { en, zh }

function isGeiliLocale(value: string): value is GeiliLocale {
  return value === 'en' || value === 'zh'
}

/** 把 Geili 词条合并进指定语言（幂等，多次调用无副作用）。 */
export function mergeGeiliMessages(locale: string): void {
  if (!isGeiliLocale(locale)) return
  i18n.global.mergeLocaleMessage(locale, GEILI_MESSAGES[locale])
}

let installed = false

/** 在应用根组件 setup 内调用一次。返回停止监听的函数（主要供测试使用）。 */
export function installGeiliMessages(): () => void {
  mergeGeiliMessages(i18n.global.locale.value)
  if (installed) return () => {}
  installed = true
  const stop = watch(
    () => i18n.global.locale.value,
    (locale) => mergeGeiliMessages(locale),
    { flush: 'sync' }
  )
  return () => {
    stop()
    installed = false
  }
}
