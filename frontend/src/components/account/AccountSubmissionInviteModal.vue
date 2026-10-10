<template>
  <BaseDialog :show="show" :title="t('admin.accounts.keyIntake.invite')" width="wide" @close="close">
    <div class="space-y-6">
      <p class="text-sm text-gray-500 dark:text-dark-400">{{ t('admin.accounts.keyIntake.inviteHint') }}</p>
      <form class="space-y-4" @submit.prevent="create">
        <div class="grid gap-4 sm:grid-cols-2">
          <div><label for="invite-name" class="input-label">{{ t('admin.accounts.keyIntake.accountName') }}</label><input id="invite-name" v-model="form.name" class="input w-full" maxlength="100" required :disabled="busy" /></div>
          <div><label for="invite-platform" class="input-label">{{ t('admin.accounts.keyIntake.platform') }}</label><select id="invite-platform" v-model="form.platform" class="input w-full" :disabled="busy" @change="form.group_ids = []"><option value="anthropic">Claude</option><option value="openai">OpenAI</option></select></div>
          <div><label for="invite-concurrency" class="input-label">{{ t('admin.accounts.keyIntake.concurrency') }}</label><input id="invite-concurrency" v-model.number="form.concurrency" type="number" min="1" max="1000" class="input w-full" required :disabled="busy" /></div>
          <div><label for="invite-priority" class="input-label">{{ t('admin.accounts.keyIntake.priority') }}</label><input id="invite-priority" v-model.number="form.priority" type="number" min="0" max="10000" class="input w-full" required :disabled="busy" /></div>
          <div><label for="invite-rate" class="input-label">{{ t('admin.accounts.keyIntake.multiplier') }}</label><input id="invite-rate" v-model.number="form.rate_multiplier" type="number" min="0" max="999999" step="0.0001" class="input w-full" required :disabled="busy" /></div>
          <div><label for="invite-proxy" class="input-label">{{ t('admin.accounts.keyIntake.proxy') }}</label><select id="invite-proxy" v-model="form.proxy_id" class="input w-full" :disabled="busy"><option :value="null">{{ t('admin.accounts.keyIntake.direct') }}</option><option v-for="proxy in proxies" :key="proxy.id" :value="proxy.id">{{ proxy.name }}</option></select></div>
        </div>
        <fieldset :disabled="busy" class="space-y-2"><legend class="input-label">{{ t('admin.accounts.keyIntake.groups') }}</legend>
          <div class="flex max-h-40 flex-wrap gap-3 overflow-y-auto rounded-lg border border-gray-200 p-3 dark:border-dark-600">
            <label v-for="group in platformGroups" :key="group.id" class="flex items-center gap-2 text-sm"><input v-model="form.group_ids" type="checkbox" :value="group.id" class="checkbox" />{{ group.name }}</label>
            <span v-if="!platformGroups.length" class="text-sm text-gray-500">{{ t('admin.accounts.keyIntake.noGroups') }}</span>
          </div>
          <p class="text-xs text-gray-500">{{ t('admin.accounts.keyIntake.groupsHint') }}</p>
        </fieldset>
        <button type="submit" class="btn btn-primary" :disabled="busy || !!link">{{ busy ? t('admin.accounts.keyIntake.creating') : t('admin.accounts.keyIntake.generate') }}</button>
      </form>
      <div v-if="link" class="space-y-2 rounded-lg bg-primary-50 p-4 dark:bg-primary-900/20">
        <p class="text-sm">{{ t('admin.accounts.keyIntake.linkHint') }}</p>
        <input class="input w-full" :value="link" readonly :aria-label="t('admin.accounts.keyIntake.link')" @focus="($event.target as HTMLInputElement).select()" />
        <div class="flex gap-2"><button class="btn btn-secondary" @click="copy">{{ t('admin.accounts.keyIntake.copy') }}</button><button class="btn btn-secondary" @click="link = ''">{{ t('admin.accounts.keyIntake.another') }}</button></div>
      </div>
      <p v-if="error" class="text-sm text-red-600 dark:text-red-400" role="alert">{{ error }}</p>
      <div class="space-y-3 border-t border-gray-200 pt-4 dark:border-dark-700">
        <div class="flex items-center justify-between"><h4 class="font-medium">{{ t('admin.accounts.keyIntake.history') }}</h4><button class="btn btn-secondary" :disabled="listLoading" @click="load">{{ t('admin.accounts.keyIntake.refresh') }}</button></div>
        <p class="text-xs text-gray-500">{{ t('admin.accounts.keyIntake.enableHint') }}</p>
        <div v-for="item in items" :key="item.id" class="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-gray-200 p-3 dark:border-dark-700">
          <div class="min-w-0"><p class="break-words text-sm font-medium">{{ item.config.name }} · {{ item.config.platform === 'openai' ? 'OpenAI' : 'Claude' }}</p><p class="text-xs text-gray-500">{{ statusLabel(item.status) }} · {{ t('admin.accounts.keyIntake.expires') }} {{ new Date(item.expires_at).toLocaleString() }}</p><p v-if="item.account_id" class="text-xs text-gray-500">{{ t('admin.accounts.keyIntake.accountId', { id: item.account_id }) }}</p></div>
          <button v-if="item.status === 'pending'" class="btn btn-secondary" :disabled="revoking === item.id" @click="revoke(item.id)">{{ t('admin.accounts.keyIntake.revoke') }}</button>
        </div>
        <p v-if="!items.length && !listLoading" class="text-sm text-gray-500">{{ t('admin.accounts.keyIntake.empty') }}</p>
        <div class="flex items-center justify-between"><button class="btn btn-secondary" :disabled="page === 1 || listLoading" @click="page--; load()">{{ t('admin.accounts.keyIntake.previous') }}</button><span class="text-sm">{{ page }}</span><button class="btn btn-secondary" :disabled="page * 20 >= total || listLoading" @click="page++; load()">{{ t('admin.accounts.keyIntake.next') }}</button></div>
      </div>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { useAppStore } from '@/stores/app'
