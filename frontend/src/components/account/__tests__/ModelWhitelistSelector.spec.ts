import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

const {
  copyToClipboard,
  showError,
  showSuccess,
  showInfo,
  showWarning,
  syncUpstreamModels,
  syncUpstreamModelsPreview,
  getOpenAISyncModelIDs
} = vi.hoisted(() => ({
  copyToClipboard: vi.fn().mockResolvedValue(true),
  showError: vi.fn(),
  showSuccess: vi.fn(),
  showInfo: vi.fn(),
  showWarning: vi.fn(),
  syncUpstreamModels: vi.fn(),
  syncUpstreamModelsPreview: vi.fn(),
  getOpenAISyncModelIDs: vi.fn()
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => (key === 'common.copy' ? '复制' : key)
    })
  }
})

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError,
    showSuccess,
    showInfo,
    showWarning
  })
}))

vi.mock('@/api/admin/settings', () => ({ getOpenAISyncModelIDs }))

vi.mock('@/api/admin/accounts', () => ({
  accountsAPI: {
    syncUpstreamModels,
    syncUpstreamModelsPreview,
  getOpenAISyncModelIDs
  }
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({
    copyToClipboard
  })
}))

import ModelWhitelistSelector from '../ModelWhitelistSelector.vue'
import { DEFAULT_OPENAI_SYNC_MODEL_IDS } from '@/constants/openaiSyncModels'

function mountSelector(props: Record<string, unknown> = {}) {
  return mount(ModelWhitelistSelector, {
    props: {
      modelValue: [],
      platform: 'openai',
      ...props,
    },
    global: {
      stubs: {
        ModelIcon: true
      }
    }
  })
}

function findModelRow(wrapper: ReturnType<typeof mountSelector>, modelId: string) {
  const row = wrapper
    .findAll('[data-testid="model-option"]')
    .find(candidate => candidate.text().includes(modelId))

  if (!row) {
    throw new Error(`Model row not found: ${modelId}`)
  }

  return row
}

