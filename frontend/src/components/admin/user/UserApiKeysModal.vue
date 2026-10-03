<template>
  <BaseDialog :show="show" :title="t('admin.users.userApiKeys')" width="wide" @close="handleClose">
    <div v-if="user" class="space-y-4">
      <div class="flex items-center gap-3 rounded-xl bg-gray-50 p-4 dark:bg-dark-700">
        <div class="flex h-10 w-10 items-center justify-center rounded-full bg-primary-100 dark:bg-primary-900/30">
          <span class="text-lg font-medium text-primary-700 dark:text-primary-300">{{ user.email.charAt(0).toUpperCase() }}</span>
        </div>
        <div><p class="font-medium text-gray-900 dark:text-white">{{ user.email }}</p><p class="text-sm text-gray-500 dark:text-dark-400">{{ user.username }}</p></div>
      </div>
      <div v-if="loading" class="flex justify-center py-8"><svg class="h-8 w-8 animate-spin text-primary-500" fill="none" viewBox="0 0 24 24"><circle class="opacity-25" cx="12" cy="12" r="10"></circle><path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 013 7.938l-2.647 2.647z"></path></svg></div>
      <div v-else-if="apiKeys.length === 0" class="py-8 text-center"><p class="text-sm text-gray-500">{{ t('admin.users.noApiKeys') }}</p></div>
      <div v-else ref="scrollContainerRef" class="max-h-96 space-y-3 overflow-y-auto" @scroll="closeGroupSelector">
        <div v-for="key in apiKeys" :key="key.id" class="rounded-xl border border-gray-200 bg-white p-4 dark:border-dark-600 dark:bg-dark-800">
          <div class="flex items-start justify-between">
            <div class="min-w-0 flex-1">
              <div class="mb-1 flex items-center gap-2"><span class="font-medium text-gray-900 dark:text-white">{{ key.name }}</span><span :class="['badge text-xs', key.status === 'active' ? 'badge-success' : 'badge-danger']">{{ key.status }}</span></div>
              <p class="truncate font-mono text-sm text-gray-500">{{ key.key.substring(0, 20) }}...{{ key.key.substring(key.key.length - 8) }}</p>
            </div>
          </div>
          <div class="mt-3 flex flex-wrap gap-4 text-xs text-gray-500">
            <div class="flex min-w-0 items-start gap-1">
              <span class="shrink-0">{{ t('admin.users.group') }}:</span>
              <div v-if="key.routing_mode === 'composite'" class="min-w-0 space-y-2" data-testid="composite-key-groups">
                <GroupBadge :name="t('keys.compositeKey')" platform="composite" :show-rate="false" />
                <div
                  v-for="panel in compositePanels(key)"
                  :key="panel.panel"
                  class="flex flex-wrap items-center gap-1.5 rounded-lg bg-gray-50 px-2 py-1.5 dark:bg-dark-700/60"
                  :data-testid="`composite-panel-${panel.panel}`"
                >
                  <span class="mr-1 font-medium text-gray-600 dark:text-gray-300">{{ t(`keys.usagePanel.${panel.panel}`) }}</span>
                  <GroupBadge
                    v-for="group in panel.groups"
                    :key="group.id"
                    :name="group.name"
                    :platform="group.platform"
                    :subscription-type="group.subscription_type"
                    :rate-multiplier="group.rate_multiplier"
                    :peak-rate-enabled="group.peak_rate_enabled"
                    :peak-start="group.peak_start"
                    :peak-end="group.peak_end"
                    :peak-rate-multiplier="group.peak_rate_multiplier"
                  />
                  <button
                    v-if="panel.editable"
                    type="button"
                    data-group-selector-trigger
                    class="ml-1 inline-flex items-center gap-1 rounded-md px-1.5 py-1 font-medium text-primary-600 transition-colors hover:bg-primary-50 dark:text-primary-300 dark:hover:bg-primary-900/20"
                    :disabled="updatingKeyIds.has(key.id)"
                    @click="openGroupSelector(key, panel.panel, $event)"
                  >
                    <svg v-if="updatingKeyIds.has(key.id)" class="h-3 w-3 animate-spin" fill="none" viewBox="0 0 24 24"><circle class="opacity-25" cx="12" cy="12" r="10"></circle><path fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 5.373 0 4 12h4z"></path></svg>
                    <span>{{ t('admin.users.changeGroup') }}</span>
                  </button>
                </div>
                <span v-if="compositeUnassignedLabels(key).length" class="text-gray-400 italic">{{ t('admin.users.compositeUnassigned') }}: {{ compositeUnassignedLabels(key).join(', ') }}</span>
              </div>
              <button
                v-else
                :ref="(el) => setGroupButtonRef(key.id, el)"
                data-group-selector-trigger
                @click="openGroupSelector(key, null, $event)"
                class="-mx-1 -my-0.5 flex cursor-pointer items-center gap-1 rounded-md px-1 py-0.5 transition-colors hover:bg-gray-100 dark:hover:bg-dark-700"
                :disabled="updatingKeyIds.has(key.id)"
              >
                <GroupBadge
                  v-if="key.group_id && key.group"
                  :name="key.group.name"
                  :platform="key.group.platform"
                  :subscription-type="key.group.subscription_type"
                  :rate-multiplier="key.group.rate_multiplier"
                  :peak-rate-enabled="key.group.peak_rate_enabled"
                  :peak-start="key.group.peak_start"
                  :peak-end="key.group.peak_end"
                  :peak-rate-multiplier="key.group.peak_rate_multiplier"
                />
                <span v-else class="text-gray-400 italic">{{ t('admin.users.none') }}</span>
                <svg v-if="updatingKeyIds.has(key.id)" class="h-3 w-3 animate-spin text-primary-500" fill="none" viewBox="0 0 24 24"><circle class="opacity-25" cx="12" cy="12" r="10"></circle><path fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 5.373 0 4 12h4z"></path></svg>
                <svg v-else class="h-3 w-3 text-gray-400" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2"><path stroke-linecap="round" stroke-linejoin="round" d="M8.25 15L12 18.75 15.75 15m-7.5-6L12 5.25 15.75 9" /></svg>
              </button>
            </div>
            <div class="flex items-center gap-1"><span>{{ t('admin.users.columns.created') }}: {{ formatDateTime(key.created_at) }}</span></div>
          </div>
        </div>
      </div>
    </div>
  </BaseDialog>

  <Teleport to="body">
    <div
      v-if="groupSelectorKeyId !== null && dropdownPosition"
      ref="dropdownRef"
      class="animate-in fade-in slide-in-from-top-2 fixed z-[100000020] w-72 overflow-hidden rounded-xl bg-white shadow-lg ring-1 ring-black/5 duration-200 dark:bg-dark-800 dark:ring-white/10"
      :style="{ top: dropdownPosition.top + 'px', left: dropdownPosition.left + 'px' }"
    >
      <div class="max-h-72 overflow-y-auto p-1.5">
        <template v-if="selectedKeyForGroup?.routing_mode !== 'composite'">
          <button
            @click="changeGroup(selectedKeyForGroup!, null)"
            :class="[
              'flex w-full items-center rounded-lg px-3 py-2 text-sm transition-colors',
              !selectedKeyForGroup?.group_id ? 'bg-primary-50 dark:bg-primary-900/20' : 'hover:bg-gray-100 dark:hover:bg-dark-700'
            ]"
          >
            <span class="text-gray-500 italic">{{ t('admin.users.none') }}</span>
            <svg v-if="!selectedKeyForGroup?.group_id" class="ml-auto h-4 w-4 shrink-0 text-primary-600 dark:text-primary-400" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2"><path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" /></svg>
          </button>
        </template>
        <div v-else class="px-3 py-2 text-xs font-medium text-gray-500 dark:text-gray-400">
          {{ t(`keys.usagePanel.${groupSelectorPanel}`) }} · {{ t('admin.users.changeGroup') }}
        </div>
        <p v-if="selectableGroups.length === 0" class="px-3 py-4 text-center text-xs text-gray-500 dark:text-gray-400">{{ t('admin.users.compositeNoAvailableGroups') }}</p>
        <button
          v-for="group in selectableGroups"
          :key="group.id"
          @click="selectedKeyForGroup?.routing_mode === 'composite' ? toggleCompositeGroup(group.id) : changeGroup(selectedKeyForGroup!, group.id)"
          :class="[
            'flex w-full items-center justify-between rounded-lg px-3 py-2 text-sm transition-colors',
            isSelectedGroup(group.id) ? 'bg-primary-50 dark:bg-primary-900/20' : 'hover:bg-gray-100 dark:hover:bg-dark-700'
          ]"
        >
          <GroupOptionItem
            :name="group.name"
            :platform="group.platform"
            :subscription-type="group.subscription_type"
            :rate-multiplier="group.rate_multiplier"
            :peak-rate-enabled="group.peak_rate_enabled"
            :peak-start="group.peak_start"
            :peak-end="group.peak_end"
            :peak-rate-multiplier="group.peak_rate_multiplier"
            :description="group.description"
            :selected="isSelectedGroup(group.id)"
          />
        </button>
        <div v-if="selectedKeyForGroup?.routing_mode === 'composite'" class="flex items-center justify-between border-t border-gray-200 px-2 pt-2 dark:border-dark-600">
          <span class="text-xs text-gray-500 dark:text-gray-400">{{ pendingCompositeGroupIDs.length }} {{ t('admin.users.selectedGroups') }}</span>
          <button
            type="button"
            data-testid="save-composite-groups"
            class="btn btn-primary px-3 py-1.5 text-xs"
            :disabled="pendingCompositeGroupIDs.length === 0 || updatingKeyIds.has(selectedKeyForGroup.id)"
            @click="saveCompositeGroups"
          >
            {{ t('common.save') }}
          </button>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted, type ComponentPublicInstance } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import { formatDateTime } from '@/utils/format'
