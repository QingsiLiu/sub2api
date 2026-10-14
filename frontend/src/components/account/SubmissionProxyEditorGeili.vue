<template>
  <div class="space-y-3">
    <label class="input-label">{{ t('admin.accounts.proxy') }}</label>
    <ProxySelector v-model="selection" :proxies="options" :disabled="disabled" public-mode />
    <div class="flex flex-wrap gap-2">
      <button type="button" class="btn btn-secondary" :disabled="disabled" @click="open">{{ t('admin.proxies.createProxy') }}</button>
      <button v-if="modelValue.proxy" type="button" class="btn btn-secondary" :disabled="disabled" @click="open">{{ t('common.edit') }}</button>
    </div>
    <p class="input-hint">{{ t('admin.accounts.keyIntake.connectionHint') }}</p>
    <BaseDialog :show="show" :title="t('admin.proxies.createProxy')" width="normal" @close="close">
      <form :id="formID" class="space-y-5" @submit.prevent="apply">
        <ProxyDraftFieldsGeili v-model="draft" :max-name-length="80" />
        <template v-if="hasPassword">
          <p class="input-hint">{{ t('admin.accounts.keyIntake.passwordKeep') }}</p>
          <label class="flex items-center gap-2 text-sm"><input v-model="clearPassword" type="checkbox" />{{ t('admin.accounts.keyIntake.passwordClear') }}</label>
        </template>
      </form>
      <template #footer><button type="button" class="btn btn-secondary" @click="close">{{ t('common.cancel') }}</button><button type="submit" :form="formID" class="btn btn-primary">{{ t('common.save') }}</button></template>
    </BaseDialog>
  </div>
</template>
<script setup lang="ts">
import { computed, ref, onBeforeUnmount, useId } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ProxySelector from '@/components/common/ProxySelector.vue'
import ProxyDraftFieldsGeili, { type ProxyDraftFields } from '@/components/common/ProxyDraftFieldsGeili.vue'
import type { SubmissionConnectionInput } from '@/api/accountSubmission'
const props = defineProps<{ modelValue: SubmissionConnectionInput; hasPreset?: boolean; hasPassword?: boolean; disabled?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: SubmissionConnectionInput] }>()
const { t } = useI18n()
const formID = `external-proxy-${useId()}`
const blank = (): ProxyDraftFields => ({ name: '', protocol: 'http', host: '', port: 8080, username: '', password: '' })
const show = ref(false), draft = ref(blank()), clearPassword = ref(false)
const options = computed(() => {
  const list: { id: number; name: string; host: string; port: number; protocol: string }[] = []
  if (props.hasPreset || props.modelValue.mode === 'preset') list.push({ id: -1, name: t('admin.accounts.keyIntake.presetProxy'), host: '', protocol: '', port: 0 })
  if (props.modelValue.proxy) list.push({ ...props.modelValue.proxy, id: -2 })
  return list
})
const selection = computed({ get: () => props.modelValue.mode === 'direct' ? null : props.modelValue.mode === 'preset' ? -1 : -2,
  set: id => emit('update:modelValue', { mode: id === null ? 'direct' : id === -1 ? 'preset' : 'custom', ...(id === -2 ? { proxy: props.modelValue.proxy } : {}) }) })
function open() { draft.value = { ...blank(), ...props.modelValue.proxy, password: props.modelValue.proxy?.password || '' }; clearPassword.value = props.modelValue.proxy?.clear_password || false; show.value = true }
function close() { show.value = false; draft.value = blank(); clearPassword.value = false }
function apply() { emit('update:modelValue', { mode: 'custom', proxy: { ...draft.value, password: clearPassword.value ? '' : draft.value.password, clear_password: clearPassword.value } }); close() }
onBeforeUnmount(close)
</script>
