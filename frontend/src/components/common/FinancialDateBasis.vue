<template>
  <div class="w-full sm:w-auto sm:min-w-[240px]">
    <label class="input-label" :for="id">{{ t('financial.dateBasis') }}</label>
    <select :id="id" class="input" :value="modelValue" data-testid="financial-date-basis" @change="updateBasis">
      <option value="accounting">{{ t('financial.accountingBasis') }}</option>
      <option value="completed">{{ t('financial.completedBasis') }}</option>
    </select>
  </div>
</template>
<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { UsageDateBasis } from '@/types'
withDefaults(defineProps<{ modelValue: UsageDateBasis; id?: string }>(), { id: 'financial-date-basis' })
const emit = defineEmits<{ 'update:modelValue': [value: UsageDateBasis]; change: [value: UsageDateBasis] }>()
const { t } = useI18n()
function updateBasis(event: Event) {
  const value = (event.target as HTMLSelectElement).value === 'completed' ? 'completed' : 'accounting'
  emit('update:modelValue', value)
  emit('change', value)
}
</script>
