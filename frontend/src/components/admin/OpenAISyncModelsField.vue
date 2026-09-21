<template>
  <section class="card" data-testid="openai-sync-model-settings">
    <div class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
      <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('admin.settings.openaiSyncModels.title') }}</h2>
      <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.settings.openaiSyncModels.description') }}</p>
    </div>
    <div class="space-y-4 p-6">
      <ol class="flex flex-wrap gap-2">
        <li v-for="(model, index) in modelValue" :key="model" data-testid="openai-sync-model-chip" class="inline-flex max-w-full items-center gap-2 rounded-lg border border-gray-200 bg-gray-50 px-3 py-2 text-sm dark:border-dark-600 dark:bg-dark-700">
          <span class="text-xs tabular-nums text-gray-400">{{ index + 1 }}</span>
          <span class="truncate font-mono text-gray-800 dark:text-gray-100" :title="model">{{ model }}</span>
          <div class="flex shrink-0 items-center gap-1">
            <button type="button" class="btn btn-secondary btn-sm !px-1" :disabled="disabled || index === 0" :aria-label="t('admin.settings.openaiSyncModels.moveUp', { model })" @click="move(index, -1)"><Icon name="arrowUp" size="xs" /></button>
            <button type="button" class="btn btn-secondary btn-sm !px-1" :disabled="disabled || index === modelValue.length - 1" :aria-label="t('admin.settings.openaiSyncModels.moveDown', { model })" @click="move(index, 1)"><Icon name="arrowDown" size="xs" /></button>
            <button type="button" class="btn btn-secondary btn-sm !px-1" :disabled="disabled || modelValue.length <= 1" :aria-label="t('admin.settings.openaiSyncModels.remove', { model })" @click="remove(index)"><Icon name="x" size="xs" /></button>
          </div>
        </li>
      </ol>
      <div class="flex flex-wrap items-center gap-2">
        <Select v-model="draft" class="w-full sm:w-72" :options="options" searchable :disabled="disabled" :placeholder="t('admin.settings.openaiSyncModels.addPlaceholder')" :aria-label="t('admin.settings.openaiSyncModels.addPlaceholder')" />
        <button type="button" class="btn btn-secondary btn-sm" :disabled="disabled || !options.some(option => option.value === draft)" @click="add">{{ t('admin.settings.openaiSyncModels.add') }}</button>
        <button type="button" class="btn btn-secondary btn-sm" :disabled="disabled" @click="emit('update:modelValue', [...DEFAULT_OPENAI_SYNC_MODEL_IDS])">{{ t('admin.settings.openaiSyncModels.reset') }}</button>
      </div>
      <p v-if="disabled" class="text-sm text-amber-600 dark:text-amber-400">{{ t('admin.settings.openaiSyncModels.loadFailed') }}</p>
      <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.settings.openaiSyncModels.hint') }}</p>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import { DEFAULT_OPENAI_SYNC_MODEL_IDS } from '@/constants/openaiSyncModels'

const props = defineProps<{ modelValue: string[]; candidates: string[]; disabled?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [models: string[]] }>()
const { t } = useI18n()
const draft = ref<string | null>(null)
const options = computed(() => props.candidates.filter(model => !props.modelValue.includes(model)).map(model => ({ value: model, label: model })))
function add() {
  if (props.disabled || !draft.value || !options.value.some(option => option.value === draft.value)) return
  emit('update:modelValue', [...props.modelValue, draft.value])
  draft.value = null
}
function remove(index: number) {
  if (!props.disabled && props.modelValue.length > 1) emit('update:modelValue', props.modelValue.filter((_, i) => i !== index))
}
function move(index: number, delta: number) {
  const target = index + delta
  if (props.disabled || target < 0 || target >= props.modelValue.length) return
  const models = [...props.modelValue]
  ;[models[index], models[target]] = [models[target], models[index]]
  emit('update:modelValue', models)
}
</script>
