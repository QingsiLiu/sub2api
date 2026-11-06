<template>
  <BaseDialog :show="show" :title="t('invoice.issuerSettings')" width="wide" @close="emit('close')">
    <div v-if="loading" class="flex justify-center py-10">
      <Icon name="refresh" size="lg" class="animate-spin text-gray-400" />
    </div>
    <form v-else id="invoice-issuer-form" class="space-y-4" @submit.prevent="save">
      <div class="grid gap-4 md:grid-cols-2">
        <div>
          <label class="input-label">{{ t('invoice.issuer.companyName') }}</label>
          <input v-model="form.company_name" type="text" maxlength="200" class="input" required />
        </div>
        <div>
          <label class="input-label">{{ t('invoice.issuer.numberPrefix') }}</label>
          <input
            v-model="form.number_prefix"
            type="text"
            maxlength="12"
            pattern="[A-Za-z0-9]{1,12}"
            class="input font-mono uppercase"
            required
          />
          <p class="input-hint">{{ t('invoice.issuer.numberPrefixHint', { example: numberExample }) }}</p>
        </div>
        <div>
          <label class="input-label">{{ t('invoice.issuer.companyAddress') }}</label>
          <textarea v-model="form.company_address" rows="3" maxlength="2000" class="input"></textarea>
        </div>
        <div>
          <label class="input-label">{{ t('invoice.issuer.companyContact') }}</label>
          <textarea v-model="form.company_contact" rows="3" maxlength="2000" class="input"></textarea>
          <p class="input-hint">{{ t('invoice.issuer.companyContactHint') }}</p>
        </div>
      </div>

      <div v-for="currency in INVOICE_CURRENCIES" :key="currency">
        <label class="input-label">{{ t('invoice.issuer.payment', { currency }) }}</label>
        <textarea v-model="form.payment[currency]" rows="5" maxlength="2000" class="input font-mono text-sm"></textarea>
        <p class="input-hint">{{ t('invoice.issuer.paymentHint') }}</p>
      </div>

      <div>
        <label class="input-label">{{ t('invoice.issuer.footerNote') }}</label>
        <textarea v-model="form.footer_note" rows="2" maxlength="2000" class="input"></textarea>
      </div>

      <div>
        <label class="input-label">{{ t('invoice.issuer.seal') }}</label>
        <div class="flex flex-wrap items-center gap-4">
          <div class="flex h-32 w-32 items-center justify-center rounded-lg border border-dashed border-gray-300 bg-white dark:border-dark-500">
            <img v-if="sealPreview" :src="sealPreview" alt="" class="max-h-28 max-w-28 object-contain" />
            <span v-else class="px-2 text-center text-xs text-gray-400">{{ t('invoice.issuer.sealNone') }}</span>
          </div>
          <div class="flex flex-col gap-2">
            <label class="btn btn-secondary cursor-pointer">
              <Icon name="upload" size="md" class="mr-1" />
              {{ t('invoice.issuer.sealUpload') }}
              <input type="file" accept="image/png,image/jpeg,image/webp" class="hidden" @change="onSealFile" />
            </label>
            <button v-if="sealPreview" type="button" class="btn btn-secondary" @click="removeSeal">
              <Icon name="trash" size="md" class="mr-1" />
              {{ t('invoice.issuer.sealRemove') }}
            </button>
          </div>
        </div>
        <p class="input-hint">{{ t('invoice.issuer.sealHint') }}</p>
      </div>
    </form>
    <template #footer>
      <div class="flex justify-end gap-3">
        <button type="button" class="btn btn-secondary" @click="emit('close')">{{ t('common.cancel') }}</button>
        <button type="submit" form="invoice-issuer-form" class="btn btn-primary" :disabled="saving || loading">
          {{ saving ? t('common.saving') : t('common.save') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import {
  INVOICE_CURRENCIES,
  invoicesAPI,
  type InvoiceCurrency,
  type InvoiceIssuer,
  type InvoiceIssuerPayload
} from '@/api/admin/invoices'
import { extractApiErrorMessage } from '@/utils/apiError'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'

const props = defineProps<{ show: boolean }>()
const emit = defineEmits<{ close: []; saved: [issuer: InvoiceIssuer] }>()

const { t } = useI18n()
const appStore = useAppStore()

const SEAL_MAX_SIDE = 800
const SEAL_MAX_BYTES = 1 << 20

const loading = ref(false)
const saving = ref(false)
const form = reactive({
  company_name: '',
  company_address: '',
  company_contact: '',
  number_prefix: 'INV',
  payment: { USD: '', HKD: '', CNY: '' } as Record<InvoiceCurrency, string>,
  footer_note: ''
})
const sealPreview = ref('')
// undefined = unchanged, '' = remove, data URL = replace
const sealChange = ref<string | undefined>(undefined)

const numberExample = computed(
  () => `${(form.number_prefix || 'INV').toUpperCase()}-${new Date().getFullYear()}-0001`
)

watch(
  () => props.show,
  (open) => {
    if (open) void load()
  }
)

async function load() {
  loading.value = true
  sealChange.value = undefined
  try {
    const issuer = await invoicesAPI.getIssuer()
    Object.assign(form, {
      company_name: issuer.company_name,
      company_address: issuer.company_address,
      company_contact: issuer.company_contact,
      number_prefix: issuer.number_prefix || 'INV',
      payment: { USD: issuer.payment.USD || '', HKD: issuer.payment.HKD || '', CNY: issuer.payment.CNY || '' },
      footer_note: issuer.footer_note
    })
    sealPreview.value = issuer.seal_image
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('invoice.failedToLoad')))
    emit('close')
  } finally {
    loading.value = false
  }
}

