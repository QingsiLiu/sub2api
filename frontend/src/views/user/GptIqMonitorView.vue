<template>
  <AppLayout>
    <div class="space-y-6 pb-12">
      <!-- Header: title, live state, config and legend -->
      <section
        class="card relative overflow-hidden !rounded-3xl !border-0 p-0 shadow-sm ring-1 ring-gray-900/5 dark:!bg-dark-800 dark:ring-dark-700"
      >
        <div
          class="pointer-events-none absolute -right-24 -top-24 h-64 w-64 rounded-full bg-primary-400/10 blur-3xl dark:bg-primary-500/10"
          aria-hidden="true"
        ></div>
        <header class="relative flex flex-wrap items-start justify-between gap-4 px-5 py-5 sm:px-6">
          <div class="flex min-w-0 items-start gap-3.5">
            <span
              class="inline-flex h-11 w-11 shrink-0 items-center justify-center rounded-2xl bg-gradient-to-br from-primary-500 to-primary-600 text-white shadow-md shadow-primary-500/25"
            >
              <Icon name="lightbulb" size="md" />
            </span>
            <div class="min-w-0">
              <p class="text-[11px] font-semibold uppercase tracking-wider text-primary-600 dark:text-primary-400">
                {{ t('gptIq.eyebrow') }}
              </p>
              <h1 class="mt-0.5 text-xl font-black text-gray-900 dark:text-white">{{ t('gptIq.title') }}</h1>
              <p class="mt-1 text-xs text-gray-500 dark:text-dark-400">
                {{ t('gptIq.subtitle', { interval: intervalMinutes, answer: expectedAnswer }) }}
              </p>
            </div>
          </div>

          <div class="flex items-center gap-3">
            <div class="text-right text-xs leading-5">
              <p class="flex items-center justify-end gap-1.5 font-medium text-gray-700 dark:text-gray-200">
                <span class="relative flex h-2 w-2">
                  <span
                    v-if="snapshot && !loading"
                    class="absolute inline-flex h-full w-full animate-ping rounded-full bg-emerald-400 opacity-60"
                  ></span>
                  <span
                    class="relative inline-flex h-2 w-2 rounded-full"
                    :class="snapshot && !loading ? 'bg-emerald-500' : 'bg-gray-300 dark:bg-dark-500'"
                  ></span>
                </span>
                <template v-if="snapshot?.updated_at">{{ t('gptIq.updatedAt', { time: formatClock(snapshot.updated_at, locale) }) }}</template>
                <template v-else>{{ t('gptIq.waiting') }}</template>
              </p>
              <p class="tabular-nums text-gray-400 dark:text-dark-500">
                {{ loading ? t('gptIq.refreshing') : t('gptIq.nextRefresh', { s: countdown }) }}
              </p>
            </div>
            <button
              type="button"
              class="relative inline-flex h-11 w-11 items-center justify-center rounded-full text-gray-600 transition hover:bg-gray-100 hover:text-primary-600 disabled:cursor-wait dark:text-gray-300 dark:hover:bg-dark-700 dark:hover:text-primary-400"
              :title="t('gptIq.refresh')"
              :aria-label="t('gptIq.refresh')"
              :disabled="loading"
              @click="reload"
            >
              <svg class="absolute inset-0 h-full w-full -rotate-90" viewBox="0 0 44 44" aria-hidden="true">
                <circle cx="22" cy="22" r="20" fill="none" stroke-width="2" class="stroke-gray-100 dark:stroke-dark-700" />
                <circle
                  cx="22"
                  cy="22"
                  r="20"
                  fill="none"
                  stroke-width="2"
                  stroke-linecap="round"
                  class="stroke-primary-500 transition-[stroke-dashoffset] duration-1000 ease-linear"
                  :stroke-dasharray="RING"
                  :stroke-dashoffset="RING * (1 - ringProgress)"
                />
              </svg>
              <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
            </button>
          </div>
        </header>

        <div
          class="relative flex flex-wrap items-center justify-between gap-x-6 gap-y-3 border-t border-gray-100 bg-gray-50/60 px-5 py-3 dark:border-dark-700 dark:bg-dark-900/20 sm:px-6"
        >
          <dl v-if="snapshot" class="flex flex-wrap items-center gap-2 text-xs">
            <div
              v-for="item in configItems"
              :key="item.label"
              class="inline-flex items-center gap-1.5 rounded-lg bg-white px-2.5 py-1 ring-1 ring-gray-900/5 dark:bg-dark-800 dark:ring-dark-700"
            >
              <dt class="text-gray-400 dark:text-dark-500">{{ item.label }}</dt>
              <dd class="font-semibold text-gray-800 dark:text-gray-100" :class="item.mono ? 'font-mono' : ''">{{ item.value }}</dd>
            </div>
          </dl>
          <div v-else class="h-7 w-72 animate-pulse rounded-lg bg-gray-100 dark:bg-dark-700" aria-hidden="true"></div>
          <ul class="flex flex-wrap items-center gap-x-3.5 gap-y-1 text-xs text-gray-500 dark:text-dark-400" :aria-label="t('gptIq.title')">
            <li v-for="status in IQ_STATUSES" :key="status" class="inline-flex items-center gap-1.5">
              <span class="h-2.5 w-2.5 rounded-[3px]" :class="IQ_CELL_CLASS[status].replace('animate-pulse', '')" aria-hidden="true"></span>
              {{ status === 'skipped' ? t('gptIq.legend.skipped') : t(`gptIq.status.${status}`) }}
            </li>
          </ul>
        </div>
      </section>

      <!-- How to read -->
      <aside
        class="flex gap-3 rounded-2xl bg-primary-50/60 px-4 py-3.5 text-sm leading-relaxed text-gray-600 ring-1 ring-primary-500/10 dark:bg-primary-500/5 dark:text-dark-300 dark:ring-primary-400/10 sm:px-5"
      >
        <Icon name="infoCircle" size="md" class="mt-0.5 shrink-0 text-primary-500" />
        <p>{{ t('gptIq.hint') }}</p>
      </aside>

      <!-- Error / idle banners -->
      <div
        v-if="errorMessage"
        class="flex flex-wrap items-center justify-between gap-3 rounded-2xl bg-amber-50 px-4 py-3 text-sm text-amber-800 ring-1 ring-amber-500/20 dark:bg-amber-500/10 dark:text-amber-200 dark:ring-amber-400/20 sm:px-5"
        role="alert"
      >
        <span class="flex items-center gap-2">
          <Icon name="exclamationTriangle" size="sm" class="shrink-0" />
          {{ errorMessage }}
        </span>
        <button type="button" class="btn btn-secondary btn-sm" :disabled="loading" @click="reload">{{ t('gptIq.retry') }}</button>
      </div>
      <p
        v-else-if="idle"
        class="rounded-2xl bg-gray-50 px-4 py-3 text-sm text-gray-600 ring-1 ring-gray-900/5 dark:bg-dark-800 dark:text-dark-300 dark:ring-dark-700 sm:px-5"
      >
        {{ t('gptIq.idle') }}
      </p>

      <!-- Groups -->
      <section v-if="snapshot?.groups.length" class="grid gap-4 lg:grid-cols-2" :aria-label="t('gptIq.title')">
        <IqGroupCard
          v-for="group in snapshot.groups"
          :key="group.id"
          :group="group"
          :expected-answer="expectedAnswer"
          @select="(point) => openPoint(group, point)"
        />
      </section>
      <section v-else-if="!snapshot && loading" class="grid gap-4 lg:grid-cols-2" aria-hidden="true">
        <div v-for="i in 2" :key="i" class="h-56 animate-pulse rounded-3xl bg-gray-100 dark:bg-dark-800"></div>
      </section>

      <IqDrawingGallery
        v-if="snapshot?.drawings"
        :drawings="snapshot.drawings"
        :draw-log="drawLog"
        :is-admin="isAdmin"
        :can-manage="canManage"
        :deleting-key="deletingKey"
        @delete="askDelete"
      />

      <!-- Reasoning question -->
      <section
        v-if="snapshot"
        class="card overflow-hidden !rounded-3xl !border-0 shadow-sm ring-1 ring-gray-900/5 dark:!bg-dark-800 dark:ring-dark-700"
        aria-labelledby="iq-question-title"
      >
        <div class="grid gap-0 lg:grid-cols-[1fr_260px]">
          <div class="p-5 sm:p-6">
            <p class="text-[11px] font-semibold uppercase tracking-wider text-primary-600 dark:text-primary-400">
              {{ t('gptIq.question.kicker') }}
            </p>
            <h2 id="iq-question-title" class="mt-1 text-lg font-bold text-gray-900 dark:text-white">{{ t('gptIq.question.title') }}</h2>
            <p class="mt-1 text-xs text-gray-500 dark:text-dark-400">{{ t('gptIq.question.lede', { interval: intervalMinutes }) }}</p>
            <div class="relative mt-4">
              <p
                lang="en"
                class="whitespace-pre-line text-sm leading-7 text-gray-700 dark:text-gray-300"
                :class="questionOpen ? '' : 'line-clamp-4'"
              >
                {{ snapshot.question }}
              </p>
              <button
                type="button"
                class="mt-2 inline-flex items-center gap-1 text-xs font-semibold text-primary-600 hover:text-primary-700 dark:text-primary-400 dark:hover:text-primary-300"
                @click="questionOpen = !questionOpen"
              >
                {{ questionOpen ? t('gptIq.question.collapse') : t('gptIq.question.expand') }}
                <Icon name="chevronDown" size="xs" :class="questionOpen ? 'rotate-180' : ''" />
              </button>
            </div>
          </div>
          <div
            class="flex flex-col justify-center gap-3 border-t border-gray-100 bg-gradient-to-br from-emerald-50 to-white p-5 dark:border-dark-700 dark:from-emerald-500/10 dark:to-dark-800 sm:p-6 lg:border-l lg:border-t-0"
          >
            <p class="text-[11px] font-semibold uppercase tracking-wider text-emerald-700 dark:text-emerald-300">
              {{ t('gptIq.question.answerLabel') }}
            </p>
            <p class="text-4xl font-black tabular-nums tracking-tight text-emerald-600 dark:text-emerald-400">
              {{ expectedAnswer }}
              <span class="text-base font-semibold text-emerald-700/70 dark:text-emerald-300/70">{{ t('gptIq.question.answerUnit') }}</span>
            </p>
            <p class="text-xs leading-relaxed text-gray-500 dark:text-dark-400">{{ t('gptIq.question.rule', { answer: expectedAnswer }) }}</p>
          </div>
        </div>
      </section>

      <IqAdminLogs v-if="isAdmin" :probe-log="probeLog" :draw-log="drawLog" :expected-answer="expectedAnswer" />
    </div>

    <!-- Minute detail -->
    <BaseDialog
      :show="!!selected"
      :title="selected?.group.name || ''"
      width="wide"
      :close-on-click-outside="true"
      @close="selected = null"
    >
      <div v-if="selected" class="space-y-4">
        <div class="flex flex-wrap items-center gap-2 text-xs text-gray-500 dark:text-dark-400">
          <span
            class="inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 font-semibold ring-1 ring-inset"
            :class="IQ_PILL_CLASS[selected.point.status]"
          >
            <span class="h-1.5 w-1.5 rounded-full" :class="IQ_DOT_CLASS[selected.point.status]"></span>
            {{ t(`gptIq.status.${selected.point.status}`) }}
          </span>
          <time class="tabular-nums">{{ formatStamp(selected.point.minute, locale) }}</time>
          <span v-if="snapshot">· {{ snapshot.config.model }} · {{ reasoningLabel(snapshot.config.reasoning_effort) }}</span>
          <span v-if="selected.point.reason">· {{ reasonLabel(t, selected.point.reason, expectedAnswer) }}</span>
        </div>
        <div class="grid grid-cols-2 gap-3">
          <div class="rounded-2xl bg-gray-50 p-4 dark:bg-dark-900/40">
            <p class="text-[11px] font-medium text-gray-400 dark:text-dark-500">{{ t('gptIq.detail.extracted') }}</p>
            <p
              class="mt-1 text-2xl font-black tabular-nums"
              :class="selected.point.status === 'normal' ? 'text-emerald-600 dark:text-emerald-400' : selected.point.status === 'degraded' ? 'text-red-600 dark:text-red-400' : 'text-gray-500'"
            >
              <template v-if="selected.point.answer != null">{{ selected.point.answer }}</template>
              <span v-else class="text-sm font-semibold">{{ t('gptIq.detail.noAnswer') }}</span>
            </p>
          </div>
          <div class="rounded-2xl bg-gray-50 p-4 dark:bg-dark-900/40">
            <p class="text-[11px] font-medium text-gray-400 dark:text-dark-500">{{ t('gptIq.detail.expected') }}</p>
            <p class="mt-1 text-2xl font-black tabular-nums text-gray-800 dark:text-gray-100">{{ expectedAnswer }}</p>
          </div>
        </div>
        <p v-if="selected.point.account" class="text-xs text-gray-500 dark:text-dark-400">
          {{ t('gptIq.detail.account') }} <strong class="text-gray-800 dark:text-gray-100">{{ selected.point.account }}</strong>
        </p>
        <div>
          <p class="mb-1.5 text-[11px] font-semibold uppercase tracking-wider text-gray-400 dark:text-dark-500">{{ t('gptIq.detail.reply') }}</p>
          <pre
            class="max-h-80 overflow-auto whitespace-pre-wrap break-words rounded-2xl bg-gray-900 p-4 font-mono text-xs leading-relaxed text-gray-100 dark:bg-black/40"
          >{{ replyText(t, selected.point) }}</pre>
        </div>
        <p class="text-xs leading-relaxed text-gray-400 dark:text-dark-500">{{ t('gptIq.question.rule', { answer: expectedAnswer }) }}</p>
      </div>
      <template #footer>
        <div class="flex justify-end">
          <button type="button" class="btn btn-secondary" @click="selected = null">{{ t('gptIq.detail.close') }}</button>
        </div>
      </template>
    </BaseDialog>

    <ConfirmDialog
      :show="!!pendingDelete"
      :title="t('gptIq.drawings.confirmTitle')"
      :message="t('gptIq.drawings.confirmMessage')"
      :confirm-text="t('gptIq.drawings.delete')"
      danger
      @confirm="confirmDelete"
      @cancel="pendingDelete = null"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import {
  IqMonitorError,
  gptIqMonitorAPI,
  type IqDrawingItem,
  type IqGroupSnapshot,
  type IqMinutePoint,
  type IqMonitorSnapshot,
  type IqProbeLogItem
} from '@/api/gptIqMonitor'
import IqGroupCard from '@/features/gpt-iq-monitor/IqGroupCard.vue'
import IqDrawingGallery from '@/features/gpt-iq-monitor/IqDrawingGallery.vue'
import IqAdminLogs from '@/features/gpt-iq-monitor/IqAdminLogs.vue'
import {
  IQ_CELL_CLASS,
  IQ_DOT_CLASS,
  IQ_PILL_CLASS,
  IQ_STATUSES,
  formatClock,
  formatStamp,
  isIdleWindow,
  nextRefreshDelayMs,
  reasonLabel,
  reasoningLabel,
  refreshWindowMs,
  replyText
} from '@/features/gpt-iq-monitor/iqFormat'

