<template>
  <!-- geili: assign or clear the supplier owner of one account. -->
  <BaseDialog
    :show="show"
    :title="t('supplier.admin.setOwnerTitle', { name: account?.name || '' })"
    width="narrow"
    @close="emit('close')"
  >
    <form id="supplier-owner-form" class="space-y-3" @submit.prevent="save">
      <div>
        <label class="input-label">{{ t('supplier.admin.ownerUserId') }}</label>
        <input v-model.number="ownerUserId" type="number" min="0" step="1" class="input" required />
        <p class="input-hint">{{ t('supplier.admin.ownerUserIdHint') }}</p>
      </div>
    </form>
    <template #footer>
      <div class="flex justify-end gap-3">
        <button type="button" class="btn btn-secondary" @click="emit('close')">{{ t('common.cancel') }}</button>
        <button type="submit" form="supplier-owner-form" class="btn btn-primary" :disabled="saving">
          {{ saving ? t('common.saving') : t('common.save') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { adminSetSupplierOwner } from '@/api/supplier'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { Account } from '@/types'

const props = defineProps<{ show: boolean; account: Account | null }>()
const emit = defineEmits<{
  (e: 'close'): void
  (e: 'saved', owner: { id: number; email: string } | null): void
}>()

const { t } = useI18n()
const appStore = useAppStore()
const ownerUserId = ref(0)
const saving = ref(false)

watch(
  () => [props.show, props.account] as const,
  ([show, account]) => {
    if (show) ownerUserId.value = account?.supplier_owner?.id ?? 0
  }
)

async function save() {
  if (!props.account || saving.value) return
  const id = Number(ownerUserId.value)
  if (!Number.isInteger(id) || id < 0) return
  saving.value = true
  try {
    const result = await adminSetSupplierOwner(props.account.id, id)
    appStore.showSuccess(t('supplier.admin.ownerSaved'))
    emit('saved', result.supplier_owner)
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('supplier.accounts.failedToSave')))
  } finally {
    saving.value = false
  }
}
</script>
