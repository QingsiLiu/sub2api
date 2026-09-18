<template>
  <button
    type="button"
    class="group flex min-h-0 w-full flex-col rounded-2xl border border-gray-200/80 bg-white/80 p-3 text-left shadow-sm backdrop-blur-xl transition-all duration-200 ease-out hover:border-gray-300 hover:shadow-md dark:border-dark-700/70 dark:bg-dark-800/70 dark:hover:border-primary-500/30 sm:p-4"
    @click="emit('click')"
  >
    <!-- Header: icon + name/model + status chip -->
    <div class="flex min-w-0 items-center gap-2.5">
      <span
        class="grid h-8 w-8 shrink-0 place-items-center rounded-lg ring-1 ring-black/5 dark:ring-white/10"
        :class="[providerGradient(item.provider), providerTintClass]"
      >
        <ProviderIcon :provider="item.provider" :size="20" />
      </span>
      <div class="flex-1 min-w-0">
        <div class="truncate text-sm font-semibold text-gray-900 dark:text-gray-100 sm:text-base">
          {{ item.name }}
        </div>
        <div class="mt-0.5 flex min-w-0 items-center gap-1.5">
          <span
            class="inline-flex items-center rounded-md px-1.5 py-0.5 text-[10px] font-medium flex-shrink-0"
            :class="providerBadgeClass(item.provider)"
          >
            {{ providerLabel(item.provider) }}
          </span>
          <!-- 纯配额模式主模型是占位符 "quota"，展示层替换为本地化「配额」标签 -->
          <span class="font-mono text-xs truncate text-gray-500 dark:text-gray-400">
            {{ formatMonitorModel(item.primary_model) }}
          </span>
          <span
            v-if="item.group_name"
            class="inline-flex items-center rounded-md px-1.5 py-0.5 text-[10px] font-medium bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300 flex-shrink-0"
          >
            {{ item.group_name }}
          </span>
        </div>
      </div>
      <span
        class="shrink-0 rounded-full px-2 py-1 text-[11px] font-semibold"
        :class="statusBadgeClass(item.primary_status)"
      >
        {{ statusLabel(item.primary_status) }}
      </span>
    </div>

    <div class="grid min-w-0 gap-x-4 gap-y-2 lg:grid-cols-[minmax(0,1.15fr)_minmax(180px,0.85fr)] lg:items-center">
      <div class="min-w-0">
        <!-- Metrics -->
        <MonitorMetricPair
          primary-icon="bolt"
          :primary-label="t('monitorCommon.dialogLatency')"
          :primary-value="formatLatency(item.primary_latency_ms)"
          primary-unit="ms"
          secondary-icon="globe"
          :secondary-label="t('monitorCommon.endpointPing')"
          :secondary-value="formatLatency(item.primary_ping_latency_ms)"
          secondary-unit="ms"
        />

        <!-- 配额模式：最新用量/余额快照（服务端已按系统开关剥离，此处 flag 为纵深防御） -->
        <MonitorQuotaView v-if="quotaVisible" :snapshot="item.latest_quota" class="mt-2" />
      </div>

      <div class="min-w-0 border-t border-gray-100 pt-0.5 dark:border-dark-700/60 lg:border-t-0 lg:pt-0">
        <!-- Availability row -->
        <MonitorAvailabilityRow
          :window-label="availabilityLabel"
          :value="availabilityValue"
          :samples-label="extraModelsCountLabel"
        />

        <!-- Timeline -->
        <MonitorTimeline
          :buckets="item.timeline"
          :countdown-seconds="countdownSeconds"
        />
      </div>
    </div>
  </button>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { UserMonitorView } from '@/api/channelMonitor'
import {
  useChannelMonitorFormat,
  providerGradient,
} from '@/composables/useChannelMonitorFormat'
import { isChannelMonitorQuotaVisible } from '@/utils/featureFlags'
import ProviderIcon from './ProviderIcon.vue'
import MonitorMetricPair from './MonitorMetricPair.vue'
import MonitorAvailabilityRow from './MonitorAvailabilityRow.vue'
import MonitorTimeline from './MonitorTimeline.vue'
import MonitorQuotaView from '@/components/common/MonitorQuotaView.vue'

// 图标配色与 utils/platformColors.ts 的平台色对齐（新 4 家）。
const PROVIDER_TINT: Record<string, string> = {
  openai: 'text-emerald-600 dark:text-emerald-300',
  anthropic: 'text-orange-600 dark:text-orange-300',
  gemini: 'text-sky-600 dark:text-sky-300',
  grok: 'text-zinc-700 dark:text-zinc-200',
  antigravity: 'text-purple-600 dark:text-purple-300',
  kimi: 'text-pink-600 dark:text-pink-300',
  zhipu: 'text-indigo-600 dark:text-indigo-300',
  deepseek: 'text-teal-600 dark:text-teal-300',
  opencode_go: 'text-amber-700 dark:text-amber-300',
}

const props = defineProps<{
  item: UserMonitorView
  window: '7d' | '15d' | '30d'
  availabilityValue: number | null
  countdownSeconds: number
}>()

const emit = defineEmits<{
  (e: 'click'): void
}>()

const { t } = useI18n()
const {
  statusLabel,
  statusBadgeClass,
  providerLabel,
  providerBadgeClass,
  formatLatency,
  formatMonitorModel,
} = useChannelMonitorFormat()

const providerTintClass = computed(() =>
  PROVIDER_TINT[props.item.provider] ?? 'text-gray-500 dark:text-gray-300'
)

const quotaVisible = computed(
  () => isChannelMonitorQuotaVisible() && !!props.item.latest_quota
)

const availabilityLabel = computed(() => {
  const win = t(`channelStatus.windowTab.${props.window}`)
  return `${t('monitorCommon.availabilityPrefix')} · ${win}`
})

const extraModelsCountLabel = computed(() => {
  const count = props.item.extra_models?.length ?? 0
  if (count === 0) return undefined
  return t('monitorCommon.extraModelsCount', { n: count })
})
</script>