const RING = 2 * Math.PI * 20

const { t, locale } = useI18n()

const snapshot = ref<IqMonitorSnapshot | null>(null)
const loading = ref(false)
const errorMessage = ref('')
const countdown = ref(0)
const windowMs = ref(refreshWindowMs())
const selected = ref<{ group: IqGroupSnapshot; point: IqMinutePoint } | null>(null)
const questionOpen = ref(false)
const pendingDelete = ref<IqDrawingItem | null>(null)
const deletingKey = ref<string | null>(null)

let deadline = Date.now()
let timer: number | undefined
let abort: AbortController | null = null
let disposed = false

const expectedAnswer = computed(() => snapshot.value?.expected_answer ?? 46.24)
const intervalMinutes = computed(() => snapshot.value?.config.probe_interval_minutes ?? 2)
const isAdmin = computed(() => Boolean(snapshot.value?.is_admin || snapshot.value?.drawings?.can_manage))
const canManage = computed(() => Boolean(snapshot.value?.drawings?.can_manage))
const probeLog = computed<IqProbeLogItem[]>(() => (isAdmin.value ? snapshot.value?.probe_log ?? [] : []))
const drawLog = computed<IqDrawingItem[]>(() => (isAdmin.value ? snapshot.value?.draw_log ?? [] : []))
const idle = computed(() => {
  const groups = snapshot.value?.groups ?? []
  return groups.length > 0 && groups.every((group) => isIdleWindow(group.minutes))
})
const ringProgress = computed(() => (loading.value ? 1 : Math.min(1, Math.max(0, 1 - (countdown.value * 1000) / windowMs.value))))

