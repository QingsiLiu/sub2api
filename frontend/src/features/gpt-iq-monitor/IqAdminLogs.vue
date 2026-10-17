<template>
  <section
    class="card overflow-hidden !rounded-3xl !border-0 shadow-sm ring-1 ring-gray-900/5 dark:!bg-dark-800 dark:ring-dark-700"
    aria-labelledby="iq-admin-title"
  >
    <header class="flex flex-wrap items-end justify-between gap-4 border-b border-gray-100 px-5 py-4 dark:border-dark-700 sm:px-6">
      <div>
        <p class="inline-flex items-center gap-1 text-[11px] font-semibold uppercase tracking-wider text-amber-600 dark:text-amber-400">
          <Icon name="lock" size="xs" />
          {{ t('gptIq.admin.kicker') }}
        </p>
        <h2 id="iq-admin-title" class="mt-1 text-lg font-bold text-gray-900 dark:text-white">{{ t('gptIq.admin.title') }}</h2>
        <p class="mt-1 text-xs text-gray-500 dark:text-dark-400">{{ t('gptIq.admin.lede') }}</p>
      </div>
      <nav class="tabs" role="tablist">
        <button
          v-for="tab in tabs"
          :key="tab.value"
          type="button"
          role="tab"
          class="tab"
          :aria-selected="active === tab.value"
          :class="active === tab.value ? 'tab-active' : ''"
          @click="active = tab.value"
        >
          {{ tab.label }}
        </button>
      </nav>
    </header>

    <div class="max-h-[min(60vh,560px)] overflow-auto p-4 sm:p-5">
      <template v-if="active === 'reasoning'">
        <p v-if="!probeLog.length" class="py-6 text-center text-sm text-gray-500 dark:text-dark-400">{{ t('gptIq.admin.emptyReasoning') }}</p>
        <div v-else class="table-container border-0">
          <table class="table min-w-[560px]">
            <thead>
              <tr>
                <th>{{ t('gptIq.admin.time') }}</th>
                <th>{{ t('gptIq.admin.group') }}</th>
                <th>{{ t('gptIq.admin.result') }}</th>
                <th>{{ t('gptIq.admin.answer') }}</th>
                <th>{{ t('gptIq.admin.account') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="item in probeLog" :key="`${item.group_id}-${item.minute}`">
                <td class="whitespace-nowrap tabular-nums">{{ formatStamp(item.minute, locale) }}</td>
                <td>{{ item.group_name }}</td>
                <td>
                  <span class="rounded-full px-2 py-0.5 text-[11px] font-semibold ring-1 ring-inset" :class="IQ_PILL_CLASS[item.status]">
                    {{ t(`gptIq.status.${item.status}`) }}
                  </span>
                </td>
                <td class="tabular-nums">{{ item.answer ?? t('gptIq.detail.noAnswer') }}</td>
                <td :class="item.account ? 'font-medium' : 'text-amber-600 dark:text-amber-400'">{{ item.account || t('gptIq.drawings.pendingAccount') }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </template>

      <template v-else>
        <p v-if="!drawLog.length" class="py-6 text-center text-sm text-gray-500 dark:text-dark-400">{{ t('gptIq.admin.emptyDrawing') }}</p>
        <div v-else class="table-container border-0">
          <table class="table min-w-[720px]">
            <thead>
              <tr>
                <th>{{ t('gptIq.admin.time') }}</th>
                <th>{{ t('gptIq.admin.group') }}</th>
                <th>{{ t('gptIq.admin.result') }}</th>
                <th>{{ t('gptIq.admin.elapsed') }}</th>
                <th>{{ t('gptIq.admin.reason') }}</th>
                <th>{{ t('gptIq.admin.account') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="item in drawLog" :key="`${item.group_id}:${item.observed_at}`">
                <td class="whitespace-nowrap tabular-nums">{{ formatStamp(item.observed_at, locale) }}</td>
                <td>{{ item.group_name }}</td>
                <td>
                  <span
                    class="rounded-full px-2 py-0.5 text-[11px] font-semibold ring-1 ring-inset"
                    :class="IQ_PILL_CLASS[drawingTone(item.status)]"
                  >
                    {{ t(`gptIq.drawings.${item.status}`) }}
                  </span>
                </td>
                <td class="whitespace-nowrap tabular-nums">{{ formatDuration(t, item.elapsed_ms) }}</td>
                <td class="max-w-[280px]">
                  <span class="block">{{ drawReason(item.reason) }}</span>
                  <details v-if="item.attempts?.length" class="mt-1 text-xs text-gray-500 dark:text-dark-400">
                    <summary class="cursor-pointer select-none hover:text-gray-700 dark:hover:text-gray-200">
                      {{ t('gptIq.admin.attempts', { n: item.attempts.length }) }}
                    </summary>
                    <ol class="mt-1 list-decimal space-y-0.5 pl-4">
                      <li v-for="(attempt, index) in item.attempts" :key="index">
                        {{ attempt.stream ? t('gptIq.admin.stream') : t('gptIq.admin.nonStream') }}
                        · {{ formatDuration(t, attempt.elapsed_ms) }} · {{ drawReason(attempt.reason) }}
                      </li>
                    </ol>
                    <p v-if="item.budget_exhausted" class="mt-1">{{ t('gptIq.admin.budget') }}</p>
                  </details>
                </td>
                <td :class="item.account ? 'font-medium' : 'text-amber-600 dark:text-amber-400'">{{ item.account || t('gptIq.drawings.pendingAccount') }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </template>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import type { IqDrawingItem, IqProbeLogItem } from '@/api/gptIqMonitor'
import { IQ_PILL_CLASS, drawingTone, formatDuration, formatStamp, reasonLabel } from './iqFormat'

const props = defineProps<{
  probeLog: IqProbeLogItem[]
  drawLog: IqDrawingItem[]
  expectedAnswer: number
}>()

const { t, locale } = useI18n()

const active = ref<'reasoning' | 'drawing'>('reasoning')
const tabs = computed(() => [
  { value: 'reasoning' as const, label: t('gptIq.admin.reasoningTab') },
  { value: 'drawing' as const, label: t('gptIq.admin.drawingTab') }
])

function drawReason(reason?: string | null): string {
  if (reason === 'ok') return t('gptIq.drawings.ok')
  return reasonLabel(t, reason, props.expectedAnswer) || '—'
}
</script>
