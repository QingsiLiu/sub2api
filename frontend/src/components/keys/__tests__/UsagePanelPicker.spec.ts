import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'

import type { Group } from '@/types'
import UsagePanelPicker from '../UsagePanelPicker.vue'

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: { count?: number; rank?: number }) => {
        if (key === 'keys.selectedGroupsCount') return `selected ${params?.count ?? 0}`
        if (key === 'keys.priorityRank') return `rank ${params?.rank ?? 0}`
        return key
      },
    }),
  }
})

const group = (partial: Partial<Group> & Pick<Group, 'id' | 'name' | 'usage_panel'>): Group => ({
  description: null,
  platform: 'openai',
  rate_multiplier: 1,
  subscription_rate_multiplier: 1,
  is_exclusive: false,
  status: 'active',
  subscription_type: 'standard',
  peak_rate_enabled: false,
  peak_start: '',
  peak_end: '',
  peak_rate_multiplier: 1,
  ...partial,
} as Group)

const groups = [
  group({ id: 1, name: 'GPT stable', usage_panel: 'gpt', platform: 'openai', rate_multiplier: 0.15 }),
  group({ id: 2, name: 'Kimi', usage_panel: 'national', platform: 'kimi', rate_multiplier: 1 }),
  group({ id: 3, name: 'GPT pro', usage_panel: 'gpt', platform: 'openai', rate_multiplier: 0.2 }),
]

const mountPicker = (modelValue: number[] = []) =>
  mount(UsagePanelPicker, {
    props: {
      modelValue,
      groups,
      rateOf: (item: Group) => item.rate_multiplier,
    },
    global: {
      stubs: {
        GroupBadge: { props: ['name'], template: '<span>{{ name }}</span>' },
        Icon: { props: ['name'], template: '<span>{{ name }}</span>' },
      },
    },
  })

describe('UsagePanelPicker', () => {
  it('puts selected groups first and keeps reorder controls on those rows only', async () => {
    const wrapper = mountPicker([3])
    const gptChoices = wrapper.findAll('[data-test="key-group-choice"][data-panel="gpt"]')
    expect(gptChoices.map(input => Number((input.element as HTMLInputElement).value))).toEqual([3, 1])
    expect(wrapper.findAll('[data-panel="gpt"][aria-label="keys.moveUp"]')).toHaveLength(1)
    expect(wrapper.text()).toContain('GPT pro')
    expect(wrapper.get('[aria-label="rank 1"]').text()).toBe('1')
  })

  it('emits composite group ids in the fixed panel order', async () => {
    const wrapper = mountPicker()
    await wrapper.get('[data-test="key-group-choice"][data-panel="national"]').setValue(true)
    await wrapper.setProps({ modelValue: wrapper.emitted('update:modelValue')?.at(-1)?.[0] })
    await wrapper.get('[data-test="key-group-choice"][data-panel="gpt"]').setValue(true)
    expect(wrapper.emitted('update:modelValue')?.at(-1)?.[0]).toEqual([1, 2])
  })

  it('moves a selected group up within its panel', async () => {
    const wrapper = mountPicker([1, 3])
    await wrapper.findAll('[data-panel="gpt"][aria-label="keys.moveUp"]')[1].trigger('click')
    expect(wrapper.emitted('update:modelValue')?.at(-1)?.[0]).toEqual([3, 1])
  })
})
