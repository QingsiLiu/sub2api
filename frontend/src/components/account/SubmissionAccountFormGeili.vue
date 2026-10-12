<template>
  <button class="btn btn-primary w-full" @click="show = true">{{ t('admin.accounts.createAccount') }}</button>
  <CreateAccountModal :show="show" :proxies="[]" :groups="[]" @close="show = false" @created="emit('created', token)">
    <template #invitation-proxy>
      <SubmissionProxyEditorGeili v-model="connection" :has-preset="invite.has_preset_proxy" />
    </template>
  </CreateAccountModal>
</template>
<script setup lang="ts">
import { nextTick, onMounted, provide, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import CreateAccountModal from './CreateAccountModal.vue'
import SubmissionProxyEditorGeili from './SubmissionProxyEditorGeili.vue'
import { createSubmissionFormAPI, type PublicSubmissionInvite, type SubmissionConnectionInput } from '@/api/accountSubmission'
import { accountAdminAPIKeyGeili, accountInvitationFormKeyGeili } from '@/composables/useAccountAdminAPIGeili'
const props = defineProps<{token: string; invite: PublicSubmissionInvite}>()
const emit = defineEmits<{created: [token: string]}>()
const { t } = useI18n()
const show = ref(false)
const connection = ref<SubmissionConnectionInput>({mode: props.invite.has_preset_proxy ? 'preset' : 'direct'})
// The capability belongs to this mount, never a later fragment navigation.
const token = props.token
provide(accountInvitationFormKeyGeili, {platform:props.invite.platform,name:props.invite.name || '',concurrency:props.invite.concurrency || 1,priority:props.invite.priority,rate_multiplier:props.invite.rate_multiplier})
provide(accountAdminAPIKeyGeili, createSubmissionFormAPI(token, () => connection.value))
onMounted(async () => {await nextTick();show.value = true})
</script>
