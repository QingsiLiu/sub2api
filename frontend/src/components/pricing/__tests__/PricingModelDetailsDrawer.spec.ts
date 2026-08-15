import { mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { PricingGroup } from '@/api/pricing'
import PricingModelDetailsDrawer from '../PricingModelDetailsDrawer.vue'
import type { PricingRow } from '../pricingPresentation'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  const messages: Record<string, string> = {
    'pricing.detailsTitle': '{model} 价格明细',
    'pricing.close': '关闭',
    'pricing.groupFilter': '请求分组',
    'pricing.billingMode': '计费模式',
    'pricing.systemSource': '系统价格来源',
    'pricing.officialReference': '官方对比型号',
    'pricing.officialSource': '官方价格来源',
    'pricing.officialRatePeriods.off_peak': '当前采用官方空闲时段价格',
    'pricing.effectivePeriod': '官方价格有效期',
    'pricing.current': '当前',
    'pricing.pricingBreakdown': '完整价格明细',
    'pricing.billingItems': '项计费',
    'pricing.noComparable': '暂无同规格官方对比',
    'pricing.fxRequired': '待配置对比汇率',
    'pricing.savePercent': '省 {value}%',
    'pricing.abovePercent': '高于官方 {value}%',
    'pricing.defaultRate': '分组默认倍率',
    'pricing.defaultMultiplier': '默认 ×{value}',
    'pricing.items.input': '输入',
    'pricing.units.request': '每次请求',
    'pricing.units.millionTokens': '每 100 万 tokens',
    'pricing.columns.official': '官方价格',
    'pricing.columns.system': '系统基准价',
    'pricing.columns.multiplier': '有效倍率',
    'pricing.columns.actual': '实际人民币扣费',
    'pricing.columns.savings': '相比官方节省',
    'pricing.billingModes.token': 'Token 计费',
    'pricing.priceSources.channel': '渠道覆盖价',
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
    official_reference_model: 'gpt-5.6-sol',
    official_source: 'https://developers.openai.com/api/docs/pricing',
    official_effective_from: '2026-07-09',
    official_rate_period: 'off_peak',
    group_prices: [],
  },
  price: {
    group_id: 4,
    billing_mode: 'token',
    price_source: 'channel',
    items: [{
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
    }],
  },
}

afterEach(() => {
  document.body.innerHTML = ''
  document.body.style.overflow = ''
})

describe('PricingModelDetailsDrawer', () => {
  it('shows official, system, multiplier, actual, and savings values together', async () => {
    const wrapper = mount(PricingModelDetailsDrawer, {
      attachTo: document.body,
      props: { open: true, row, group },
    })
    await wrapper.vm.$nextTick()

    const text = document.body.textContent || ''
    expect(text).toContain('gpt-5.6-sol')
    expect(text).toContain('当前采用官方空闲时段价格')
    expect(text).toContain('$5')
    expect(text).toContain('¥0.5')
    expect(text).toContain('×0.1')
    expect(text).toContain('分组默认倍率')
    expect(text).toContain('省 98.53%')
    expect(text).toContain('¥33.5')
    expect(document.body.style.overflow).toBe('hidden')

    await wrapper.setProps({ open: false })
    expect(document.body.style.overflow).toBe('')
    wrapper.unmount()
  })

  it('closes on Escape', async () => {
    const wrapper = mount(PricingModelDetailsDrawer, {
      attachTo: document.body,
      props: { open: true, row, group },
    })
    await wrapper.vm.$nextTick()

    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    expect(wrapper.emitted('close')).toHaveLength(1)
    wrapper.unmount()
  })
})