import type { AdminUser, AdminGroup, ApiKey, Group } from '@/types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import GroupBadge from '@/components/common/GroupBadge.vue'
import GroupOptionItem from '@/components/common/GroupOptionItem.vue'

const props = defineProps<{ show: boolean; user: AdminUser | null }>()
const emit = defineEmits(['close'])
const { t } = useI18n()
const appStore = useAppStore()

const COMPOSITE_PANEL_ORDER = ['gpt', 'grok', 'claude', 'national', 'gemini'] as const
type CompositePanel = typeof COMPOSITE_PANEL_ORDER[number]

const propsPanel = (value: string | null | undefined): value is CompositePanel =>
  COMPOSITE_PANEL_ORDER.includes(value as CompositePanel)

const apiKeys = ref<ApiKey[]>([])
const allGroups = ref<AdminGroup[]>([])
const loading = ref(false)
let requestVersion = 0
const updatingKeyIds = ref(new Set<number>())
const groupSelectorKeyId = ref<number | null>(null)
const groupSelectorPanel = ref<CompositePanel | null>(null)
const dropdownPosition = ref<{ top: number; left: number } | null>(null)
const dropdownRef = ref<HTMLElement | null>(null)
const scrollContainerRef = ref<HTMLElement | null>(null)
const groupButtonRefs = ref<Map<number, HTMLElement>>(new Map())
const pendingCompositeGroupIDs = ref<number[]>([])

