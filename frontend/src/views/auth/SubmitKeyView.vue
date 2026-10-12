<template>
  <AuthLayout>
    <div class="space-y-5">
      <h2 class="text-center text-2xl font-bold text-gray-900 dark:text-white">{{ t('admin.accounts.keyIntake.title') }}</h2>
      <p v-if="loading" class="text-center text-sm text-gray-500" role="status">{{ t('admin.accounts.keyIntake.checking') }}</p>
      <section v-else-if="submitted" class="space-y-4">
      <div class="rounded-xl bg-green-50 p-5 text-green-800 dark:bg-green-900/20 dark:text-green-200" role="status">
        {{ t('admin.accounts.keyIntake.success') }}
      </div>
      <AccountSubmissionUsage :token="token" />
      <SubmissionConnectionGeili :key="token" :token="token" />
      </section>
      <template v-else>
        <p v-if="error" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300" role="alert">{{ error }}</p>
        <section v-if="invite" class="space-y-5">
          <p class="text-sm text-gray-500 dark:text-dark-400">{{ t('admin.accounts.keyIntake.privacy') }}</p>
          <SubmissionAccountFormGeili :key="token" :token="token" :invite="invite" @created="finish" />
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
import SubmissionAccountFormGeili from '@/components/account/SubmissionAccountFormGeili.vue'
import SubmissionConnectionGeili from '@/components/account/SubmissionConnectionGeili.vue'
import AccountSubmissionUsage from '@/components/account/AccountSubmissionUsage.vue'
import { inspectSubmissionInvite, SubmissionRequestError, type PublicSubmissionInvite } from '@/api/accountSubmission'

const { t } = useI18n()
let token = new URLSearchParams(window.location.hash.slice(1)).get('token') || ''
let requestEpoch = 0
const invite = ref<PublicSubmissionInvite | null>(null)
const loading = ref(true)
const submitted = ref(false)
const error = ref('')
const retryable = ref(false)

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
  retryable.value = false
  void inspect()
}
onMounted(() => { window.addEventListener('hashchange', switchInvitation); void inspect() })
onBeforeUnmount(() => { window.removeEventListener('hashchange', switchInvitation); requestEpoch++; token = '' })
</script>