function dataURLBytes(dataURL: string): number {
  const base64 = dataURL.slice(dataURL.indexOf(',') + 1)
  return Math.floor((base64.length * 3) / 4)
}

// Re-encodes the upload as PNG on a canvas: keeps transparency, bounds the size
// and drops any metadata in the original file.
function encodeSeal(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const url = URL.createObjectURL(file)
    const img = new Image()
    img.onload = () => {
      URL.revokeObjectURL(url)
      const scale = Math.min(1, SEAL_MAX_SIDE / Math.max(img.naturalWidth, img.naturalHeight))
      const canvas = document.createElement('canvas')
      canvas.width = Math.max(1, Math.round(img.naturalWidth * scale))
      canvas.height = Math.max(1, Math.round(img.naturalHeight * scale))
      const ctx = canvas.getContext('2d')
      if (!ctx) {
        reject(new Error('canvas'))
        return
      }
      ctx.drawImage(img, 0, 0, canvas.width, canvas.height)
      resolve(canvas.toDataURL('image/png'))
    }
    img.onerror = () => {
      URL.revokeObjectURL(url)
      reject(new Error('image'))
    }
    img.src = url
  })
}

async function onSealFile(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file) return
  try {
    const dataURL = await encodeSeal(file)
    if (dataURLBytes(dataURL) > SEAL_MAX_BYTES) {
      appStore.showError(t('invoice.issuer.sealTooLarge'))
      return
    }
    sealPreview.value = dataURL
    sealChange.value = dataURL
  } catch {
    appStore.showError(t('invoice.issuer.sealInvalid'))
  }
}

function removeSeal() {
  sealPreview.value = ''
  sealChange.value = ''
}

async function save() {
  if (saving.value) return
  saving.value = true
  try {
    const payload: InvoiceIssuerPayload = {
      company_name: form.company_name.trim(),
      company_address: form.company_address.trim(),
      company_contact: form.company_contact.trim(),
      number_prefix: form.number_prefix.trim().toUpperCase(),
      payment: { ...form.payment },
      footer_note: form.footer_note.trim()
    }
    if (sealChange.value !== undefined) payload.seal_image = sealChange.value
    const saved = await invoicesAPI.updateIssuer(payload)
    appStore.showSuccess(t('invoice.issuer.saved'))
    emit('saved', saved)
    emit('close')
  } catch (error) {
    appStore.showError(
      extractApiErrorMessage(error, t('invoice.issuer.failedToSave'), {
        INVOICE_ISSUER_INVALID: t('invoice.errors.issuerInvalid')
      })
    )
  } finally {
    saving.value = false
  }
}
</script>
