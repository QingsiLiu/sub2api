import { describe, expect, it } from 'vitest'
import {
  flattenPanelGroupIds,
  normalizeCompositeGroupIds,
  selectedUsagePanels,
  splitGroupIdsByPanel
} from '../usagePanels'

const groups = [
  { id: 1, usage_panel: 'gpt' },
  { id: 2, usage_panel: 'national' },
  { id: 3, usage_panel: 'gpt' },
  { id: 4, usage_panel: 'claude' },
  { id: 5 }
]

describe('usagePanels', () => {
  it('keeps GPT before national even when national was selected first', () => {
    expect(normalizeCompositeGroupIds([2, 1], groups)).toEqual([1, 2])
  })

  it('preserves intra-panel order and appends unassigned groups', () => {
    const split = splitGroupIdsByPanel([3, 1, 4, 5, 2], groups)
    expect(split.panels.gpt).toEqual([3, 1])
    expect(split.panels.claude).toEqual([4])
    expect(split.panels.national).toEqual([2])
    expect(split.leftover).toEqual([5])
    expect(flattenPanelGroupIds(split.panels, split.leftover)).toEqual([3, 1, 4, 2, 5])
  })

  it('summarizes selected panels in the fixed panel order', () => {
    expect(selectedUsagePanels([2, 4, 1], groups)).toEqual(['gpt', 'claude', 'national'])
  })
})
