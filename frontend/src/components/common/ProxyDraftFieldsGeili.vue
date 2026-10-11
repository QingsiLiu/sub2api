<template>
  <div class="space-y-5">
    <div><label class="input-label">{{ t('admin.proxies.name') }}</label><input v-model="model.name" required :maxlength="maxNameLength" class="input" :placeholder="t('admin.proxies.enterProxyName')" /></div>
    <div><label class="input-label">{{ t('admin.proxies.protocol') }}</label><Select v-model="model.protocol" :options="protocols" /></div>
    <div class="grid grid-cols-2 gap-4">
      <div><label class="input-label">{{ t('admin.proxies.host') }}</label><input v-model="model.host" required maxlength="255" class="input" :placeholder="t('admin.proxies.form.hostPlaceholder')" /></div>
      <div><label class="input-label">{{ t('admin.proxies.port') }}</label><input v-model.number="model.port" required type="number" min="1" max="65535" class="input" :placeholder="t('admin.proxies.form.portPlaceholder')" /></div>
    </div>
    <div><label class="input-label">{{ t('admin.proxies.username') }}</label><input v-model="model.username" maxlength="100" autocomplete="off" class="input" :placeholder="t('admin.proxies.optionalAuth')" /></div>
    <div>
      <label class="input-label">{{ t('admin.proxies.password') }}</label>
      <div class="relative"><input v-model="model.password" :type="visible ? 'text' : 'password'" maxlength="100" autocomplete="new-password" class="input pr-10" :placeholder="t('admin.proxies.optionalAuth')" />
        <button type="button" class="absolute right-3 top-1/2 -translate-y-1/2 text-gray-400" :aria-label="t('admin.proxies.password')" @click="visible = !visible"><Icon :name="visible ? 'eyeOff' : 'eye'" size="md" /></button>
      </div>
    </div>
  </div>
</template>
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
export interface ProxyDraftFields { name: string; protocol: string; host: string; port: number; username: string; password: string }
const props = withDefaults(defineProps<{ modelValue: ProxyDraftFields; maxNameLength?: number }>(), { maxNameLength: 100 })
const emit = defineEmits<{ 'update:modelValue': [value: ProxyDraftFields] }>()
const model = computed({ get: () => props.modelValue, set: value => emit('update:modelValue', value) })
const { t } = useI18n()
const visible = ref(false)
const protocols = computed(() => ['http', 'https', 'socks5', 'socks5h'].map(value => ({ value, label: t('admin.proxies.protocols.' + value) })))
</script>
