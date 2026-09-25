<template>
  <AppLayout>
    <div class="space-y-6">
      <div v-if="loading && !stats" class="flex items-center justify-center py-12"><LoadingSpinner /></div>
      <template v-else-if="stats">
        <UserDashboardStats :stats="stats" :balance="user?.balance || 0" :is-simple="authStore.isSimpleMode" :platform-quotas="platformQuotas" />
        <div class="card p-4"><FinancialDateBasis v-model="dateBasis" @change="refreshAll" /></div>
        <UserDashboardCharts v-model:startDate="startDate" v-model:endDate="endDate" v-model:granularity="granularity" :timezone="financialTimezone(dateBasis)" :loading="loadingCharts" :trend="trendData" :models="modelStats" @dateRangeChange="loadRange" @granularityChange="loadCharts" @refresh="refreshAll" />
        <div class="grid grid-cols-1 gap-6 lg:grid-cols-3">
          <div class="lg:col-span-2"><UserDashboardRecentUsage :data="recentUsage" :loading="loadingUsage" /></div>
          <div class="lg:col-span-1"><UserDashboardQuickActions /></div>
        </div>
      </template>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useAuthStore } from '@/stores/auth'
import { usageAPI, type UserDashboardStats as UserStatsType } from '@/api/usage'
import AppLayout from '@/components/layout/AppLayout.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import FinancialDateBasis from '@/components/common/FinancialDateBasis.vue'
import UserDashboardStats from '@/components/user/dashboard/UserDashboardStats.vue'
import UserDashboardCharts from '@/components/user/dashboard/UserDashboardCharts.vue'
import UserDashboardRecentUsage from '@/components/user/dashboard/UserDashboardRecentUsage.vue'
import UserDashboardQuickActions from '@/components/user/dashboard/UserDashboardQuickActions.vue'
import type { UsageLog, TrendDataPoint, ModelStat, PlatformQuotaItem, UsageDateBasis } from '@/types'
import { getMyPlatformQuotas } from '@/api/user'
import { financialDate, financialTimezone } from '@/utils/financialUsage'

const authStore = useAuthStore()
const user = computed(() => authStore.user)
const stats = ref<UserStatsType | null>(null)
const loading = ref(false)
const loadingUsage = ref(false)
const loadingCharts = ref(false)
const trendData = ref<TrendDataPoint[]>([])
const modelStats = ref<ModelStat[]>([])
const recentUsage = ref<UsageLog[]>([])
const platformQuotas = ref<PlatformQuotaItem[] | null>(null)
const dateBasis = ref<UsageDateBasis>('accounting')
const startDate = ref(financialDate(new Date(Date.now() - 6 * 86400000)))
const endDate = ref(financialDate())
const granularity = ref<'day' | 'hour'>('day')
let statsSeq = 0
let chartsSeq = 0
let recentSeq = 0
const dateParams = () => ({ date_basis: dateBasis.value, timezone: financialTimezone(dateBasis.value) })
const rangeParams = () => ({ ...dateParams(), start_date: startDate.value, end_date: endDate.value })

const loadStats = async () => {
  const seq = ++statsSeq
  const params = dateParams()
  loading.value = true
  try {
    const [, response] = await Promise.all([authStore.refreshUser(), usageAPI.getDashboardStats(params)])
    if (seq === statsSeq) stats.value = response
  } catch (error) { console.error('Failed to load dashboard stats:', error) }
  finally { if (seq === statsSeq) loading.value = false }
}
const loadCharts = async () => {
  const seq = ++chartsSeq
  const params = rangeParams()
  loadingCharts.value = true
  try {
    const [trend, models] = await Promise.all([
      usageAPI.getDashboardTrend({ ...params, granularity: granularity.value }),
      usageAPI.getDashboardModels(params),
    ])
    if (seq === chartsSeq) { trendData.value = trend.trend || []; modelStats.value = models.models || [] }
  } catch (error) { console.error('Failed to load charts:', error) }
  finally { if (seq === chartsSeq) loadingCharts.value = false }
}
const loadRecent = async () => {
  const seq = ++recentSeq
  loadingUsage.value = true
  try {
    const response = await usageAPI.query({ ...rangeParams(), page: 1, page_size: 5 })
    if (seq === recentSeq) recentUsage.value = response.items
  } catch (error) { console.error('Failed to load recent usage:', error) }
  finally { if (seq === recentSeq) loadingUsage.value = false }
}
const loadPlatformQuotas = async () => {
  try { const data = await getMyPlatformQuotas(); platformQuotas.value = data.platform_quotas ?? [] }
  catch (error) { console.warn('Failed to load platform quotas:', error); platformQuotas.value = [] }
}
const loadRange = () => { void loadCharts(); void loadRecent() }
const refreshAll = () => { void loadStats(); loadRange(); void loadPlatformQuotas() }
onMounted(refreshAll)
onUnmounted(() => { ++statsSeq; ++chartsSeq; ++recentSeq })
</script>
