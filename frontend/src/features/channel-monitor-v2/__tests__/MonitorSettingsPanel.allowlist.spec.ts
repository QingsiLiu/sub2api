import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import MonitorSettingsPanel from '../MonitorSettingsPanel.vue'
import zh from '@/i18n/locales/zh/channelMonitorV2'
import en from '@/i18n/locales/en/channelMonitorV2'
import type { MonitorConfig } from '@/api/channelMonitorV2'

const { getConfig, updateConfig, getGroups, showSuccess, showError } = vi.hoisted(() => ({
  getConfig: vi.fn(), updateConfig: vi.fn(), getGroups: vi.fn(), showSuccess: vi.fn(), showError: vi.fn(),
}))
vi.mock('@/api/channelMonitorV2', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/api/channelMonitorV2')>(), getConfig, updateConfig,
}))
vi.mock('@/api/admin', () => ({ adminAPI: { groups: { getAllIncludingInactive: getGroups } } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({
  cachedPublicSettings: { channel_monitor_enabled: true }, showSuccess, showError,
}) }))
vi.mock('@/utils/featureFlags', () => ({ getChannelMonitorMode: () => 'v2', isChannelMonitorV2Mode: () => true }))

let testLocale = 'zh'
vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => {
    const messages = testLocale === 'zh' ? zh : en
    const lookup = (key: string): unknown => key.split('.').reduce<unknown>(
      (value, part) => value && typeof value === 'object' ? (value as Record<string, unknown>)[part] : undefined,
      messages,
    )
    return {
      t: (key: string, args: Record<string, unknown> = {}) => String(lookup(key) ?? key)
        .replace(/\{(\w+)\}/g, (_, name: string) => String(args[name] ?? `{${name}}`)),
      te: (key: string) => typeof lookup(key) === 'string',
    }
  },
}))

function config() {
  return {
    version: 1, enabled: true, refresh_interval_seconds: 60,
    platforms: [
      { platform: 'openai', enabled: true, models: ['gpt-5'] },
      { platform: 'anthropic', enabled: true, models: [] },
    ],
    group_ids: [], ignored_error_categories: [],
  }
}
function mountSettings(locale = 'zh') {
  testLocale = locale
  return mount(MonitorSettingsPanel, {
    global: {
      stubs: { Toggle: true, Icon: true, RouterLink: true },
    },
  })
}

beforeEach(() => {
  vi.clearAllMocks()
  getConfig.mockResolvedValue(config())
  getGroups.mockResolvedValue([])
  // Match the real HTTP roundtrip: response data must not contain Vue proxies.
  updateConfig.mockImplementation(async (value: MonitorConfig) => JSON.parse(JSON.stringify({ ...value, version: value.version + 1 })))
})

describe('strict model allowlist settings', () => {
  it.each(['zh', 'en'])('explains strict and empty allowlists without adding a switch (%s)', async (locale) => {
    const wrapper = mountSettings(locale)
    await flushPromises()
    const text = (locale === 'zh' ? zh : en).channelMonitorV2.settings
    expect(wrapper.text()).toContain(text.platformsHint)
    expect(wrapper.text()).toContain(text.badgeListedModels)
    expect(wrapper.text()).toContain(text.badgeNoModels)
    expect(wrapper.text()).not.toContain(locale === 'zh' ? '+ 其他' : '+ Other')
    expect(wrapper.findAll('toggle-stub')).toHaveLength(3) // main enable + two platforms only
    wrapper.unmount()
  })

  it('saves exact trimmed/deduplicated model names and reloads the same allowlist', async () => {
    const wrapper = mountSettings()
    await flushPromises()
    await wrapper.findAll('input[type="text"]')[0].setValue(' gpt-5.1, gpt-5, gpt-5.1, ')
    await wrapper.get('button.btn-primary').trigger('click')
    await flushPromises()
    expect(updateConfig).toHaveBeenCalledOnce()
    const saved = updateConfig.mock.calls[0][0] as MonitorConfig
    expect(saved.platforms[0].models).toEqual(['gpt-5', 'gpt-5.1'])
    expect(saved.platforms[1].models).toEqual([])
    expect(showSuccess).toHaveBeenCalledOnce()
    expect(showError).not.toHaveBeenCalled()
    expect(wrapper.get('button.btn-primary').attributes('disabled')).toBeDefined()
    wrapper.unmount()
    getConfig.mockResolvedValue(JSON.parse(JSON.stringify({ ...saved, version: 2 })))
    const reloaded = mountSettings()
    await flushPromises()
    expect((reloaded.findAll('input[type="text"]')[0].element as HTMLInputElement).value).toBe('gpt-5, gpt-5.1')
    reloaded.unmount()
  })

  it('saves an empty allowlist without auto-filling models and explains the no-data state', async () => {
    const wrapper = mountSettings()
    await flushPromises()
    await wrapper.findAll('input[type="text"]')[0].setValue(' , , ')
    expect(wrapper.text()).toContain(zh.channelMonitorV2.settings.namedModelsEmpty)
    await wrapper.get('button.btn-primary').trigger('click')
    await flushPromises()
    expect(updateConfig.mock.calls[0][0].platforms.every((p: { models: string[] }) => p.models.length === 0)).toBe(true)
    expect(wrapper.text()).not.toContain('仅指定模型')
    wrapper.unmount()
  })
})
