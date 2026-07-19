<script setup lang="ts">
import { onMounted } from 'vue'
import { RouterView } from 'vue-router'
import { ElConfigProvider } from 'element-plus'
import zhCn from 'element-plus/es/locale/lang/zh-cn'

import AppShell from './layout/AppShell.vue'
import { useSystemStore } from './stores/system'

const systemStore = useSystemStore()

onMounted(() => {
  void systemStore.refresh()
})
</script>

<template>
  <ElConfigProvider :locale="zhCn" size="default" :z-index="3000">
    <AppShell :device-name="systemStore.deviceName" :connection-state="systemStore.connectionState">
      <RouterView />
    </AppShell>
  </ElConfigProvider>
</template>
