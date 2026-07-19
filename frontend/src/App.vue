<script setup lang="ts">
import { RouterView } from 'vue-router'
import { ElConfigProvider } from 'element-plus'
import zhCn from 'element-plus/es/locale/lang/zh-cn'

import AppShell from './layout/AppShell.vue'
</script>

<template>
  <ElConfigProvider :locale="zhCn" size="default" :z-index="3000">
    <AppShell>
      <RouterView />
    </AppShell>
  </ElConfigProvider>
</template>