import { createSubmissionInvite, listSubmissionInvites, revokeSubmissionInvite, type SubmissionConfig, type SubmissionInvite } from '@/api/accountSubmission'
import type { AdminGroup, Proxy } from '@/types'
const props = defineProps<{ show: boolean; groups: AdminGroup[]; proxies: Proxy[] }>()
const emit = defineEmits<{ close: [] }>()
const { t } = useI18n()
const appStore = useAppStore()
const form = reactive<SubmissionConfig>({ name: '', platform: 'anthropic', group_ids: [], proxy_id: null, concurrency: 1, priority: 50, rate_multiplier: 1 })
const platformGroups = computed(() => props.groups.filter(group => group.platform === form.platform && group.status === 'active'))
const items = ref<SubmissionInvite[]>([])
const page = ref(1)
const total = ref(0)
const link = ref('')
const error = ref('')
const busy = ref(false)
const listLoading = ref(false)
const revoking = ref<number | null>(null)
function statusLabel(status: SubmissionInvite['status']) {
  const keys = { pending: 'admin.accounts.keyIntake.pending', submitted: 'admin.accounts.keyIntake.submitted', revoked: 'admin.accounts.keyIntake.revoked', expired: 'admin.accounts.keyIntake.expired' }
  return t(keys[status])
}
async function load() {
  listLoading.value = true
  try { const data = await listSubmissionInvites(page.value); items.value = data.items; total.value = data.total }
  catch { error.value = t('admin.accounts.keyIntake.adminFailed') }
  finally { listLoading.value = false }
}
async function create() {
  if (busy.value) return
  busy.value = true
  error.value = ''
  try {
    const data = await createSubmissionInvite({ ...form, group_ids: [...form.group_ids] })
    link.value = `${window.location.origin}/submit-key#token=${encodeURIComponent(data.token)}`
    page.value = 1
    await load()
  } catch { error.value = t('admin.accounts.keyIntake.adminFailed') }
  finally { busy.value = false }
}
async function revoke(id: number) {
  revoking.value = id
  error.value = ''
  try { await revokeSubmissionInvite(id); await load() }
  catch { error.value = t('admin.accounts.keyIntake.adminFailed') }
  finally { revoking.value = null }
}
async function copy() {
  try { await navigator.clipboard.writeText(link.value); appStore.showSuccess(t('admin.accounts.keyIntake.copied')) }
  catch { error.value = t('admin.accounts.keyIntake.copyFailed') }
}
function close() { if (!busy.value) { link.value = ''; emit('close') } }
watch(() => props.show, show => { if (show) { error.value = ''; page.value = 1; void load() } else { link.value = '' } })
</script>
