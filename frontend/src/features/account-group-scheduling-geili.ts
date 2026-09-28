import { ref, watch, type Ref } from 'vue'
import apiClient from '@/api/client'

export interface GroupSchedulingRow {
  account_id: number
  account_priority: number
  configured_priority: number
  effective_priority: number | null
  effective_mode: 'auto' | 'fixed' | 'inherit'
  cache_pending: boolean
}

// One batch per selected group, shared contract with S2A. Failure never appears as zero.
export function useGroupSchedulingGeili(group: () => string, accounts: Ref<Array<{id: number}>>) {
  const rows = ref<Record<number, GroupSchedulingRow>>({})
  const unavailable = ref(false)
  let generation = 0
  watch([group, accounts], async () => {
    const current = ++generation
    const id = Number(group())
    rows.value = {}
    unavailable.value = false
    if (!Number.isInteger(id) || id <= 0) return
    try {
      const response = await apiClient.get('/admin/group-scheduling', {params: {group_ids: id}})
      if (current !== generation) return
      const snapshot = response.data.groups?.find((item: {group_id: number}) => item.group_id === id)
      if (!snapshot) throw new Error('missing group')
      rows.value = Object.fromEntries(snapshot.rows.map((row: GroupSchedulingRow) => [row.account_id, row]))
    } catch {
      if (current === generation) unavailable.value = true
    }
  }, {immediate: true})
  return {rows, unavailable}
}
