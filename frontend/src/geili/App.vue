<script setup lang="ts">
/**
 * Geili 全站壳（通过 overrides.ts 替换上游 `src/App.vue`）
 *
 * 骨架阶段采用 wrapper 模式：把上游 App.vue 原样渲染，只在外面挂 Geili 全局能力。
 * 后续把上游 App.vue 里的 Toast / 公告弹窗 / 合规对话框逐个换成 Geili 实现时，
 * 直接在这里展开，不再引用 UpstreamApp 即可。
 *
 * 注意：在本文件里 `import '@/App.vue'` 拿到的是**上游原文件**（见 overrides.ts 规则 2）。
 */
import UpstreamApp from '@/App.vue'
import { installGeiliMessages } from './i18n'

installGeiliMessages()
document.documentElement.setAttribute('data-geili-ui', '')
</script>

<template>
  <UpstreamApp />
</template>
