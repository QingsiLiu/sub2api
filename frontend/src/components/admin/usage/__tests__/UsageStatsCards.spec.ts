import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'

import UsageStatsCards from '../UsageStatsCards.vue'

const messages: Record<string, string> = {
  'usage.totalRequests': 'Total Requests',
  'usage.inSelectedRange': 'in selected range',
  'usage.totalTokens': 'Total Tokens',
  'usage.in': 'In',
  'usage.out': 'Out',
  'usage.cacheTotal': 'Cache',
  'usage.cacheBreakdown': 'Cache Token Breakdown',
  'usage.cacheCreationTokensLabel': 'Cache Creation',
  'usage.cacheReadTokensLabel': 'Cache Read',
  'usage.totalCost': 'Total Cost',
  'usage.accountCost': 'Cost',
  'usage.standardCost': 'Standard',
  'usage.avgDuration': 'Avg Duration',
}

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => messages[key] ?? key,
    }),
  }
})

const stats = {
  total_requests: 1,
  total_input_tokens: 100,
  total_output_tokens: 50,
  total_cache_tokens: 34,
  total_cache_creation_tokens: 12,
  total_cache_read_tokens: 22,
  total_tokens: 184,
  total_cost: 0.001,
  total_actual_cost: 0.001,
  total_account_cost: 0.001,
  average_duration_ms: 250,
}

describe('UsageStatsCards', () => {
  it('shows cache token breakdown values', () => {
    const wrapper = mount(UsageStatsCards, {
      props: {
        stats,
      },
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    const text = wrapper.text()
    expect(text).toContain('Cache: 34')
    expect(text).toContain('Cache Token Breakdown')
    expect(text).toContain('Cache Creation')
    expect(text).toContain('12')
    expect(text).toContain('Cache Read')
    expect(text).toContain('22')
  })

  it('keeps the cache tooltip out of the layout while it is hidden', () => {
    const wrapper = mount(UsageStatsCards, {
      props: {
        stats,
      },
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    const tooltip = wrapper.findAll('span').find((el) => el.classes().includes('group-hover:block'))

    expect(tooltip).toBeDefined()
    // `opacity-0` hides the tooltip visually but keeps it in the layout, so its
    // fixed width still widens the document and causes horizontal scrolling on
    // narrow screens. `hidden` (display: none) takes it out of the flow.
    expect(tooltip?.classes()).toContain('hidden')
    expect(tooltip?.classes()).not.toContain('opacity-0')
  })
})


describe('financial completeness', () => {
  it('shows actual subscription consumption independently from standard and balance costs', () => {
    const wrapper = mount(UsageStatsCards, {
      props: { stats: { ...stats, total_actual_cost: 90, total_cost: 66, balance_actual_cost: 0, subscription_actual_cost: 90 } },
      global: { stubs: { Icon: true } },
    })
    const split = wrapper.get('[data-testid="financial-spending-split"]').text()
    expect(split).toContain('financial.balanceSpending: $0.0000')
    expect(split).toContain('financial.subscriptionSpending: $90.0000')
    expect(wrapper.text()).toContain('$66.0000')
  })
  it('labels known subtotals and does not advertise incomplete token/standard sums as exact', () => {
    const wrapper = mount(UsageStatsCards, {
      props: { stats: { ...stats, unknown_amount_count: 2, incomplete_record_count: 2, detail_pending_count: 1, standard_cost_complete: false, token_counts_complete: false } },
      global: { stubs: { Icon: true } },
    })
    expect(wrapper.text()).toContain('financial.knownAmount')
    expect(wrapper.text()).toContain('financial.unknown')
    expect(wrapper.get('[data-testid="financial-usage-notice"]').text()).toContain('financial.amountUnknown')
    expect(wrapper.text()).not.toContain('184')
  })
})


it('does not label a partial supplier-cost sum as complete', () => {
  const wrapper = mount(UsageStatsCards, { props: { stats: { ...stats, total_account_cost: 70, standard_cost_complete: false, incomplete_record_count: 2 } }, global: { stubs: { Icon: true } } })
  expect(wrapper.text()).toContain('Cost financial.unknown')
  expect(wrapper.text()).not.toContain('$70.0000')
})
