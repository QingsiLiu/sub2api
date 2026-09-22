export const USAGE_PANEL_ORDER = ['gpt', 'grok', 'claude', 'national', 'gemini'] as const

export type UsagePanel = (typeof USAGE_PANEL_ORDER)[number]

export function isUsagePanel(value: string | null | undefined): value is UsagePanel {
  return USAGE_PANEL_ORDER.includes(value as UsagePanel)
}

export function emptyUsagePanelMap(): Record<UsagePanel, number[]> {
  return {
    gpt: [],
    grok: [],
    claude: [],
    national: [],
    gemini: []
  }
}

export function splitGroupIdsByPanel(
  ids: number[],
  groups: Array<{ id: number; usage_panel?: string | null }>
): { panels: Record<UsagePanel, number[]>; leftover: number[] } {
  const byID = new Map(groups.map(group => [group.id, group]))
  const panels = emptyUsagePanelMap()
  const leftover: number[] = []
  const seen = new Set<number>()
  for (const id of ids) {
    if (id <= 0 || seen.has(id)) continue
    seen.add(id)
    const panel = byID.get(id)?.usage_panel
    if (isUsagePanel(panel)) panels[panel].push(id)
    else leftover.push(id)
  }
  return { panels, leftover }
}

export function flattenPanelGroupIds(
  panels: Record<UsagePanel, number[]>,
  leftover: number[] = []
): number[] {
  const ids: number[] = []
  const seen = new Set<number>()
  const push = (id: number) => {
    if (id <= 0 || seen.has(id)) return
    seen.add(id)
    ids.push(id)
  }
  for (const panel of USAGE_PANEL_ORDER) {
    for (const id of panels[panel] ?? []) push(id)
  }
  for (const id of leftover) push(id)
  return ids
}

export function normalizeCompositeGroupIds(
  ids: number[],
  groups: Array<{ id: number; usage_panel?: string | null }>
): number[] {
  const split = splitGroupIdsByPanel(ids, groups)
  return flattenPanelGroupIds(split.panels, split.leftover)
}

export function selectedUsagePanels(
  ids: number[],
  groups: Array<{ id: number; usage_panel?: string | null }>
): UsagePanel[] {
  const { panels } = splitGroupIdsByPanel(ids, groups)
  return USAGE_PANEL_ORDER.filter(panel => panels[panel].length > 0)
}

/**
 * Replace every group on the selected group's usage panel and keep the other
 * panels. A single-group edit on a composite key is a panel replacement, not a
 * conversion to a single-group key.
 */
export function replaceCompositeGroup(
  existing: number[],
  groups: Array<{ id: number; usage_panel?: string | null }>,
  selectedId: number
): number[] | null {
  const selected = groups.find(group => group.id === selectedId)
  if (!selected || !isUsagePanel(selected.usage_panel)) return null
  const panel = selected.usage_panel
  const kept = existing.filter(id => {
    const group = groups.find(item => item.id === id)
    return group?.usage_panel !== panel
  })
  return normalizeCompositeGroupIds([selectedId, ...kept], groups)
}