const configItems = computed(() => {
  const s = snapshot.value
  if (!s) return []
  return [
    { label: t('gptIq.config.model'), value: s.config.model, mono: true },
    { label: t('gptIq.config.reasoning'), value: reasoningLabel(s.config.reasoning_effort), mono: false },
    { label: t('gptIq.config.interval'), value: t('gptIq.config.intervalValue', { n: s.config.probe_interval_minutes }), mono: false },
    { label: t('gptIq.config.answer'), value: String(s.expected_answer), mono: true }
  ]
})

function scheduleNext(delayMs: number) {
  windowMs.value = Math.max(delayMs, 1000)
  deadline = Date.now() + delayMs
  countdown.value = Math.ceil(delayMs / 1000)
}

function errorText(error: unknown): string {
  const code = error instanceof IqMonitorError ? error.code : 'unavailable'
  if (code === 'login_required') return t('gptIq.errors.authFailed')
  return snapshot.value ? t('gptIq.errors.stale') : t('gptIq.errors.unavailable')
}

async function reload() {
  if (loading.value || disposed) return
  loading.value = true
  abort?.abort()
  abort = new AbortController()
  try {
    const next = await gptIqMonitorAPI.getSnapshot(abort.signal)
    snapshot.value = next
    errorMessage.value = ''
    scheduleNext(nextRefreshDelayMs(next))
  } catch (error) {
    if ((error as { name?: string }).name === 'AbortError') return
    errorMessage.value = errorText(error)
    scheduleNext(refreshWindowMs(snapshot.value?.config.probe_interval_minutes))
  } finally {
    loading.value = false
  }
}

