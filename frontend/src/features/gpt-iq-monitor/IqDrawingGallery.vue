<template>
  <section
    class="card overflow-hidden !rounded-3xl !border-0 shadow-sm ring-1 ring-gray-900/5 dark:!bg-dark-800 dark:ring-dark-700"
    aria-labelledby="iq-drawings-title"
  >
    <header class="flex flex-wrap items-end justify-between gap-4 border-b border-gray-100 px-5 py-4 dark:border-dark-700 sm:px-6">
      <div class="min-w-0">
        <p class="text-[11px] font-semibold uppercase tracking-wider text-primary-600 dark:text-primary-400">
          {{ t('gptIq.drawings.kicker') }}
        </p>
        <h2 id="iq-drawings-title" class="mt-1 text-lg font-bold text-gray-900 dark:text-white">
          {{ drawings.title }}
        </h2>
        <p class="mt-1 text-xs text-gray-500 dark:text-dark-400">
          {{ t('gptIq.drawings.lede', { interval: drawings.interval_minutes, keep: drawings.keep }) }}
        </p>
      </div>
      <nav v-if="drawings.groups.length > 1" class="tabs" role="tablist">
        <button
          v-for="group in drawings.groups"
          :key="group.id"
          type="button"
          role="tab"
          class="tab"
          :aria-selected="activeId === group.id"
          :class="activeId === group.id ? 'tab-active' : ''"
          @click="activeId = group.id"
        >
          {{ group.name }}
        </button>
      </nav>
    </header>

    <div class="space-y-4 p-5 sm:p-6">
      <blockquote
        class="rounded-2xl border-l-4 border-primary-400 bg-gray-50 px-4 py-3 text-sm leading-relaxed text-gray-600 dark:border-primary-500 dark:bg-dark-900/40 dark:text-dark-300"
      >
        {{ drawings.question }}
      </blockquote>

      <p v-if="!activeGroup || !activeGroup.items.length" class="rounded-2xl bg-gray-50 px-4 py-8 text-center text-sm text-gray-500 dark:bg-dark-900/30 dark:text-dark-400">
        {{ t('gptIq.drawings.empty') }}
      </p>
      <ul v-else class="grid grid-cols-2 gap-3 sm:grid-cols-3 sm:gap-4 xl:grid-cols-5">
        <li
          v-for="item in activeGroup.items"
          :key="keyOf(item)"
          class="group/card flex flex-col overflow-hidden rounded-2xl bg-white ring-1 ring-gray-900/5 transition hover:-translate-y-0.5 hover:shadow-lg dark:bg-dark-900/40 dark:ring-dark-700"
        >
          <button
            type="button"
            class="relative aspect-[4/3] w-full overflow-hidden bg-white focus:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary-500"
            :title="t('gptIq.drawings.enlarge')"
            :disabled="!item.svg"
            @click="preview = item"
          >
            <img
              v-if="item.svg"
              :src="svgDataUrl(item.svg)"
              :alt="altOf(item)"
              class="h-full w-full object-contain transition duration-300 group-hover/card:scale-[1.03]"
              loading="lazy"
            />
            <span v-else class="flex h-full items-center justify-center px-3 text-center text-xs text-gray-400">
              {{ item.reason ? reasonLabel(t, item.reason) : t('gptIq.drawings.noSvg') }}
            </span>
          </button>
          <footer class="flex items-center justify-between gap-2 border-t border-gray-100 px-3 py-2 dark:border-dark-700">
            <div class="min-w-0">
              <time class="block text-xs font-medium tabular-nums text-gray-700 dark:text-gray-200">
                {{ formatStamp(item.observed_at, locale) }}
              </time>
              <span
                v-if="isAdmin"
                class="block truncate text-[11px]"
                :class="accountOf(item) ? 'text-gray-500 dark:text-dark-400' : 'text-amber-600 dark:text-amber-400'"
              >
                {{ accountOf(item) || t('gptIq.drawings.pendingAccount') }}
              </span>
            </div>
            <div class="flex shrink-0 items-center gap-1">
              <span
                class="rounded-full px-2 py-0.5 text-[11px] font-semibold ring-1 ring-inset"
                :class="IQ_PILL_CLASS[drawingTone(item.status)]"
              >
                {{ t(`gptIq.drawings.${item.status}`) }}
              </span>
              <button
                v-if="canManage"
                type="button"
                class="rounded-lg p-1 text-gray-400 transition hover:bg-red-50 hover:text-red-600 disabled:opacity-50 dark:hover:bg-red-500/10 dark:hover:text-red-400"
                :title="deletingKey === keyOf(item) ? t('gptIq.drawings.deleting') : t('gptIq.drawings.delete')"
                :aria-label="t('gptIq.drawings.delete')"
                :disabled="deletingKey === keyOf(item)"
                @click="emit('delete', item)"
              >
                <Icon name="trash" size="sm" />
              </button>
            </div>
          </footer>
        </li>
      </ul>
    </div>

    <BaseDialog
      :show="!!preview"
      :title="preview ? `${preview.group_name} · ${formatStamp(preview.observed_at, locale)}` : ''"
      width="wide"
      :close-on-click-outside="true"
      @close="preview = null"
    >
      <div v-if="preview?.svg" class="overflow-hidden rounded-2xl bg-white ring-1 ring-gray-900/5">
        <img :src="svgDataUrl(preview.svg)" :alt="altOf(preview)" class="mx-auto max-h-[70vh] w-full object-contain" />
      </div>
      <p v-if="preview" class="mt-3 text-xs text-gray-500 dark:text-dark-400">
        {{ t('gptIq.drawings.elapsed', { time: formatDuration(t, preview.elapsed_ms) }) }}
        <template v-if="isAdmin && accountOf(preview)"> · {{ accountOf(preview) }}</template>
      </p>
    </BaseDialog>
  </section>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import type { IqDrawingItem, IqDrawingsSnapshot } from '@/api/gptIqMonitor'
import { IQ_PILL_CLASS, drawingTone, formatDuration, formatStamp, reasonLabel, svgDataUrl } from './iqFormat'

const props = defineProps<{
  drawings: IqDrawingsSnapshot
  drawLog: IqDrawingItem[]
  isAdmin: boolean
  canManage: boolean
  deletingKey: string | null
}>()

const emit = defineEmits<{
  (e: 'delete', item: IqDrawingItem): void
}>()

const { t, locale } = useI18n()

const activeId = ref<number | null>(props.drawings.groups[0]?.id ?? null)
const preview = ref<IqDrawingItem | null>(null)

watch(
  () => props.drawings.groups.map((group) => group.id),
  (ids) => {
    if (activeId.value === null || !ids.includes(activeId.value)) activeId.value = ids[0] ?? null
  }
)

const activeGroup = computed(() => props.drawings.groups.find((group) => group.id === activeId.value))

function keyOf(item: IqDrawingItem): string {
  return `${item.group_id}:${item.observed_at}`
}

function accountOf(item: IqDrawingItem): string {
  if (item.account) return item.account
  return props.drawLog.find((row) => keyOf(row) === keyOf(item))?.account || ''
}

function altOf(item: IqDrawingItem): string {
  return t('gptIq.drawings.alt', { group: item.group_name, time: formatStamp(item.observed_at, locale.value) })
}
</script>
