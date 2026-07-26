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
    <div v-if="$slots.filters || $slots.tools" class="workspace-toolbar">
      <div class="workspace-filters">
        <slot name="filters" />
      </div>
      <div class="workspace-tools">
        <slot name="tools" />
      </div>
    </div>
  </section>
</template>

<style scoped>
.workspace-header {
  overflow: hidden;
  border-color: rgba(203,214,228,.86);
  background:
    radial-gradient(circle at 82% -40%, rgba(52,116,212,.075), transparent 34%),
    var(--ncp-surface);
}
.workspace-header__main {
  display: flex;
  min-height: 92px;
  align-items: center;
  gap: 22px;
  padding: 18px 20px;
}
.workspace-header__title {
  display: flex;
  flex: 1 1 340px;
  min-width: 0;
  align-items: center;
  gap: 12px;
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
h1 { margin: 0; font-size: clamp(1.45rem, 1.8vw, 1.68rem); font-weight: 760; letter-spacing: -.035em; line-height: 1.18; }
p { margin: 5px 0 0; color: var(--ncp-text-muted); font-size: .9rem; line-height: 1.45; }
.workspace-stats {
  display: flex;
  flex: 0 0 auto;
  align-items: stretch;
  margin: 0;
  padding: 0;
  border: 0;
  background: transparent;
}
.workspace-stat {
  display: grid;
  min-width: 96px;
  align-content: center;
  gap: 2px;
  padding: 3px 16px;
  border-right: 1px solid var(--ncp-line);
}
.workspace-stat:last-child { border-right: 0; }
.workspace-stat dt { color: var(--ncp-text-subtle); font-size: .8rem; font-weight: 650; white-space: nowrap; }
.workspace-stat dd { order: -1; margin: 0; font-family: var(--ncp-font-latin); font-size: 1.22rem; font-weight: 760; line-height: 1.2; font-variant-numeric:tabular-nums; }
.workspace-stat--success dd { color: var(--ncp-success); }
.workspace-stat--warning dd { color: var(--ncp-warning-strong); }
.workspace-toolbar {
  display: flex;
  min-width: 0;
  min-height: 58px;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 8px 14px;
  border-top: 1px solid var(--ncp-line);
  background: rgba(248,250,253,.72);
}
.workspace-filters,
.workspace-tools,
.workspace-header__actions {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 8px;
}
.workspace-filters { justify-content: flex-start; }
.workspace-tools { margin-left: auto; justify-content: flex-end; }
.workspace-header__actions:empty,
.workspace-filters:empty,
.workspace-tools:empty { display: none; }
.workspace-header__actions :deep(.el-button),
.workspace-toolbar :deep(.el-button) {
  min-height: var(--ncp-control-height);
  margin: 0;
  padding-inline: 14px;
  border-radius: 10px;
  background: #fff;
  font-weight: 680;
}
.workspace-toolbar :deep(.el-input__wrapper) {
  min-height: var(--ncp-control-height);
  border-radius: 10px;
  background: #fff;
  box-shadow: 0 0 0 1px var(--ncp-line) inset;
}
.workspace-toolbar :deep(.source-filter),
.workspace-toolbar :deep(.state-filter) {
  min-height: var(--ncp-control-height);
  padding: 2px;
  border: 0;
  border-radius: 10px;
  background: var(--ncp-surface-quiet);
}
.workspace-toolbar :deep(.source-filter button),
.workspace-toolbar :deep(.state-filter button) {
  min-height: 38px;
  padding-inline: 14px;
  border-radius: 8px;
}
@media (max-width: 1040px) {
  .workspace-header__main { flex-wrap: wrap; }
  .workspace-header__title { flex-basis: calc(100% - 160px); }
  .workspace-stats { order: 3; }
  .workspace-stats { width: 100%; }
  .workspace-stat { min-width: 0; flex: 1; }
  .workspace-tools { min-width: min(360px, 45vw); }
}
@media (max-width: 900px) {
  .workspace-toolbar { flex-wrap: wrap; }
  .workspace-filters,
  .workspace-tools { width: 100%; }
  .workspace-tools { min-width: 0; margin-left: 0; }
}
@media (max-width: 640px) {
  .workspace-header__main { display: grid; grid-template-columns: 1fr; min-height: 0; gap: 12px; padding: 13px; }
  .workspace-header__title { width: 100%; }
  .workspace-toolbar { align-items: stretch; flex-direction:column; gap:8px; }
  .workspace-filters,
  .workspace-tools,
  .workspace-header__actions { width: 100%; }
  .workspace-filters { align-items: stretch; flex-direction: column; }
  .workspace-header__actions { justify-content: stretch; }
  .workspace-header__actions :deep(.el-button) { flex: 1; }
  .workspace-stat { padding: 0 10px; }
  .workspace-stat:first-child { padding-left: 2px; }
  .workspace-stat dt { font-size: .72rem; }
  .workspace-tools { min-width:0; margin-left:0; flex-wrap: wrap; }
}
</style>
