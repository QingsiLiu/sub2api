<template>
  <div class="space-y-3">
    <div>
      <p class="input-label mb-1">{{ t('keys.usagePanels') }}</p>
      <p class="input-hint mt-0">{{ t('keys.usagePanelHint') }}</p>
    </div>

    <div class="space-y-2">
      <section
        v-for="panel in usagePanelOrder"
        :key="panel"
        class="overflow-hidden rounded-xl border border-gray-200 dark:border-dark-600"
      >
        <header class="flex items-center justify-between gap-3 border-b border-gray-100 bg-gray-50 px-3 py-2.5 dark:border-dark-700 dark:bg-dark-800/80">
          <h3 class="text-sm font-medium text-gray-900 dark:text-white">
            {{ t(`keys.usagePanel.${panel}`) }}
          </h3>
          <span
            class="inline-flex items-center rounded-full px-2 py-0.5 text-[11px] font-medium"
            :class="selectedCount(panel) > 0
              ? 'bg-primary-50 text-primary-700 dark:bg-primary-900/30 dark:text-primary-300'
              : 'bg-white text-gray-500 ring-1 ring-inset ring-gray-200 dark:bg-dark-800 dark:text-dark-400 dark:ring-dark-600'"
          >
            {{ t('keys.selectedGroupsCount', { count: selectedCount(panel) }) }}
          </span>
        </header>

        <p
          v-if="!groupsForPanel(panel).length"
          class="px-3 py-4 text-center text-xs text-gray-500 dark:text-dark-400"
        >
          {{ t('keys.usagePanelEmpty') }}
        </p>
        <ul v-else class="divide-y divide-gray-100 dark:divide-dark-700">
          <li
            v-for="row in panelRows(panel)"
            :key="row.group.id"
            class="flex items-center gap-2 px-3 py-2"
            :class="row.selected ? 'bg-primary-50/70 dark:bg-primary-900/15' : 'bg-white dark:bg-transparent'"
          >
            <label class="flex min-w-0 flex-1 cursor-pointer items-center gap-2.5">
              <span class="relative inline-flex h-4 w-4 shrink-0 items-center justify-center">
                <input
                  data-test="key-group-choice"
                  :data-panel="panel"
                  type="checkbox"
                  class="peer sr-only"
                  :value="row.group.id"
                  :checked="row.selected"
                  @change="toggleGroup(row.group.id, ($event.target as HTMLInputElement).checked)"
                />
                <span
                  aria-hidden="true"
                  class="flex h-4 w-4 items-center justify-center rounded border transition-colors peer-focus-visible:ring-2 peer-focus-visible:ring-primary-500/30"
                  :class="row.selected
                    ? 'border-primary-500 bg-primary-500 text-white'
                    : 'border-gray-300 bg-white dark:border-dark-500 dark:bg-dark-800'"
                >
                  <svg v-if="row.selected" class="h-3 w-3" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="3" d="M5 13l4 4L19 7" />
                  </svg>
                </span>
              </span>
              <GroupBadge
                class="min-w-0"
                :name="row.group.name"
                :platform="row.group.platform"
                :subscription-type="row.group.subscription_type"
                :rate-multiplier="rateOf(row.group)"
                :peak-rate-enabled="row.group.peak_rate_enabled"
                :peak-start="row.group.peak_start"
                :peak-end="row.group.peak_end"
                :peak-rate-multiplier="row.group.peak_rate_multiplier"
                always-show-rate
              />
            </label>

            <div v-if="row.selected" class="flex shrink-0 items-center gap-1">
              <span
                class="inline-flex h-5 min-w-[1.25rem] items-center justify-center rounded-md bg-white px-1 text-[11px] font-semibold tabular-nums text-gray-600 ring-1 ring-gray-200 dark:bg-dark-800 dark:text-gray-300 dark:ring-dark-600"
                :aria-label="t('keys.priorityRank', { rank: row.rank })"
              >
                {{ row.rank }}
              </span>
              <button
                type="button"
                class="btn btn-secondary btn-sm !px-2"
                :disabled="row.rank === 1"
                :aria-label="t('keys.moveUp')"
                :data-panel="panel"
                @click="moveGroup(panel, row.rank - 1, -1)"
              >
                <Icon name="arrowUp" size="xs" />
              </button>
              <button
                type="button"
                class="btn btn-secondary btn-sm !px-2"
                :disabled="row.rank === selectedCount(panel)"
                :aria-label="t('keys.moveDown')"
                :data-panel="panel"
                @click="moveGroup(panel, row.rank - 1, 1)"
              >
                <Icon name="arrowDown" size="xs" />
              </button>
            </div>
          </li>
        </ul>
      </section>
    </div>

    <p class="input-hint">{{ t('keys.groupFallbackHint') }}</p>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import GroupBadge from '@/components/common/GroupBadge.vue'
import Icon from '@/components/icons/Icon.vue'
import type { Group } from '@/types'
import {
  USAGE_PANEL_ORDER,
  flattenPanelGroupIds,
  normalizeCompositeGroupIds,
  splitGroupIdsByPanel,
  type UsagePanel
} from '@/utils/usagePanels'

const props = defineProps<{
  modelValue: number[]
  groups: Group[]
  rateOf: (group: Group) => number
}>()

const emit = defineEmits<{
  'update:modelValue': [value: number[]]
}>()

const { t } = useI18n()
const usagePanelOrder = USAGE_PANEL_ORDER

const selectedIds = (panel: UsagePanel) =>
  splitGroupIdsByPanel(props.modelValue, props.groups).panels[panel]

const selectedCount = (panel: UsagePanel) => selectedIds(panel).length

const groupsForPanel = (panel: UsagePanel) =>
  props.groups.filter(group => group.usage_panel === panel)

const panelRows = (panel: UsagePanel) => {
  const chosen = selectedIds(panel)
  const selected = chosen.flatMap((id, index) => {
    const group = props.groups.find(item => item.id === id)
    return group ? [{ group, selected: true, rank: index + 1 }] : []
  })
  const unselected = groupsForPanel(panel)
    .filter(group => !chosen.includes(group.id))
    .map(group => ({ group, selected: false, rank: 0 }))
  return [...selected, ...unselected]
}

const toggleGroup = (id: number, checked: boolean) => {
  const next = checked
    ? [...props.modelValue, id]
    : props.modelValue.filter(groupId => groupId !== id)
  emit('update:modelValue', normalizeCompositeGroupIds(next, props.groups))
}

const moveGroup = (panel: UsagePanel, index: number, delta: number) => {
  const split = splitGroupIdsByPanel(props.modelValue, props.groups)
  const items = [...split.panels[panel]]
  const target = index + delta
  if (target < 0 || target >= items.length) return
  ;[items[index], items[target]] = [items[target], items[index]]
  split.panels[panel] = items
  emit('update:modelValue', flattenPanelGroupIds(split.panels, split.leftover))
}
</script>