const selectedKeyForGroup = computed(() => {
  if (groupSelectorKeyId.value === null) return null
  return apiKeys.value.find((k) => k.id === groupSelectorKeyId.value) || null
})

const groupCatalogForKey = (key: ApiKey): Group[] => {
  const byID = new Map<number, Group>()
  for (const group of key.groups || []) byID.set(group.id, group)
  for (const group of allGroups.value) if (!byID.has(group.id)) byID.set(group.id, group)
  return (key.group_ids || []).map(id => byID.get(id)).filter((group): group is Group => !!group)
}

const compositePanels = (key: ApiKey): Array<{ panel: CompositePanel; groups: Group[]; editable: boolean }> => {
  const grouped = new Map<CompositePanel, Group[]>()
  for (const group of groupCatalogForKey(key)) {
    const panel = group.usage_panel
    if (!propsPanel(panel)) continue
    const groups = grouped.get(panel) || []
    groups.push(group)
    grouped.set(panel, groups)
  }
  return COMPOSITE_PANEL_ORDER
    .filter(panel => grouped.has(panel))
    .map(panel => ({ panel, groups: grouped.get(panel) || [], editable: true }))
}

const compositeUnassignedLabels = (key: ApiKey): string[] => {
  const byID = new Map(groupCatalogForKey(key).map(group => [group.id, group]))
  const panels = new Set(compositePanels(key).flatMap(panel => panel.groups.map(group => group.id)))
  return (key.group_ids || []).filter(id => !panels.has(id)).map(id => byID.get(id)?.name || `#${id}`)
}

