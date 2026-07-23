<script setup lang="ts">
import type { Component } from 'vue'

export interface WorkspaceStat {
  label: string
  value: string | number
  tone?: 'default' | 'success' | 'warning'
}

defineProps<{
  title: string
  description: string
  icon: Component
  stats: WorkspaceStat[]
}>()
</script>

<template>
  <section class="workspace-header panel" aria-labelledby="workspace-title">
    <div class="workspace-header__main">
      <div class="workspace-header__title">
        <span class="workspace-header__icon" aria-hidden="true"><component :is="icon" :size="21" /></span>
        <div>
          <h1 id="workspace-title">{{ title }}</h1>
          <p>{{ description }}</p>
        </div>
      </div>
      <dl v-if="stats.length" class="workspace-stats">
        <div v-for="stat in stats" :key="stat.label" :class="`workspace-stat workspace-stat--${stat.tone ?? 'default'}`">
          <dt>{{ stat.label }}</dt>
          <dd>{{ stat.value }}</dd>
        </div>
      </dl>
      <div class="workspace-header__actions"><slot name="actions" /></div>
    </div>
    <div v-if="$slots.tools" class="workspace-toolbar">
      <div class="workspace-tools">
        <slot name="tools" />
      </div>
    </div>
  </section>
</template>

<style scoped>
.workspace-header {
  overflow: hidden;
}
.workspace-header__main {
  display: grid;
  min-height: 92px;
  grid-template-columns: minmax(300px, 1fr) auto auto;
  align-items: center;
  gap: 24px;
  padding: 16px 18px;
}
.workspace-header__title {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 13px;
}
.workspace-header__icon {
  display: grid;
  width: 48px;
  height: 48px;
  flex: 0 0 auto;
  place-items: center;
  border: 1px solid rgba(36, 104, 216, .13);
  border-radius: 14px;
  background: var(--ncp-primary-soft);
  color: var(--ncp-primary-strong);
}
h1 { margin: 0; font-size: clamp(1.25rem, 2vw, 1.58rem); font-weight: 780; letter-spacing: -.04em; line-height: 1.2; }
p { margin: 4px 0 0; color: var(--ncp-text-subtle); font-size: .84rem; line-height: 1.45; }
.workspace-stats {
  display: flex;
  flex: 0 0 auto;
  align-items: stretch;
  margin: 0;
  padding: 4px;
  border: 1px solid var(--ncp-line);
  border-radius: 14px;
  background: var(--ncp-surface-quiet);
}
.workspace-stat {
  display: grid;
  min-width: 92px;
  align-content: center;
  gap: 2px;
  padding: 5px 14px;
  border-right: 1px solid var(--ncp-line);
}
.workspace-stat:last-child { border-right: 0; }
.workspace-stat dt { color: var(--ncp-text-subtle); font-size: .72rem; font-weight: 680; white-space: nowrap; }
.workspace-stat dd { order: -1; margin: 0; font-family: 'JetBrains Mono Variable', monospace; font-size: 1.1rem; font-weight: 760; line-height: 1.2; }
.workspace-stat--success dd { color: var(--ncp-success); }
.workspace-stat--warning dd { color: var(--ncp-warning-strong); }
.workspace-toolbar {
  display: flex;
  min-width: 0;
  min-height: 54px;
  align-items: center;
  justify-content: flex-end;
  padding: 7px 10px;
  border-top: 1px solid var(--ncp-line);
  background: #f8fafc;
}
.workspace-tools,
.workspace-header__actions {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 8px;
}
.workspace-tools { flex: 1; justify-content: flex-end; }
.workspace-header__actions:empty,
.workspace-tools:empty { display: none; }
.workspace-header__actions :deep(.el-button),
.workspace-toolbar :deep(.el-button) {
  min-height: 40px;
  margin: 0;
  padding-inline: 14px;
  border-radius: 10px;
  background: #fff;
  font-weight: 680;
}
.workspace-toolbar :deep(.el-input__wrapper) {
  min-height: 40px;
  border-radius: 10px;
  background: #fff;
  box-shadow: 0 0 0 1px var(--ncp-line) inset;
}
.workspace-toolbar :deep(.source-filter),
.workspace-toolbar :deep(.state-filter) {
  min-height: 40px;
  padding: 2px;
  border: 0;
  border-radius: 10px;
  background: rgba(226, 232, 241, .78);
}
.workspace-toolbar :deep(.source-filter button),
.workspace-toolbar :deep(.state-filter button) {
  min-height: 36px;
  padding-inline: 14px;
  border-radius: 8px;
}
@media (max-width: 1280px) {
  .workspace-header__main { grid-template-columns: minmax(280px, 1fr) auto; }
  .workspace-header__actions { grid-column: 1 / -1; justify-content: flex-end; }
}
@media (max-width: 900px) {
  .workspace-header__main { grid-template-columns: minmax(0, 1fr) auto; }
  .workspace-stats { grid-column: 1 / -1; }
  .workspace-stats { width: 100%; }
  .workspace-stat { min-width: 0; flex: 1; }
  .workspace-tools { width: 100%; justify-content: stretch; }
}
@media (max-width: 640px) {
  .workspace-header__main { grid-template-columns: 1fr; min-height: 0; gap: 13px; padding: 13px; }
  .workspace-stats { grid-column: 1; }
  .workspace-toolbar { align-items: stretch; }
  .workspace-tools,
  .workspace-header__actions { width: 100%; }
  .workspace-header__actions { grid-column: 1; justify-content: stretch; }
  .workspace-header__actions :deep(.el-button) { flex: 1; }
  .workspace-stat { padding: 0 10px; }
  .workspace-stat:first-child { padding-left: 2px; }
  .workspace-stat dt { font-size: .68rem; }
  .workspace-tools { flex-wrap: wrap; }
}
</style>