function openPoint(group: IqGroupSnapshot, point: IqMinutePoint) {
  selected.value = { group, point }
}

function askDelete(item: IqDrawingItem) {
  if (!canManage.value || deletingKey.value) return
  pendingDelete.value = item
}

async function confirmDelete() {
  const item = pendingDelete.value
  pendingDelete.value = null
  if (!item) return
  deletingKey.value = `${item.group_id}:${item.observed_at}`
  try {
    const drawings = await gptIqMonitorAPI.deleteDrawing(item.observed_at, item.group_id)
    if (snapshot.value) snapshot.value = { ...snapshot.value, drawings }
  } catch (error) {
    const code = error instanceof IqMonitorError ? error.code : 'unavailable'
    errorMessage.value =
      code === 'login_required'
        ? t('gptIq.errors.authFailed')
        : code === 'admin_required'
          ? t('gptIq.errors.adminOnly')
          : t('gptIq.errors.deleteFailed')
  } finally {
    deletingKey.value = null
  }
}

function tick() {
  countdown.value = Math.max(0, Math.ceil((deadline - Date.now()) / 1000))
  if (!countdown.value && !loading.value && document.visibilityState === 'visible') void reload()
}

onMounted(() => {
  void reload()
  timer = window.setInterval(tick, 1000)
  document.addEventListener('visibilitychange', tick)
})

onBeforeUnmount(() => {
  disposed = true
  window.clearInterval(timer)
  document.removeEventListener('visibilitychange', tick)
  abort?.abort()
})
</script>
