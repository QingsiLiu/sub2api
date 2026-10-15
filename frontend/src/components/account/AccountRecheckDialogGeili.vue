<template>
  <BaseDialog :show="show" :title="t('admin.accounts.runtime.recheck')" width="normal" @close="close">
    <form id="account-recheck-geili" class="space-y-3" @submit.prevent="submit">
      <p class="text-sm font-medium">{{ account?.name }}</p>
      <label class="block text-sm" for="account-recheck-model-geili">{{ t('admin.accounts.runtime.model') }}</label>
      <input id="account-recheck-model-geili" v-model="model" class="input w-full" :disabled="checking || account?.runtime_state?.balance_blocked" autocomplete="off" />
      <p class="text-xs text-gray-500">{{ t('admin.accounts.runtime.modelHint') }}</p>
      <p v-if="account?.runtime_state?.balance_blocked" role="alert" class="text-sm text-amber-600">{{ t('admin.accounts.runtime.rechargeHint') }}</p>
      <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
    </form>
    <template #footer>
      <button type="button" class="btn btn-secondary" :disabled="checking" @click="close">{{ t('common.cancel') }}</button>
      <button type="submit" form="account-recheck-geili" class="btn btn-primary" :disabled="checking || !model.trim() || account?.runtime_state?.balance_blocked">{{ checking ? t('admin.accounts.runtime.checking') : t('admin.accounts.runtime.recheck') }}</button>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import BaseDialog from '@/components/common/BaseDialog.vue'
import type { Account } from '@/types'

const props = defineProps<{ show: boolean; account: Account | null }>()
const emit = defineEmits<{ (e: 'close'): void; (e: 'checked', account: Account): void }>()
const { t } = useI18n()
const model = ref('')
const checking = ref(false)
const error = ref('')
let version = 0
watch([() => props.show, () => props.account?.id], () => {
  version++
  model.value = ''
  error.value = ''
  checking.value = false
})
const close = () => {
  if (!checking.value) emit('close')
}
const submit = () => {
  if (!model.value.trim() || checking.value || props.account?.runtime_state?.balance_blocked) return
  void run()
}
const run = async () => {
  if (!props.account || checking.value || !model.value.trim() || props.account.runtime_state?.balance_blocked) return
  const requestVersion = ++version
  const accountID = props.account.id
  const selectedModel = model.value.trim()
  checking.value = true
  error.value = ''
  try {
    const account = await adminAPI.accounts.recheck(accountID, { model: selectedModel })
    if (version === requestVersion && props.show && props.account?.id === accountID) {
      emit('checked', account)
      emit('close')
    }
  } catch (err: unknown) {
    if (version === requestVersion) error.value = err instanceof Error ? err.message : t('admin.accounts.runtime.failed')
  } finally {
    if (version === requestVersion) checking.value = false
  }
}
</script>
