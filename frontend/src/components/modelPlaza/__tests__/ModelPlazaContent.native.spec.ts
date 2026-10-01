import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import zhDashboard from '@/i18n/locales/zh/dashboard'
import ModelPlazaContent from '../ModelPlazaContent.vue'
import type { ModelPlazaResponse } from '@/api/modelPlaza'

// Resolve keys against the real zh copy; the test build of vue-i18n cannot compile messages.
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params: Record<string, unknown> = {}) => {
        const raw = key
          .split('.')
          .reduce<unknown>((node, part) => (node as Record<string, unknown> | undefined)?.[part], zhDashboard)
        if (typeof raw !== 'string') return key
        return raw.replace(/\{(\w+)\}/g, (_, name) => String(params[name] ?? ''))
      }
    })
  }
})

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ cachedPublicSettings: null })
}))

function tok(input: number, output: number) {
  return {
    billing_mode: 'token' as const,
    input_price: input,
    output_price: output,
    cache_write_price: null,
    cache_read_price: null,
    image_input_price: null,
    image_output_price: null,
    per_request_price: null,
    intervals: []
  }
}

const baseGroup = {
  description: '',
  subscription_type: 'standard',
  peak_rate_enabled: false,
  peak_start: '',
  peak_end: '',
  peak_rate_multiplier: 1,
  is_exclusive: false,
  image_rate_independent: false,
  image_rate_multiplier: 1,
  video_rate_independent: false,
  video_rate_multiplier: 1,
  long_context_pricing_enabled: true
}

const response: ModelPlazaResponse = {
  description: '',
  prices_include_rate: true,
  display: { usd_cny_rate: 6.8, quota_usd_per_cny: 1 },
  groups: [
    {
      ...baseGroup,
      id: 4,
      name: 'GPT 稳定',
      platform: 'openai',
      rate_multiplier: 0.15,
      currency: 'usd',
      models: [
        {
          name: 'gpt-5.4',
          platform: 'openai',
          pricing: tok(0.375e-6, 2.25e-6),
          pricing_status: 'configured',
          official_pricing: { input_price: 2.5e-6, output_price: 15e-6, cache_write_price: null, cache_read_price: null }
        }
      ]
    },
    {
      ...baseGroup,
      id: 86,
      name: 'Kimi-GLM',
      platform: 'anthropic',
      rate_multiplier: 1,
      currency: 'cny',
      models: [
        {
          name: 'deepseek-v4',
          platform: 'anthropic',
          pricing: tok(2e-6, 8e-6),
          pricing_status: 'configured',
          official_pricing: {
            input_price: 4e-6,
            output_price: 16e-6,
            cache_write_price: null,
            cache_read_price: null,
            currency: 'cny',
            note: '高峰价'
          }
        }
      ]
    }
  ]
}

function mountContent(res: ModelPlazaResponse) {
  setActivePinia(createPinia())
  return mount(ModelPlazaContent, {
    props: { response: res, loading: false, embedded: true }
  })
}

describe('native model plaza with real zh copy', () => {
  it('shows the quota/exchange note, CNY symbols, override note and savings tags', () => {
    const text = mountContent(response).text()

    expect(text).toContain('额度换算：¥1 = $1 额度')
    expect(text).toContain('¥6.8')

    // USD group: $ paid prices, USD official, saving vs ¥ converted official
    expect(text).toContain('$ / 1M token')
    expect(text).toContain('$0.375')
    expect(text).toContain('$2.50')
    expect(text).toContain('省 98%')

    // CNY-native group: ¥ on both sides, override note, 50% off
    expect(text).toContain('¥ / 1M token')
    expect(text).toContain('¥8.00')
    expect(text).toContain('¥16.00')
    expect(text).toContain('高峰价')
    expect(text).toContain('省 50%')
  })

  it('degrades to the plain table when the backend sends no display block', () => {
    const { display: _display, ...legacy } = response
    const text = mountContent(legacy as ModelPlazaResponse).text()
    expect(text).not.toContain('额度换算')
    expect(text).not.toMatch(/省 \d+%/)
  })
})
