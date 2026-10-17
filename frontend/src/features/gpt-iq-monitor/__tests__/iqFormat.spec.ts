import { describe, expect, it } from 'vitest'
import type { IqMinutePoint, IqMinuteStatus, IqMonitorSnapshot } from '@/api/gptIqMonitor'
import { latestStreak, nextRefreshDelayMs, normalRate, reasonLabel } from '../iqFormat'

const points = (...statuses: IqMinuteStatus[]): IqMinutePoint[] =>
  statuses.map((status, index) => ({ minute: `2026-10-17T16:${String(index).padStart(2, '0')}:00+08:00`, status }))

const t = (key: string, params?: Record<string, unknown>) => `${key}${params ? JSON.stringify(params) : ''}`

describe('gpt iq format', () => {
  it('rates only judged probes, ignoring errors and empty slots', () => {
    expect(normalRate(points('normal', 'degraded', 'error', 'skipped', 'normal', 'normal'))).toBe(0.75)
    expect(normalRate(points('error', 'skipped'))).toBeNull()
  })

  it('reports the latest judged streak past trailing pending cells', () => {
    expect(latestStreak(points('degraded', 'normal', 'normal', 'pending'))).toEqual({ status: 'normal', count: 2 })
    expect(latestStreak(points('skipped', 'pending'))).toBeNull()
  })

  it('maps upstream http reasons and passes unknown ones through', () => {
    expect(reasonLabel(t, 'upstream_http_524')).toBe('gptIq.reasons.http{"code":"524"}')
    expect(reasonLabel(t, 'wrong_answer', 46.24)).toBe('gptIq.reasons.wrong_answer{"answer":46.24}')
    expect(reasonLabel(t, 'brand_new_reason')).toBe('brand_new_reason')
  })

  it('clamps the refresh delay to one probe interval', () => {
    const base = { config: { model: 'm', reasoning_effort: 'low', probe_interval_minutes: 2 } } as IqMonitorSnapshot
    expect(nextRefreshDelayMs({ ...base, server_time: '2026-10-17T16:49:16+08:00', next_refresh_at: '2026-10-17T16:50:00+08:00' })).toBe(44_000)
    expect(nextRefreshDelayMs({ ...base, server_time: '2026-10-17T16:49:16+08:00', next_refresh_at: '2026-10-17T18:00:00+08:00' })).toBe(120_000)
    expect(nextRefreshDelayMs({ ...base, server_time: 'bad', next_refresh_at: 'bad' })).toBe(120_000)
  })
})
