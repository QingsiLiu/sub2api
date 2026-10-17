/**
 * geili: pure display helpers for the GPT group IQ monitor page.
 * Labels go through the caller's `t` so zh/en stay in locale files.
 */

import type { IqMinutePoint, IqMinuteStatus, IqMonitorSnapshot } from '@/api/gptIqMonitor'

type Translate = (key: string, params?: Record<string, unknown>) => string

export const IQ_STATUSES: IqMinuteStatus[] = ['normal', 'degraded', 'pending', 'error', 'skipped']

/** Minute-cell fill per status. */
export const IQ_CELL_CLASS: Record<IqMinuteStatus, string> = {
  normal: 'bg-emerald-500 dark:bg-emerald-500/90',
  degraded: 'bg-red-500 dark:bg-red-500/90',
  pending: 'bg-primary-400 animate-pulse dark:bg-primary-500/80',
  error: 'bg-blue-500 dark:bg-blue-500/90',
  skipped: 'bg-gray-200 dark:bg-dark-600'
}

/** Status pill (badge) per status. */
export const IQ_PILL_CLASS: Record<IqMinuteStatus, string> = {
  normal: 'bg-emerald-50 text-emerald-700 ring-emerald-600/20 dark:bg-emerald-500/10 dark:text-emerald-300 dark:ring-emerald-400/30',
  degraded: 'bg-red-50 text-red-700 ring-red-600/20 dark:bg-red-500/10 dark:text-red-300 dark:ring-red-400/30',
  pending: 'bg-primary-50 text-primary-700 ring-primary-600/20 dark:bg-primary-500/10 dark:text-primary-300 dark:ring-primary-400/30',
  error: 'bg-blue-50 text-blue-700 ring-blue-600/20 dark:bg-blue-500/10 dark:text-blue-300 dark:ring-blue-400/30',
  skipped: 'bg-gray-50 text-gray-600 ring-gray-500/20 dark:bg-dark-700 dark:text-dark-300 dark:ring-dark-500/40'
}

export const IQ_DOT_CLASS: Record<IqMinuteStatus, string> = {
  normal: 'bg-emerald-500',
  degraded: 'bg-red-500',
  pending: 'bg-primary-400',
  error: 'bg-blue-500',
  skipped: 'bg-gray-300 dark:bg-dark-500'
}

export function drawingTone(status: string): IqMinuteStatus {
  if (status === 'ok') return 'normal'
  if (status === 'error') return 'error'
  return 'skipped'
}

export function countStatus(minutes: IqMinutePoint[], status: IqMinuteStatus): number {
  return minutes.filter((item) => item.status === status).length
}

/** Share of judged probes (normal + degraded) that answered correctly; null when none were judged. */
export function normalRate(minutes: IqMinutePoint[]): number | null {
  const normal = countStatus(minutes, 'normal')
  const judged = normal + countStatus(minutes, 'degraded')
  return judged ? normal / judged : null
}

/** Latest run of the same judged status, ignoring trailing pending/skipped cells. */
export function latestStreak(minutes: IqMinutePoint[]): { status: IqMinuteStatus; count: number } | null {
  let index = minutes.length - 1
  while (index >= 0 && (minutes[index].status === 'pending' || minutes[index].status === 'skipped')) index--
  if (index < 0) return null
  const status = minutes[index].status
  let count = 0
  for (; index >= 0 && minutes[index].status === status; index--) count++
  return { status, count }
}

export function isIdleWindow(minutes: IqMinutePoint[]): boolean {
  return minutes.length > 0 && minutes.every((item) => item.status === 'skipped')
}

const KNOWN_REASONS = new Set([
  'no_sample',
  'probe_busy',
  'missing_key',
  'ok',
  'wrong_answer',
  'empty_response',
  'incomplete',
  'no_svg',
  'svg_rejected',
  'draw_busy',
  'probe_timeout',
  'timeout',
  'non_stream_not_allowed',
  'empty_stream',
  'upstream_transport',
  'output_limit',
  'response_failed'
])

export function reasonLabel(t: Translate, reason?: string | null, expected = 46.24): string {
  if (!reason) return ''
  if (KNOWN_REASONS.has(reason)) return t(`gptIq.reasons.${reason}`, { answer: expected })
  if (reason.startsWith('upstream_http_') || reason.startsWith('http_')) {
    const code = reason.split('_').pop()
    if (code) return t('gptIq.reasons.http', { code })
  }
  return reason
}

export function replyText(t: Translate, point: IqMinutePoint): string {
  if (point.output) return point.output
  if (point.reason === 'no_sample' || point.reason === 'probe_busy' || point.reason === 'missing_key') {
    return t(`gptIq.reply.${point.reason}`)
  }
  return t('gptIq.reply.none')
}

export function reasoningLabel(value?: string): string {
  if (!value) return '-'
  if (value === 'xhigh') return 'XHigh'
  return value.charAt(0).toUpperCase() + value.slice(1)
}

export function formatClock(value: string | null | undefined, locale?: string): string {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleTimeString(locale, { hour: '2-digit', minute: '2-digit', hour12: false })
}

export function formatStamp(value: string | null | undefined, locale?: string): string {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString(locale, { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false })
}

export function formatDuration(t: Translate, milliseconds?: number | null): string {
  if (milliseconds == null || !Number.isFinite(milliseconds) || milliseconds < 0) return '—'
  if (milliseconds < 1000) return t('gptIq.duration.underSecond')
  const seconds = Math.round(milliseconds / 1000)
  if (seconds < 60) return t('gptIq.duration.seconds', { s: seconds })
  return t('gptIq.duration.minutes', { m: Math.floor(seconds / 60), s: seconds % 60 })
}

export function svgDataUrl(svg: string): string {
  return 'data:image/svg+xml;charset=utf-8,' + encodeURIComponent(svg)
}

export function refreshWindowMs(intervalMinutes?: number): number {
  const minutes = intervalMinutes && intervalMinutes > 0 ? intervalMinutes : 2
  return minutes * 60_000
}

/** Delay until the next probe result should exist, clamped to one probe interval. */
export function nextRefreshDelayMs(snapshot: IqMonitorSnapshot): number {
  const windowMs = refreshWindowMs(snapshot.config?.probe_interval_minutes)
  const next = Date.parse(snapshot.next_refresh_at) - Date.parse(snapshot.server_time)
  return Math.max(1000, Math.min(windowMs, Number.isFinite(next) ? next : windowMs))
}
