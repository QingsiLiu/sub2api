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
