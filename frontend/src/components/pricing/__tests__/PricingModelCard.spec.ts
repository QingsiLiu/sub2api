import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import type { PricingGroup } from '@/api/pricing'
import PricingModelCard from '../PricingModelCard.vue'
import type { PricingRow } from '../pricingPresentation'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  const messages: Record<string, string> = {
    'pricing.details': '明细',
    'pricing.personal': '专属',
    'pricing.channelOverride': '渠道覆盖价',
    'pricing.savePercent': '省 {value}%',
    'pricing.abovePercent': '高于官方 {value}%',
    'pricing.cardDescription': '当前展示 {group}',
    'pricing.items.input': '输入',
    'pricing.items.output': '输出',
    'pricing.items.cache_read': '缓存读取',
    'pricing.units.request': '每次请求',
    'pricing.units.millionTokens': '每 100 万 tokens',
  }
  return {
    ...actual,
    useI18n: () => ({
      locale: { value: 'zh-CN' },
      t: (key: string, params: Record<string, unknown> = {}) =>
        (messages[key] || key).replace(/\{(\w+)\}/g, (_, name) => String(params[name] ?? '')),
    }),
  }
})

const group: PricingGroup = {
  id: 4,
  name: 'Plus/Pro 混合号池',
  platform: 'openai',
  default_multiplier: '0.1',
  effective_multiplier: '0.1',
  multiplier_source: 'default',
  currency_mode: 'usd_quota',
  peak_rate_enabled: false,
  peak_rate_active: false,
}

const row: PricingRow = {
  model: {
    model_id: 'gpt-5.6',
    display_name: 'GPT-5.6 (Sol)',
    provider: 'openai',
    billing_mode: 'token',
    comparison_status: 'exact',
    group_prices: [],
  },
  price: {
    group_id: 4,
    billing_mode: 'token',
    price_source: 'channel',
    items: [
      {
        key: 'input',
        unit: '1M_tokens',
        official_price: '5',
        official_currency: 'USD',
        official_cny_price: '34',
        system_base_price: '5',
        system_base_currency: 'USD',
        effective_multiplier: '0.1',
        multiplier_source: 'default',
        actual_cny_price: '0.5',
        savings_cny: '33.5',
        savings_percent: '98.53',
        comparison_status: 'exact',
      },
      {
        key: 'output',
        unit: '1M_tokens',
        official_price: '30',
        official_currency: 'USD',
        official_cny_price: '204',
        system_base_price: '30',
        system_base_currency: 'USD',
        effective_multiplier: '0.1',
        multiplier_source: 'default',
        actual_cny_price: '3',
        savings_cny: '201',
        savings_percent: '98.53',
        comparison_status: 'exact',
      },
      {
        key: 'cache_read',
        unit: '1M_tokens',
        official_price: '0.5',
        official_currency: 'USD',
        official_cny_price: '3.4',
        system_base_price: '0.5',
        system_base_currency: 'USD',
        effective_multiplier: '0.1',
        multiplier_source: 'default',
        actual_cny_price: '0.05',
        savings_cny: '3.35',
        savings_percent: '98.53',
        comparison_status: 'exact',
      },
    ],
  },
}

describe('PricingModelCard', () => {
  it('shows API-provided actual prices and group context without hover', () => {
    const wrapper = mount(PricingModelCard, {
      props: { row, group },
    })

    expect(wrapper.text()).toContain('gpt-5.6')
    expect(wrapper.text()).toContain('¥0.5')
    expect(wrapper.text()).toContain('¥3')
    expect(wrapper.text()).toContain('¥0.05')
    expect(wrapper.text()).toContain('Plus/Pro 混合号池')
    expect(wrapper.text()).toContain('省 98.53%')
  })

  it('opens the full pricing drawer from the details action', async () => {
    const wrapper = mount(PricingModelCard, {
      props: { row, group },
    })

    await wrapper.get('[data-pricing-details]').trigger('click')
    expect(wrapper.emitted('open')).toHaveLength(1)
  })
})
