<template>
  <AuthLayout :wide="submitted || !!invite">
    <div class="space-y-5">
      <h2 class="text-2xl font-bold text-gray-900 dark:text-white" :class="submitted ? 'text-left' : 'text-center'">{{ t('admin.accounts.keyIntake.title') }}</h2>
      <p v-if="loading" class="text-center text-sm text-gray-500" role="status">{{ t('admin.accounts.keyIntake.checking') }}</p>
      <section v-else-if="submitted" class="space-y-4">
        <div class="rounded-xl bg-green-50 p-3 text-sm text-green-800 dark:bg-green-900/20 dark:text-green-200" role="status">
          {{ t('admin.accounts.keyIntake.success') }}
        </div>
        <div class="grid min-w-0 items-start gap-5 lg:grid-cols-[minmax(0,1fr)_320px] xl:grid-cols-[minmax(0,1fr)_360px]">
          <div class="card min-w-0 p-4 sm:p-5">
            <AccountSubmissionUsage :token="token" @accounts="acceptAccounts" />
          </div>
          <div class="min-w-0 space-y-3">
            <label v-if="accounts.length > 1" class="input-label">{{ t('admin.accounts.keyIntake.connectionAccount') }}
              <select v-model="accountRef" class="input mt-1 w-full"><option v-for="account in accounts" :key="account.account_ref" :value="account.account_ref">{{ account.name }}</option></select>
            </label>
            <SubmissionConnectionGeili v-if="accounts.length || usageReady" :key="token + accountRef" :token="token" :account-ref="accountRef" />
          </div>
        </div>
      </section>
      <template v-else>
        <p v-if="error" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300" role="alert">{{ error }}</p>
        <section v-if="invite" class="space-y-5">
          <p class="text-sm text-gray-500 dark:text-dark-400">{{ t('admin.accounts.keyIntake.privacy') }}</p>
          <div class="flex gap-2">
            <button type="button" class="btn" :class="mode === 'batch' ? 'btn-primary' : 'btn-secondary'" @click="mode = 'batch'">{{ t('admin.accounts.keyIntake.batchMode') }}</button>
            <button type="button" class="btn" :class="mode === 'single' ? 'btn-primary' : 'btn-secondary'" @click="mode = 'single'">{{ t('admin.accounts.keyIntake.singleMode') }}</button>
          </div>
          <SubmissionBatchFormGeili v-if="mode === 'batch'" :key="token" :token="token" :invite="invite" @created="finish" />
          <SubmissionAccountFormGeili v-else :key="token" :token="token" :invite="invite" @created="finish" />
          <p class="text-xs text-gray-500">{{ t('admin.accounts.keyIntake.linkControl') }}</p>
        </section>
        <button v-else-if="retryable" class="btn btn-secondary w-full" :disabled="loading" @click="inspect">{{ t('admin.accounts.keyIntake.retry') }}</button>
      </template>
    </div>
  </AuthLayout>
</template>

<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AuthLayout from '@/components/layout/AuthLayout.vue'
import SubmissionBatchFormGeili from '@/components/account/SubmissionBatchFormGeili.vue'
import SubmissionAccountFormGeili from '@/components/account/SubmissionAccountFormGeili.vue'
import SubmissionConnectionGeili from '@/components/account/SubmissionConnectionGeili.vue'
import AccountSubmissionUsage from '@/components/account/AccountSubmissionUsage.vue'
import { inspectSubmissionInvite, SubmissionRequestError, type PublicSubmissionInvite, type SubmissionAccountUsage } from '@/api/accountSubmission'

const { t } = useI18n()
let token = new URLSearchParams(window.location.hash.slice(1)).get('token') || ''
let requestEpoch = 0
const invite = ref<PublicSubmissionInvite | null>(null)
const loading = ref(true)
const submitted = ref(false)
const error = ref('')
const retryable = ref(false)
const mode = ref<'batch' | 'single'>('batch')
const accounts = ref<SubmissionAccountUsage[]>([])
const accountRef = ref('')
const usageReady = ref(false)
function acceptAccounts(value: SubmissionAccountUsage[]) {
  accounts.value = value
  usageReady.value = true
  if (!value.some(account => account.account_ref === accountRef.value)) accountRef.value = value[0]?.account_ref || ''
}

function finish(requestToken = token) {
  if (requestToken !== token) return
  submitted.value = true
  error.value = ''
}
function showError(cause: unknown) {
  const status = cause instanceof SubmissionRequestError ? cause.status : 0
  retryable.value = status === 0 || status === 503 || status === 429
  if (status === 410 || status === 400) {
    invite.value = null
    error.value = t('admin.accounts.keyIntake.invalid')
  } else if (status === 429) {
    error.value = t('admin.accounts.keyIntake.rateLimited')
  } else {
    error.value = t('admin.accounts.keyIntake.failed')
  }
}
async function inspect() {
  const epoch = requestEpoch
  const requestToken = token
  loading.value = true
  error.value = ''
  try {
    if (!requestToken) throw new SubmissionRequestError(400)
    const state = await inspectSubmissionInvite(requestToken)
    if (epoch !== requestEpoch) return
    invite.value = state
    if (invite.value.status === 'submitted') finish()
  } catch (cause) { if (epoch === requestEpoch) showError(cause) }
  finally { if (epoch === requestEpoch) loading.value = false }
}
function switchInvitation() {
  const next = new URLSearchParams(window.location.hash.slice(1)).get('token') || ''
  if (next === token) return
  requestEpoch++
  token = next
  invite.value = null
  submitted.value = false
  accounts.value = []; accountRef.value = ''; usageReady.value = false; mode.value = 'batch'
  retryable.value = false
  void inspect()
}
onMounted(() => { window.addEventListener('hashchange', switchInvitation); void inspect() })
onBeforeUnmount(() => { window.removeEventListener('hashchange', switchInvitation); requestEpoch++; token = '' })
</script>
