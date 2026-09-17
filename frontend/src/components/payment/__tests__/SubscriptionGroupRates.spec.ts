import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import { createI18n } from 'vue-i18n'
import SubscriptionGroupRates from '../SubscriptionGroupRates.vue'
import type { CheckoutGroupRate } from '@/types/payment'

const i18n = createI18n({
  legacy: false,
  locale: 'zh',
  fallbackWarn: false,
  missingWarn: false,
  messages: {
    zh: {
      payment: {
        groupRates: { title: '分组订阅倍率', hint: '实时倍率', other: '其他' },
      },
      keys: {
        usagePanel: { gpt: 'GPT', grok: 'Grok', claude: 'Claude', national: '国模', gemini: 'Gemini' },
      },
    },
  },
})

const rate = (overrides: Partial<CheckoutGroupRate>): CheckoutGroupRate => ({
  id: 1,
  name: 'GPT 稳定',
  platform: 'openai',
  usage_panel: 'gpt',
  subscription_rate_multiplier: 1,
  ...overrides,
})

describe('SubscriptionGroupRates', () => {
  it('groups live rates by usage panel and hides empty boards', () => {
    const wrapper = mount(SubscriptionGroupRates, {
      props: {
        rates: [
          rate({ id: 27, name: 'GPT 给力 Pro', subscription_rate_multiplier: 1.3 }),
          rate({ id: 46, name: 'CC-满血 Max', usage_panel: 'claude', subscription_rate_multiplier: 8 }),
          rate({ id: 4, name: 'GPT 稳定', subscription_rate_multiplier: 1 }),
        ],
      },
      global: { plugins: [i18n] },
    })

    const text = wrapper.text()
    expect(text).toContain('GPT 稳定')
    expect(text).toContain('GPT 给力 Pro')
    expect(text).toContain('CC-满血 Max')
    expect(text).toContain('×1')
    expect(text).toContain('×1.3')
    expect(text).toContain('×8')
    expect(text).not.toContain('#4')
    expect(text).not.toContain('#27')
  })

  it('renders nothing when checkout has no public groups', () => {
    const wrapper = mount(SubscriptionGroupRates, {
      props: { rates: [] },
      global: { plugins: [i18n] },
    })
    expect(wrapper.text()).toBe('')
  })
})
