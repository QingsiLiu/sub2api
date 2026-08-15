<template>
  <AppLayout v-if="authStore.isAuthenticated">
    <PricingCatalogContent />
  </AppLayout>
  <PricingPublicLayout v-else>
    <PricingCatalogContent />
  </PricingPublicLayout>
</template>

<script setup lang="ts">
import AppLayout from '@/components/layout/AppLayout.vue'
import PricingPublicLayout from '@/components/pricing/PricingPublicLayout.vue'
import PricingCatalogContent from '@/components/pricing/PricingCatalogContent.vue'
import { useAuthStore } from '@/stores/auth'

const authStore = useAuthStore()
</script>
