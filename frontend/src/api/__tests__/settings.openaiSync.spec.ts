import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { get, put } = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: { get, put } }))

beforeEach(() => { vi.resetModules(); get.mockReset(); put.mockReset() })
afterEach(() => { vi.useRealTimers() })
const settingsWith = (ids: string[]) => ({ data: { openai_sync_model_ids: ids } })

describe('OpenAI sync catalog cache', () => {
  it('deduplicates in-flight reads and returns copies of cached IDs', async () => {
    get.mockResolvedValue(settingsWith(['gpt-reserve', 'gpt-6-astra']))
    const api = await import('@/api/admin/settings')
    const [first, second] = await Promise.all([api.getOpenAISyncModelIDs(), api.getOpenAISyncModelIDs()])
    first.pop()
    expect(second).toEqual(['gpt-reserve', 'gpt-6-astra'])
    expect(await api.getOpenAISyncModelIDs()).toEqual(second)
    expect(get).toHaveBeenCalledTimes(1)
    expect(get).toHaveBeenCalledWith('/admin/settings')
  })

  it('refreshes the cache after saving without requesting settings again', async () => {
    get.mockResolvedValue(settingsWith(['gpt-5.5']))
    put.mockResolvedValue(settingsWith(['gpt-reserve']))
    const api = await import('@/api/admin/settings')
    await api.getOpenAISyncModelIDs()
    await api.updateSettings({ openai_sync_model_ids: ['gpt-reserve'] })
    expect(await api.getOpenAISyncModelIDs()).toEqual(['gpt-reserve'])
    expect(get).toHaveBeenCalledTimes(1)
  })

  it('does not let an old GET overwrite a successful settings save', async () => {
    let resolveRead!: (data: ReturnType<typeof settingsWith>) => void
    get.mockImplementation(() => new Promise(resolve => { resolveRead = resolve }))
    put.mockResolvedValue(settingsWith(['gpt-reserve']))
    const api = await import('@/api/admin/settings')
    const pending = api.getOpenAISyncModelIDs()
    await api.updateSettings({ openai_sync_model_ids: ['gpt-reserve'] })
    resolveRead(settingsWith(['gpt-5.5']))
    expect(await pending).toEqual(['gpt-reserve'])
    expect(await api.getOpenAISyncModelIDs()).toEqual(['gpt-reserve'])
  })

  it('retries failed reads without reverting to an unrelated default', async () => {
    get.mockRejectedValueOnce(new Error('network unavailable')).mockResolvedValue(settingsWith(['gpt-6-astra']))
    const api = await import('@/api/admin/settings')
    await expect(api.getOpenAISyncModelIDs()).rejects.toThrow('network unavailable')
    expect(await api.getOpenAISyncModelIDs()).toEqual(['gpt-6-astra'])
    expect(get).toHaveBeenCalledTimes(2)
  })

  it('revalidates after the cache expires', async () => {
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(0)
    get.mockResolvedValueOnce(settingsWith(['gpt-5.5'])).mockResolvedValueOnce(settingsWith(['gpt-reserve']))
    const api = await import('@/api/admin/settings')
    await api.getOpenAISyncModelIDs()
    vi.setSystemTime(30_001)
    expect(await api.getOpenAISyncModelIDs()).toEqual(['gpt-reserve'])
    expect(get).toHaveBeenCalledTimes(2)
  })

  it.each([undefined, [], ['']])('rejects unavailable catalog %j', async ids => {
    get.mockResolvedValue({ data: { openai_sync_model_ids: ids } })
    const api = await import('@/api/admin/settings')
    await expect(api.getOpenAISyncModelIDs()).rejects.toThrow('catalog is unavailable')
  })
})
