<template>
  <AuthLayout>
    <div class="space-y-5">
      <h2 class="text-center text-2xl font-bold text-gray-900 dark:text-white">{{ t('admin.accounts.keyIntake.title') }}</h2>
      <p v-if="loading" class="text-center text-sm text-gray-500" role="status">{{ t('admin.accounts.keyIntake.checking') }}</p>
      <div v-else-if="submitted" class="rounded-xl bg-green-50 p-5 text-green-800 dark:bg-green-900/20 dark:text-green-200" role="status">
        {{ t('admin.accounts.keyIntake.success') }}
      </div>
      <template v-else>
        <p v-if="error" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300" role="alert">{{ error }}</p>
        <form v-if="invite" class="space-y-5" @submit.prevent="submit">
          <p class="text-sm text-gray-600 dark:text-dark-300">{{ t('admin.accounts.keyIntake.official', { platform: platformName }) }}</p>
          <p class="text-sm text-gray-500 dark:text-dark-400">{{ t('admin.accounts.keyIntake.privacy') }}</p>
          <div>
            <label for="upstream-key" class="input-label">API Key</label>
            <input id="upstream-key" v-model="apiKey" name="upstream-key" type="password" autocomplete="off" autocapitalize="none" :spellcheck="false" maxlength="2048" required :disabled="busy" class="input w-full" />
          </div>
          <button class="btn btn-primary w-full" type="submit" :disabled="busy || !apiKey.trim()">{{ busy ? t('admin.accounts.keyIntake.submitting') : t('admin.accounts.keyIntake.submit') }}</button>
          <p class="text-xs text-gray-500 dark:text-dark-400">{{ t('admin.accounts.keyIntake.noProbe') }}</p>
        </form>
        <button v-else-if="retryable" class="btn btn-secondary w-full" :disabled="loading" @click="inspect">{{ t('admin.accounts.keyIntake.retry') }}</button>
      </template>
    </div>
  </AuthLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AuthLayout from '@/components/layout/AuthLayout.vue'
import { inspectSubmissionInvite, submitAccountKey, SubmissionRequestError, type PublicSubmissionInvite } from '@/api/accountSubmission'

const { t } = useI18n()
let token = new URLSearchParams(window.location.hash.slice(1)).get('token') || ''
let requestEpoch = 0
const apiKey = ref('')
const invite = ref<PublicSubmissionInvite | null>(null)
const loading = ref(true)
const busy = ref(false)
const submitted = ref(false)
const error = ref('')
const retryable = ref(false)
const platformName = computed(() => invite.value?.platform === 'openai' ? 'OpenAI' : 'Claude')

function finish() {
  apiKey.value = ''
  token = ''
  submitted.value = true
  error.value = ''
  window.history.replaceState(window.history.state, '', window.location.pathname + window.location.search)
}
function showError(cause: unknown) {
  const status = cause instanceof SubmissionRequestError ? cause.status : 0
  retryable.value = status === 0 || status === 503 || status === 429
  if (status === 410 || status === 400) {
    invite.value = null
    apiKey.value = ''
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
async function submit() {
  if (busy.value) return
  const key = apiKey.value.trim()
  if (!key || key.length > 2048 || /[\s\p{Cc}]/u.test(key)) {
    error.value = t('admin.accounts.keyIntake.keyInvalid')
    return
  }
  busy.value = true
  const epoch = requestEpoch
  const requestToken = token
  error.value = ''
  try {
    await submitAccountKey(requestToken, key)
    if (epoch !== requestEpoch) return
    finish()
  } catch (cause) {
    if (epoch !== requestEpoch) return
    // Confirm a committed submission when its response was lost. Never auto-resubmit the key.
    try {
      const state = await inspectSubmissionInvite(requestToken)
      if (epoch !== requestEpoch) return
      if (state.status === 'submitted') { finish(); return }
      if (cause instanceof SubmissionRequestError && cause.status === 400) {
        error.value = t('admin.accounts.keyIntake.submitRejected')
        return
      }
    } catch { if (epoch !== requestEpoch) return /* Show only the original sanitized status. */ }
    showError(cause)
  } finally { if (epoch === requestEpoch) busy.value = false }
}
function switchInvitation() {
  const next = new URLSearchParams(window.location.hash.slice(1)).get('token') || ''
  if (next === token) return
  requestEpoch++
  token = next
  apiKey.value = ''
  invite.value = null
  submitted.value = false
  busy.value = false
  retryable.value = false
  void inspect()
}
onMounted(() => { window.addEventListener('hashchange', switchInvitation); void inspect() })
onBeforeUnmount(() => { window.removeEventListener('hashchange', switchInvitation); requestEpoch++; apiKey.value = ''; token = '' })
</script>
