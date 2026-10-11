<template>
  <div>
    <label :for="inputId" class="input-label">{{ label }}</label>
    <input v-bind="$attrs" :id="inputId" :value="modelValue" :type="kind === 'key' ? 'password' : kind === 'concurrency' ? 'number' : 'text'"
      :required="required" :min="kind === 'concurrency' ? 1 : undefined" :max="maximum"
      :autocomplete="kind === 'key' ? 'off' : undefined" autocapitalize="none" :spellcheck="false"
      :placeholder="placeholder || (kind === 'name' ? t('admin.accounts.enterAccountName') : undefined)"
      :class="['input', kind === 'key' && 'font-mono']" @input="update" />
    <p v-if="hint" class="input-hint">{{ hint }}</p>
  </div>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
defineOptions({ inheritAttrs: false })
const props = withDefaults(defineProps<{ modelValue: string | number; kind: 'name' | 'key' | 'concurrency'; inputId?: string; maximum?: number; required?: boolean; placeholder?: string; hint?: string }>(), { required: true })
const emit = defineEmits<{ 'update:modelValue': [value: any] }>()
const { t } = useI18n()
const label = computed(() => t('admin.accounts.' + ({ name: 'accountName', key: 'apiKeyRequired', concurrency: 'concurrency' }[props.kind])))
function update(event: Event) {
  const value = (event.target as HTMLInputElement).value
  emit('update:modelValue', props.kind === 'concurrency' ? Math.max(1, Number(value) || 1) : value)
}
</script>
