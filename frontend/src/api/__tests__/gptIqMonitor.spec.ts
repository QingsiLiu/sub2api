import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { IqMonitorError, getSnapshot } from '../gptIqMonitor'

const fetchMock = vi.fn()

beforeEach(() => {
  localStorage.clear()
  fetchMock.mockReset()
  vi.stubGlobal('fetch', fetchMock)
})
afterEach(() => {
  vi.unstubAllGlobals()
  localStorage.clear()
})

describe('gpt iq monitor api', () => {
  it('sends the console token to the same-origin sidecar', async () => {
    localStorage.setItem('auth_token', 'tok')
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ groups: [] }), { status: 200 }))
    await getSnapshot()
    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe('/gpt-iq-monitor/api/snapshot')
    expect(init.headers.Authorization).toBe('Bearer tok')
  })

  it('reports a sidecar 401 without ending the console session', async () => {
    localStorage.setItem('auth_token', 'tok')
    localStorage.setItem('refresh_token', 'refresh')
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ error: 'login_required' }), { status: 401 }))
    await expect(getSnapshot()).rejects.toMatchObject({ code: 'login_required' })
    expect(localStorage.getItem('auth_token')).toBe('tok')
    expect(localStorage.getItem('refresh_token')).toBe('refresh')
  })

  it('maps network failures and HTML error pages to unavailable', async () => {
    fetchMock.mockRejectedValueOnce(new TypeError('Failed to fetch'))
    await expect(getSnapshot()).rejects.toBeInstanceOf(IqMonitorError)
    fetchMock.mockResolvedValueOnce(new Response('<html>502</html>', { status: 200 }))
    await expect(getSnapshot()).rejects.toMatchObject({ code: 'unavailable' })
  })
})