const setGroupButtonRef = (keyId: number, el: Element | ComponentPublicInstance | null) => {
  if (el instanceof HTMLElement) groupButtonRefs.value.set(keyId, el)
  else groupButtonRefs.value.delete(keyId)
}

const selectableGroups = computed<AdminGroup[]>(() => {
  const key = selectedKeyForGroup.value
  if (!key || key.routing_mode !== 'composite' || !groupSelectorPanel.value) return allGroups.value
  return allGroups.value.filter(group =>
    group.status === 'active' && group.platform !== 'composite' && group.usage_panel === groupSelectorPanel.value
  )
})

const isSelectedGroup = (groupID: number): boolean => {
  const key = selectedKeyForGroup.value
  if (!key) return false
  if (key.routing_mode === 'composite') return pendingCompositeGroupIDs.value.includes(groupID)
  return key.group_id === groupID
}

watch(() => [props.show, props.user?.id] as const, ([show], _, onCleanup) => {
  onCleanup(() => { requestVersion++ })
  closeGroupSelector()
  if (show && props.user) {
    load()
    loadGroups()
  }
})

const load = async () => {
  if (!props.user) return
  const version = ++requestVersion
  apiKeys.value = []
  loading.value = true
  groupButtonRefs.value.clear()
  try {
    const res = await adminAPI.users.getUserApiKeys(props.user.id)
    if (version === requestVersion) apiKeys.value = res.items || []
  } catch (error) {
    if (version !== requestVersion) return
    console.error('Failed to load API keys:', error)
  } finally {
    if (version === requestVersion) loading.value = false
  }
}

const loadGroups = async () => {
  try {
    allGroups.value = await adminAPI.groups.getAll()
  } catch (error) {
    console.error('Failed to load groups:', error)
  }
}

const DROPDOWN_HEIGHT = 272
const DROPDOWN_GAP = 4

const openGroupSelector = (key: ApiKey, panel: CompositePanel | null, event?: MouseEvent) => {
  if (key.routing_mode === 'composite' && !panel) return
  if (groupSelectorKeyId.value === key.id && groupSelectorPanel.value === panel) {
    closeGroupSelector()
    return
  }

  const buttonEl = (event?.currentTarget as HTMLElement | null) || groupButtonRefs.value.get(key.id)
  if (buttonEl) {
    const rect = buttonEl.getBoundingClientRect()
    const spaceBelow = window.innerHeight - rect.bottom
    const openUpward = spaceBelow < DROPDOWN_HEIGHT && rect.top > spaceBelow
    dropdownPosition.value = {
      top: openUpward ? rect.top - DROPDOWN_HEIGHT - DROPDOWN_GAP : rect.bottom + DROPDOWN_GAP,
      left: Math.max(8, Math.min(rect.left, window.innerWidth - 296))
    }
  }
  groupSelectorKeyId.value = key.id
  groupSelectorPanel.value = panel
  pendingCompositeGroupIDs.value = panel
    ? (key.group_ids || []).filter((id) => groupCatalogForKey(key).find((group) => group.id === id)?.usage_panel === panel)
    : []
}

