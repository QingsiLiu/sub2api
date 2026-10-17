/**
 * geili: GPT group IQ monitor.
 * Data comes from the host sidecar behind /gpt-iq-monitor/api/ (same origin, not /api/v1).
 * Plain fetch on purpose: a sidecar 401 must not trigger apiClient's global logout.
 */

export type IqMinuteStatus = 'normal' | 'degraded' | 'skipped' | 'error' | 'pending'
export type IqDrawingStatus = 'ok' | 'error' | 'skipped'

export interface IqMinutePoint {
  minute: string
  status: IqMinuteStatus
  answer?: number | null
  output?: string | null
  reason?: string | null
  account?: string | null
}

export interface IqGroupSnapshot {
  id: number
  name: string
  multiplier: string
  status: IqMinuteStatus
  minutes: IqMinutePoint[]
}

export interface IqDrawAttempt {
  status?: string
  reason?: string
  elapsed_ms?: number
  stream?: boolean
}

export interface IqDrawingItem {
  group_id: number
  group_name: string
  observed_at: string
  status: IqDrawingStatus
  elapsed_ms?: number | null
  reason?: string | null
  svg?: string | null
  account?: string | null
  attempts?: IqDrawAttempt[]
  attempt_count?: number
  budget_exhausted?: boolean
}

export interface IqDrawingGroup {
  id: number
  name: string
  multiplier: string
  items: IqDrawingItem[]
}

export interface IqDrawingsSnapshot {
  groups: IqDrawingGroup[]
  title: string
  question: string
  interval_minutes: number
  keep: number
  updated_at: string | null
  items: IqDrawingItem[]
  can_manage?: boolean
}

export interface IqProbeLogItem {
  minute: string
  group_id: number
  group_name: string
  status: IqMinuteStatus
  answer?: number | null
  reason?: string | null
  account?: string | null
}

export interface IqMonitorSnapshot {
  question: string
  expected_answer: number
  config: {
    model: string
    reasoning_effort: string
    probe_interval_minutes: number
  }
  server_time: string
  next_refresh_at: string
  updated_at: string | null
  groups: IqGroupSnapshot[]
  drawings?: IqDrawingsSnapshot
  is_admin?: boolean
  probe_log?: IqProbeLogItem[]
  draw_log?: IqDrawingItem[]
}

export type IqMonitorErrorCode = 'login_required' | 'admin_required' | 'not_found' | 'unavailable'

export class IqMonitorError extends Error {
  constructor(public readonly code: IqMonitorErrorCode) {
    super(code)
    this.name = 'IqMonitorError'
  }
}

const BASE = '/gpt-iq-monitor/api'

function authHeaders(): Record<string, string> {
  const token = localStorage.getItem('auth_token')
  return token ? { Authorization: `Bearer ${token}` } : {}
}

function errorFor(status: number): IqMonitorError {
  if (status === 401) return new IqMonitorError('login_required')
  if (status === 403) return new IqMonitorError('admin_required')
  if (status === 404) return new IqMonitorError('not_found')
  return new IqMonitorError('unavailable')
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  let response: Response
  try {
    response = await fetch(BASE + path, {
      ...init,
      cache: 'no-store',
      headers: { Accept: 'application/json', ...authHeaders(), ...(init.headers || {}) }
    })
  } catch (error) {
    if ((error as { name?: string }).name === 'AbortError') throw error
    throw new IqMonitorError('unavailable')
  }
  if (!response.ok) throw errorFor(response.status)
  try {
    return (await response.json()) as T
  } catch {
    throw new IqMonitorError('unavailable')
  }
}

export function getSnapshot(signal?: AbortSignal): Promise<IqMonitorSnapshot> {
  return request<IqMonitorSnapshot>('/snapshot', { signal })
}

export async function deleteDrawing(observedAt: string, groupId: number): Promise<IqDrawingsSnapshot> {
  const payload = await request<{ drawings?: IqDrawingsSnapshot }>('/drawings', {
    method: 'DELETE',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ observed_at: observedAt, group_id: groupId })
  })
  if (!payload.drawings) throw new IqMonitorError('unavailable')
  return payload.drawings
}

export const gptIqMonitorAPI = { getSnapshot, deleteDrawing }

export default gptIqMonitorAPI
