import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
const { post, get } = vi.hoisted(() => ({ post: vi.fn(), get: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: { post, get } }))
import { getSubmissionUsage, inspectSubmissionInvite, submitAccountKey, SubmissionRequestError, createSubmissionInvite } from '@/api/accountSubmission'

describe('external key submission requests', () => {
  beforeEach(() => vi.clearAllMocks())
  afterEach(() => vi.unstubAllGlobals())
  it('keeps secrets in the body and bypasses authenticated interceptors', async () => {
    const fetch = vi.fn().mockResolvedValue({ ok: true, json: async () => ({ code: 0, data: { status: 'submitted' } }) })
    vi.stubGlobal('fetch', fetch)
    await submitAccountKey('synthetic-invite-token', 'synthetic-private-key')
    const [url, options] = fetch.mock.calls[0]
    expect(url).toBe('/api/v1/account-submissions/submit')
    expect(options).toMatchObject({ credentials: 'omit', cache: 'no-store', referrerPolicy: 'no-referrer', redirect: 'error' })
    expect(JSON.parse(options.body)).toEqual({ token: 'synthetic-invite-token', api_key: 'synthetic-private-key' })
    expect(post).not.toHaveBeenCalled()
  })
  it('never exposes an original network error or its request credentials', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('synthetic-private-key')))
    const error = await submitAccountKey('synthetic-invite-token', 'synthetic-private-key').catch(error => error)
    expect(error).toBeInstanceOf(SubmissionRequestError)
    expect(error.status).toBe(0)
    expect(String(error)).not.toContain('synthetic-private-key')
    expect(error).not.toHaveProperty('config')
  })
  it('does not read or expose server failure bodies', async () => {
    const json = vi.fn()
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: false, status: 410, json }))
    await expect(inspectSubmissionInvite('synthetic-invite-token')).rejects.toMatchObject({ status: 410 })
    expect(json).not.toHaveBeenCalled()
  })
  it('preserves a zero account multiplier in administrator configuration', async () => {
    post.mockResolvedValue({ data: { invite: { id: 1 }, token: 'synthetic-token' } })
    await createSubmissionInvite({ name: 'test', platform: 'openai', group_ids: [], proxy_id: null, priority: 50, concurrency: 1, rate_multiplier: 0 })
    expect(post.mock.calls[0][1].rate_multiplier).toBe(0)
  })
  it('sends usage capabilities and dates in the body with no cookies or credentials in errors', async () => {
    const fetch = vi.fn().mockResolvedValue({ ok: true, json: async () => ({ code: 0, data: { summary: {} } }) })
    vi.stubGlobal('fetch', fetch)
    await getSubmissionUsage('private-view-token', '2026-10-01', '2026-10-11')
    const [url, options] = fetch.mock.calls[0]
    expect(url).toBe('/api/v1/account-submissions/usage')
    expect(url).not.toContain('private-view-token')
    expect(options.credentials).toBe('omit')
    expect(JSON.parse(options.body)).toEqual({ token: 'private-view-token', start_date: '2026-10-01', end_date: '2026-10-11' })
  })

})