const closeGroupSelector = () => {
  groupSelectorKeyId.value = null
  groupSelectorPanel.value = null
  dropdownPosition.value = null
  pendingCompositeGroupIDs.value = []
}

const changeGroup = async (key: ApiKey, newGroupId: number | null) => {
  closeGroupSelector()
  if (key.routing_mode === 'composite') {
    if (newGroupId === null || (key.group_ids || []).includes(newGroupId)) return
  } else if (key.group_id === newGroupId || (!key.group_id && newGroupId === null)) {
    return
  }

  updatingKeyIds.value.add(key.id)
  try {
    const result = await adminAPI.apiKeys.updateApiKeyGroup(key.id, newGroupId)
    const idx = apiKeys.value.findIndex((k) => k.id === key.id)
    if (idx !== -1) apiKeys.value[idx] = result.api_key
    if (result.auto_granted_group_access && result.granted_group_name) {
      appStore.showSuccess(t('admin.users.groupChangedWithGrant', { group: result.granted_group_name }))
    } else {
      appStore.showSuccess(t('admin.users.groupChangedSuccess'))
    }
  } catch (error: any) {
    appStore.showError(error?.message || t('admin.users.groupChangeFailed'))
  } finally {
    updatingKeyIds.value.delete(key.id)
  }
}

const toggleCompositeGroup = (groupID: number) => {
  pendingCompositeGroupIDs.value = pendingCompositeGroupIDs.value.includes(groupID)
    ? pendingCompositeGroupIDs.value.filter((id) => id !== groupID)
    : [...pendingCompositeGroupIDs.value, groupID]
}

const saveCompositeGroups = async () => {
  const key = selectedKeyForGroup.value
  if (!key || key.routing_mode !== 'composite' || pendingCompositeGroupIDs.value.length === 0) return
  const groupIDs = [...pendingCompositeGroupIDs.value]
  closeGroupSelector()
  updatingKeyIds.value.add(key.id)
  try {
    const result = await adminAPI.apiKeys.updateApiKeyGroups(key.id, groupIDs)
    const idx = apiKeys.value.findIndex((item) => item.id === key.id)
    if (idx !== -1) apiKeys.value[idx] = result.api_key
    appStore.showSuccess(t('admin.users.groupChangedSuccess'))
  } catch (error: any) {
    appStore.showError(error?.message || t('admin.users.groupChangeFailed'))
  } finally {
    updatingKeyIds.value.delete(key.id)
  }
}

const handleKeyDown = (event: KeyboardEvent) => {
  if (event.key === 'Escape' && groupSelectorKeyId.value !== null) {
    event.stopPropagation()
    closeGroupSelector()
  }
}

const handleClickOutside = (event: MouseEvent) => {
  const target = event.target as HTMLElement
  if (target.closest('[data-group-selector-trigger]')) return
  if (dropdownRef.value && !dropdownRef.value.contains(target)) closeGroupSelector()
}

const handleClose = () => {
  closeGroupSelector()
  emit('close')
}

onMounted(() => {
  document.addEventListener('click', handleClickOutside)
  document.addEventListener('keydown', handleKeyDown, true)
})

onUnmounted(() => {
  document.removeEventListener('click', handleClickOutside)
  document.removeEventListener('keydown', handleKeyDown, true)
})
</script>
