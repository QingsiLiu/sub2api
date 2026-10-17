<template>
  <article
    class="card flex flex-col gap-5 !rounded-3xl !border-0 p-5 shadow-sm ring-1 ring-gray-900/5 dark:!bg-dark-800 dark:ring-dark-700 sm:p-6"
  >
    <header class="flex items-start justify-between gap-3">
      <div class="min-w-0">
        <h2 class="flex flex-wrap items-center gap-2 text-base font-bold text-gray-900 dark:text-white">
          <span class="truncate">{{ group.name }}</span>
          <span
            class="rounded-md bg-gray-100 px-1.5 py-0.5 text-[11px] font-semibold tabular-nums text-gray-500 dark:bg-dark-700 dark:text-dark-300"
          >×{{ group.multiplier }}</span>
        </h2>
        <p class="mt-1 text-xs text-gray-500 dark:text-dark-400">
          {{ t('gptIq.group.counts', counts) }}
        </p>
      </div>
      <span
        class="inline-flex shrink-0 items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-semibold ring-1 ring-inset"
        :class="IQ_PILL_CLASS[group.status]"
      >
        <span class="h-1.5 w-1.5 rounded-full" :class="IQ_DOT_CLASS[group.status]" aria-hidden="true"></span>
        {{ t(`gptIq.status.${group.status}`) }}
      </span>
    </header>

    <div class="flex items-end justify-between gap-4">
      <div>
        <p class="text-[11px] font-medium uppercase tracking-wide text-gray-400 dark:text-dark-500">
          {{ t('gptIq.group.normalRate') }}
        </p>
        <p
          class="mt-1 text-3xl font-black tabular-nums tracking-tight"
          :class="rateTone"
        >
          <template v-if="rate !== null">{{ Math.round(rate * 100) }}<span class="ml-0.5 text-lg font-bold">%</span></template>
          <span v-else class="text-lg font-semibold text-gray-400 dark:text-dark-500">{{ t('gptIq.group.noJudged') }}</span>
        </p>
      </div>
      <p v-if="streak" class="pb-1 text-right text-xs text-gray-500 dark:text-dark-400">
        {{ t('gptIq.group.streak', { n: streak.count, status: t(`gptIq.status.${streak.status}`) }) }}
      </p>
    </div>

    <div>
      <div
        class="grid h-10 gap-[3px] sm:gap-1"
        :style="{ gridTemplateColumns: `repeat(${Math.max(group.minutes.length, 1)}, minmax(0, 1fr))` }"
        role="group"
        :aria-label="t('gptIq.group.lightsLabel', { name: group.name })"
      >
        <button
          v-for="(point, index) in group.minutes"
          :key="point.minute"
          type="button"
          class="rounded-[5px] transition duration-150 hover:-translate-y-0.5 hover:brightness-110 focus:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 focus-visible:ring-offset-2 dark:focus-visible:ring-offset-dark-800"
          :class="[
            IQ_CELL_CLASS[point.status],
            index === group.minutes.length - 1 ? 'ring-2 ring-gray-900/15 ring-offset-1 dark:ring-white/25 dark:ring-offset-dark-800' : ''
          ]"
          :title="cellLabel(point)"
          :aria-label="cellLabel(point)"
          @click="emit('select', point)"
        />
      </div>
      <div class="mt-2 flex justify-between text-[11px] font-medium text-gray-400 dark:text-dark-500">
        <span>{{ t('gptIq.group.axisPast') }}</span>
        <span>{{ t('gptIq.group.axisNow') }}</span>
      </div>
    </div>
  </article>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { IqGroupSnapshot, IqMinutePoint } from '@/api/gptIqMonitor'
import {
  IQ_CELL_CLASS,
  IQ_DOT_CLASS,
  IQ_PILL_CLASS,
  countStatus,
  formatClock,
  latestStreak,
  normalRate,
  reasonLabel
} from './iqFormat'

const props = defineProps<{
  group: IqGroupSnapshot
  expectedAnswer: number
}>()

const emit = defineEmits<{
  (e: 'select', point: IqMinutePoint): void
}>()

const { t, locale } = useI18n()

const counts = computed(() => ({
  normal: countStatus(props.group.minutes, 'normal'),
  degraded: countStatus(props.group.minutes, 'degraded'),
  error: countStatus(props.group.minutes, 'error'),
  skipped: countStatus(props.group.minutes, 'skipped')
}))

const rate = computed(() => normalRate(props.group.minutes))
const streak = computed(() => latestStreak(props.group.minutes))

const rateTone = computed(() => {
  if (rate.value === null) return ''
  if (rate.value >= 0.9) return 'text-emerald-600 dark:text-emerald-400'
  if (rate.value >= 0.6) return 'text-amber-600 dark:text-amber-400'
  return 'text-red-600 dark:text-red-400'
})

function cellLabel(point: IqMinutePoint): string {
  const base = t('gptIq.group.cellLabel', {
    time: formatClock(point.minute, locale.value),
    status: t(`gptIq.status.${point.status}`)
  })
  const reason = reasonLabel(t, point.reason, props.expectedAnswer)
  return reason ? `${base} · ${reason}` : base
}
</script>
