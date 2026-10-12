<template>
  <div
    class="relative flex min-h-screen justify-center overflow-hidden p-4"
    :class="wide ? 'items-start sm:p-6 lg:p-8' : 'items-center'"
  >
    <!-- Background -->
    <div
      class="absolute inset-0 bg-gradient-to-br from-gray-50 via-primary-50/30 to-gray-100 dark:from-dark-950 dark:via-dark-900 dark:to-dark-950"
    ></div>

    <!-- Decorative Elements -->
    <div class="pointer-events-none absolute inset-0 overflow-hidden">
      <!-- Gradient Orbs -->
      <div
        class="absolute -right-40 -top-40 h-80 w-80 rounded-full bg-primary-400/20 blur-3xl"
      ></div>
      <div
        class="absolute -bottom-40 -left-40 h-80 w-80 rounded-full bg-primary-500/15 blur-3xl"
      ></div>
      <div
        class="absolute left-1/2 top-1/2 h-96 w-96 -translate-x-1/2 -translate-y-1/2 rounded-full bg-primary-300/10 blur-3xl"
      ></div>

      <!-- Grid Pattern -->
      <div
        class="absolute inset-0 bg-[linear-gradient(rgba(20,184,166,0.03)_1px,transparent_1px),linear-gradient(90deg,rgba(20,184,166,0.03)_1px,transparent_1px)] bg-[size:64px_64px]"
      ></div>
    </div>

    <!-- Content Container -->
    <!-- geili hook: submitted account dashboards use the available desktop width. -->
    <div class="relative z-10 w-full" :class="wide ? 'max-w-none' : 'max-w-md'">
      <!-- Logo/Brand -->
      <div :class="wide ? 'mb-5 grid grid-cols-[48px_1fr] gap-x-3 text-left' : 'mb-8 text-center'">
        <!-- Custom Logo or Default Logo -->
        <template v-if="settingsLoaded">
          <div
            class="inline-flex items-center justify-center overflow-hidden shadow-lg shadow-primary-500/30"
            :class="wide ? 'row-span-2 h-12 w-12 rounded-xl' : 'mb-4 h-16 w-16 rounded-2xl'"
          >
            <img :src="siteLogo || '/logo.svg'" alt="Logo" class="h-full w-full object-contain" />
          </div>
          <h1 class="text-gradient font-bold" :class="wide ? 'self-end text-xl' : 'mb-2 text-3xl'">
            {{ siteName }}
          </h1>
          <p class="text-sm text-gray-500 dark:text-dark-400">
            {{ siteSubtitle }}
          </p>
        </template>
      </div>

      <!-- Card Container -->
      <div class="card-glass rounded-2xl shadow-glass" :class="wide ? 'p-4 sm:p-6' : 'p-8'">
        <slot />
      </div>

      <!-- Footer Links -->
      <div v-if="!wide || $slots.footer" class="mt-6 text-center text-sm">
        <slot name="footer" />
      </div>

      <!-- Copyright -->
      <div class="text-center text-xs text-gray-400 dark:text-dark-500" :class="wide ? 'mt-5' : 'mt-8'">
        &copy; {{ currentYear }} {{ siteName }}. All rights reserved.
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useAppStore } from '@/stores'
import { sanitizeUrl } from '@/utils/url'

// geili hook: ordinary authentication pages retain their existing narrow layout.
withDefaults(defineProps<{ wide?: boolean }>(), { wide: false })

const appStore = useAppStore()

const siteName = computed(() => appStore.siteName || 'Sub2API')
const siteLogo = computed(() => sanitizeUrl(appStore.siteLogo || '', { allowRelative: true, allowDataUrl: true }))
const siteSubtitle = computed(() => appStore.cachedPublicSettings?.site_subtitle || 'Subscription to API Conversion Platform')
const settingsLoaded = computed(() => appStore.publicSettingsLoaded)

const currentYear = computed(() => new Date().getFullYear())

onMounted(() => {
  appStore.fetchPublicSettings()
})
</script>

<style scoped>
.text-gradient {
  @apply bg-gradient-to-r from-primary-600 to-primary-500 bg-clip-text text-transparent;
}
</style>
