import { describe, it, expect, vi } from 'vitest'
import { mount } from '@vue/test-utils'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

import UserDashboardStats from '../UserDashboardStats.vue'
import type { UserDashboardStats as UserStatsType } from '@/api/usage'

const stats = {
  total_api_keys: 1, active_api_keys: 1, total_requests: 4, total_input_tokens: 0, total_output_tokens: 0,
  total_cache_creation_tokens: 0, total_cache_read_tokens: 0, total_tokens: 0, total_cost: 78, total_actual_cost: 95,
  today_requests: 3, today_input_tokens: 0, today_output_tokens: 0, today_cache_creation_tokens: 0,
  today_cache_read_tokens: 0, today_tokens: 0, today_cost: 76, today_actual_cost: 92,
  average_duration_ms: 0, rpm: 0, tpm: 0, by_platform: [],
} as UserStatsType

describe('UserDashboardStats geili 手机布局', () => {
  it('核心与 Token 卡片在手机上单列，平板起两列、桌面四列', () => {
    const w = mount(UserDashboardStats, { props: { stats, balance: 0, isSimple: false }, global: { stubs: { Icon: true } } })
    const rows = w.findAll('.grid').slice(0, 2)
    expect(rows).toHaveLength(2)
    for (const row of rows) {
      expect(row.classes()).toEqual(expect.arrayContaining(['grid-cols-1', 'sm:grid-cols-2', 'lg:grid-cols-4']))
      expect(row.classes()).not.toContain('grid-cols-2')
    }
    expect(rows[0].text()).toContain('$92.0000')
  })
})
