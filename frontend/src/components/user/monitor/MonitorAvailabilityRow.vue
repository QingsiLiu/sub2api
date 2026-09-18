<template>
  <div class="mt-2 flex items-end justify-between gap-2">
    <div class="truncate text-[10px] uppercase tracking-widest text-gray-400">
      {{ windowLabel }}
    </div>
    <div class="flex items-baseline gap-0.5">
      <span
        class="text-2xl font-bold tabular-nums leading-none sm:text-3xl"
        :style="colorStyle"
      >
        {{ displayValue }}
      </span>
      <span
        class="text-sm font-semibold leading-none sm:text-base"
        :style="colorStyle"
      >%</span>
    </div>
  </div>
  <div
    v-if="samplesLabel"
    class="mt-0.5 truncate text-right text-[10px] text-gray-400"
  >
    {{ samplesLabel }}
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { hslForPct } from '@/composables/useChannelMonitorFormat'

const props = defineProps<{
  windowLabel: string
  value: number | null
  samplesLabel?: string
}>()

const { t } = useI18n()

const displayValue = computed(() => {
  if (props.value === null || Number.isNaN(props.value)) return t('monitorCommon.latencyEmpty')
  return props.value.toFixed(2)
})

const colorStyle = computed(() => {
  const colour = hslForPct(props.value)
  return colour ? { color: colour } : { color: 'rgb(156 163 175)' }
})
</script>
