<template>
  <div class="relative min-h-screen overflow-hidden bg-gray-50 dark:bg-dark-950">
    <div class="pointer-events-none fixed inset-0 bg-mesh-gradient"></div>
    <header class="sticky top-0 z-40 border-b border-gray-200/70 bg-white/85 backdrop-blur-xl dark:border-dark-700/70 dark:bg-dark-950/85">
      <nav class="mx-auto flex h-16 max-w-[1600px] items-center justify-between px-4 sm:px-6 lg:px-8">
        <router-link to="/home" class="flex min-w-0 items-center gap-3">
          <img :src="siteLogo || '/logo.svg'" alt="" class="h-9 w-9 rounded-xl object-contain shadow-sm" />
          <span class="truncate text-sm font-semibold text-gray-900 dark:text-white">{{ siteName }}</span>
        </router-link>

        <div class="flex items-center gap-1.5 sm:gap-3">
          <router-link
            to="/pricing"
            class="hidden rounded-lg bg-primary-50 px-3 py-2 text-sm font-medium text-primary-700 dark:bg-primary-900/20 dark:text-primary-300 sm:inline-flex"
          >
            {{ t('pricing.title') }}
          </router-link>
          <LocaleSwitcher />
          <button
            class="rounded-lg p-2 text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-700 dark:text-dark-400 dark:hover:bg-dark-800 dark:hover:text-white"
            :title="isDark ? t('home.switchToLight') : t('home.switchToDark')"
            @click="toggleTheme"
          >
            <Icon :name="isDark ? 'sun' : 'moon'" size="md" />
          </button>
          <router-link
            :to="authStore.isAuthenticated ? dashboardPath : '/login'"
            class="btn btn-primary btn-sm whitespace-nowrap"
          >
            {{ authStore.isAuthenticated ? t('home.dashboard') : t('home.login') }}
          </router-link>
        </div>
      </nav>
    </header>
    <main class="relative z-10 px-4 py-6 sm:px-6 lg:px-8 lg:py-8">
      <div class="mx-auto max-w-[1600px]"><slot /></div>
    </main>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import Icon from '@/components/icons/Icon.vue'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'

const { t } = useI18n()
const appStore = useAppStore()
const authStore = useAuthStore()
const isDark = ref(document.documentElement.classList.contains('dark'))
const siteName = computed(() => appStore.siteName || 'Sub2API')
const siteLogo = computed(() => appStore.siteLogo || '')
const dashboardPath = computed(() => (authStore.isAdmin ? '/admin/dashboard' : '/dashboard'))

function toggleTheme() {
  isDark.value = !isDark.value
  document.documentElement.classList.toggle('dark', isDark.value)
  localStorage.setItem('theme', isDark.value ? 'dark' : 'light')
}
</script>
