import { readFileSync } from 'node:fs'
import { nextTick, reactive, ref, watch } from 'vue'
import { describe, expect, it } from 'vitest'

// Execute the real platform watcher so handleEdit's next-tick sequencing is
// covered (a static string assertion would miss an initialization regression).
const source = readFileSync(process.cwd() + '/src/views/admin/GroupsView.vue', 'utf8')
const begin = source.indexOf('watch(\n  () => editForm.platform,')
const end = source.indexOf('\n);', begin) + 3
const watcher = source.slice(begin, end)

describe('group model plaza editing', () => {
  it('preserves selected models when opening a different-platform group', async () => {
    const editForm = reactive({ platform: 'anthropic', reasoning_effort_mappings: [] })
    const editingGroup = ref({ id: 3, platform: 'openai', model_plaza_config: { mode: 'selected', models: ['fixture-gpt'] } })
    const editModelPlazaConfig = reactive({ mode: 'all', models: [] as string[] })
    const normalizeModelPlazaConfig = (value?: { mode: string; models: string[] }) => ({ mode: value?.mode ?? 'all', models: [...(value?.models ?? [])] })
    const no = () => undefined
    const init = new Function('watch','editForm','editingGroup','editModelPlazaConfig','normalizeModelPlazaConfig','supportsMessagesDispatchPlatform','resetMessagesDispatchFormState','supportsLivePlatform','isProfitControlPlatform','normalizeReasoningEffortForPlatform','supportsReasoningEffortPolicyPlatform','normalizeReasoningEffortOverLimit','reasoningEffortOverLimitDowngrade','reasoningEffortMappingsToRows','reasoningEffortMappingsToAPI','editReasoningEffortPolicyRef','resetDisabledBatchImagePricing','resetModelAllowlistState','editModelAllowlistState','loadModelPlazaCandidates','loadModelAllowlistCandidates', 'return ' + watcher)
    const stop = init((...args: Parameters<typeof watch>) => watch(...args),editForm,editingGroup,editModelPlazaConfig,normalizeModelPlazaConfig,()=>true,no,()=>true,()=>true,(_:unknown,v:unknown)=>v,()=>true,(v:unknown)=>v,'downgrade',(v:unknown)=>v,(v:unknown)=>v,{value:null},no,no,{},no,no)
    editForm.platform = 'openai'
    Object.assign(editModelPlazaConfig, normalizeModelPlazaConfig(editingGroup.value.model_plaza_config))
    await nextTick()
    expect(editModelPlazaConfig).toEqual({ mode: 'selected', models: ['fixture-gpt'] })
    editForm.platform = 'gemini'
    await nextTick()
    expect(editModelPlazaConfig).toEqual({mode:'all',models:[]})
    stop?.()
  })
})