describe('ModelWhitelistSelector', () => {
  beforeEach(() => {
    copyToClipboard.mockClear()
    showError.mockReset()
    showSuccess.mockReset()
    showInfo.mockReset()
    showWarning.mockReset()
    syncUpstreamModels.mockReset()
    syncUpstreamModelsPreview.mockReset()
    getOpenAISyncModelIDs.mockReset().mockResolvedValue(['codex-auto-review', 'gpt-5.5', 'gpt-reserve'])
  })

  it('copies a model ID without selecting the model', async () => {
    const wrapper = mountSelector()
    await wrapper.get('div.cursor-pointer').trigger('click')

    const row = findModelRow(wrapper, 'gpt-5.6-sol')

    const copyButton = row.get('[data-testid="copy-model-id"]')
    expect(copyButton.attributes('aria-label')).toBe('复制 gpt-5.6-sol')

    await copyButton.trigger('click')
    await flushPromises()

    expect(copyToClipboard).toHaveBeenCalledWith('gpt-5.6-sol')
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
  })

  it('keeps the existing model selection behavior', async () => {
    const wrapper = mountSelector()
    await wrapper.get('div.cursor-pointer').trigger('click')

    const row = findModelRow(wrapper, 'gpt-5.6-sol')
    await row.get('[data-testid="select-model"]').trigger('click')

    expect(wrapper.emitted('update:modelValue')).toEqual([[['gpt-5.6-sol']]])
    expect(copyToClipboard).not.toHaveBeenCalled()
  })


  it('uses the global OpenAI sync catalog additively', async () => {
    const wrapper = mountSelector({ modelValue: ['gpt-5.6-sol', 'custom-model'] })
    const button = wrapper.findAll('button').find(item => item.text() === 'admin.accounts.fillRelatedModels')
    expect(button).toBeDefined()
    await button!.trigger('click')
    await flushPromises()
    expect(getOpenAISyncModelIDs).toHaveBeenCalledOnce()
    expect(wrapper.emitted('update:modelValue')).toEqual([[['gpt-5.6-sol', 'custom-model', 'codex-auto-review', 'gpt-5.5', 'gpt-reserve']]])
  })

  it('fills exactly the seven defaults from empty and is idempotent', async () => {
    getOpenAISyncModelIDs.mockResolvedValue([...DEFAULT_OPENAI_SYNC_MODEL_IDS])
    const wrapper = mountSelector()
    const button = wrapper.findAll('button').find(item => item.text() === 'admin.accounts.fillRelatedModels')!
    await button.trigger('click')
    await flushPromises()
    const added = wrapper.emitted('update:modelValue')![0][0] as string[]
    expect(added).toEqual(DEFAULT_OPENAI_SYNC_MODEL_IDS)
    await wrapper.setProps({ modelValue: added })
    await button.trigger('click')
    await flushPromises()
    expect(wrapper.emitted('update:modelValue')!.at(-1)![0]).toEqual(added)
  })

  it('does not overwrite selection edits while the catalog is loading', async () => {
    let resolve!: (models: string[]) => void
    getOpenAISyncModelIDs.mockImplementation(() => new Promise(done => { resolve = done }))
    const wrapper = mountSelector({ modelValue: ['gpt-5.5'] })
    const button = wrapper.findAll('button').find(item => item.text() === 'admin.accounts.fillRelatedModels')!
    await button.trigger('click')
    await wrapper.setProps({ modelValue: ['custom-model'] })
    resolve(['gpt-6-astra'])
    await flushPromises()
    expect(wrapper.emitted('update:modelValue')!.at(-1)![0]).toEqual(['custom-model', 'gpt-6-astra'])
  })

  it('keeps the selection on failure and allows a retry', async () => {
    getOpenAISyncModelIDs.mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce(['gpt-reserve'])
    const wrapper = mountSelector({ modelValue: ['gpt-6-astra'] })
    const button = wrapper.findAll('button').find(item => item.text() === 'admin.accounts.fillRelatedModels')!
    await button.trigger('click')
    await flushPromises()
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    expect(showError).toHaveBeenCalledWith('admin.settings.openaiSyncModels.loadFailed')
    await button.trigger('click')
    await flushPromises()
    expect(wrapper.emitted('update:modelValue')!.at(-1)![0]).toEqual(['gpt-6-astra', 'gpt-reserve'])
  })

  it('keeps non-OpenAI fill related behavior local', async () => {
    const wrapper = mountSelector({ platform: 'anthropic' })
    const button = wrapper.findAll('button').find(item => item.text() === 'admin.accounts.fillRelatedModels')
    await button!.trigger('click')
    await flushPromises()
    expect(getOpenAISyncModelIDs).not.toHaveBeenCalled()
    expect(wrapper.emitted('update:modelValue')).toBeDefined()
  })

  it('warns when model IDs sync but capability metadata is incomplete', async () => {
    syncUpstreamModels.mockResolvedValue({
      models: ['x-preview-f-free'],
      warnings: [
        {
          code: 'upstream_model_metadata_incomplete',
          message: 'Model IDs were synced, but capability metadata could not be updated.'
        }
      ]
    })
    const wrapper = mount(ModelWhitelistSelector, {
      props: {
        modelValue: [],
        platform: 'openai',
        accountId: 46
      },
      global: {
        stubs: {
          ModelIcon: true
        }
      }
    })

    const syncButton = wrapper
      .findAll('button')
      .find(button => button.text() === 'admin.accounts.syncUpstreamModels')
    expect(syncButton).toBeDefined()
    await syncButton!.trigger('click')
    await flushPromises()

    expect(wrapper.emitted('update:modelValue')).toEqual([[['x-preview-f-free']]])
    expect(showWarning).toHaveBeenCalledWith('admin.accounts.syncUpstreamModelsMetadataIncomplete')
    expect(showSuccess).not.toHaveBeenCalled()
  })

  it('shows success and a partial warning when some capabilities were saved', async () => {
    syncUpstreamModels.mockResolvedValue({
      models: ['gpt-6-astra', 'gpt-image-2'],
      warnings: [
        {
          code: 'upstream_model_metadata_partial',
          message: 'Some model capabilities were saved; remaining models are still incomplete.'
        }
      ]
    })
    const wrapper = mount(ModelWhitelistSelector, {
      props: {
        modelValue: [],
        platform: 'openai',
        accountId: 46
      },
      global: {
        stubs: {
          ModelIcon: true
        }
      }
    })

    const syncButton = wrapper
      .findAll('button')
      .find(button => button.text() === 'admin.accounts.syncUpstreamModels')
    expect(syncButton).toBeDefined()
    await syncButton!.trigger('click')
    await flushPromises()

    expect(wrapper.emitted('update:modelValue')).toEqual([[['gpt-6-astra', 'gpt-image-2']]])
    expect(showSuccess).toHaveBeenCalledWith('admin.accounts.syncUpstreamModelsSuccess')
    expect(showWarning).toHaveBeenCalledWith('admin.accounts.syncUpstreamModelsMetadataPartial')
  })

  it('reports a successful preview so account creation can persist metadata', async () => {
    syncUpstreamModelsPreview.mockResolvedValue({
      models: ['x-preview-f-free'],
      metadata: {
        'x-preview-f-free': {
          id: 'x-preview-f-free',
          reasoning: true,
          supported_reasoning_levels: ['low', 'high', 'max'],
        },
      },
    })
    const wrapper = mountSelector({
      syncCredentials: {
        platform: 'openai',
        type: 'apikey',
        base_url: 'https://opencode.ai/zen/v1',
        api_key: 'test-key',
      },
    })
    const syncButton = wrapper
      .findAll('button')
      .find(button => button.text() === 'admin.accounts.syncUpstreamModels')

    expect(syncButton).toBeDefined()
    await syncButton?.trigger('click')
    await flushPromises()

    expect(syncUpstreamModelsPreview).toHaveBeenCalledOnce()
    expect(wrapper.emitted('upstream-synced')).toEqual([[]])
    expect(wrapper.emitted('update:modelValue')).toEqual([[['x-preview-f-free']]])
  })

  it('shows the upstream sync button for OpenCode Go create-account credentials', () => {
    const wrapper = mountSelector({
      platform: 'opencode_go',
      syncCredentials: {
        platform: 'opencode_go',
        type: 'apikey',
        base_url: 'https://opencode.ai/zen/go/v1',
        api_key: 'sk-test',
      },
    })
    const syncButton = wrapper
      .findAll('button')
      .find(button => button.text() === 'admin.accounts.syncUpstreamModels')

    expect(syncButton).toBeDefined()
    expect(syncButton?.exists()).toBe(true)
  })
})
