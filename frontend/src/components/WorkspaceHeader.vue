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
  <section class="workspace-header" aria-labelledby="workspace-title">
    <div class="workspace-header__title">
      <span class="workspace-header__icon" aria-hidden="true"><component :is="icon" :size="21" /></span>
      <div>
        <h1 id="workspace-title">{{ title }}</h1>
        <p>{{ description }}</p>
      </div>
    </div>
    <div class="workspace-header__actions"><slot name="actions" /></div>

    <div class="workspace-command panel">
      <dl class="workspace-stats">
        <div v-for="stat in stats" :key="stat.label" :class="`workspace-stat workspace-stat--${stat.tone ?? 'default'}`">
          <dt>{{ stat.label }}</dt>
          <dd>{{ stat.value }}</dd>
        </div>
      </dl>
      <div class="workspace-tools"><slot name="tools" /></div>
    </div>
  </section>
</template>

<style scoped>
.workspace-header {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  gap: 14px 20px;
}
.workspace-header__title {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 12px;
}
.workspace-header__icon {
  display: grid;
  width: 44px;
  height: 44px;
  flex: 0 0 auto;
  place-items: center;
  border: 1px solid rgba(36, 104, 216, .13);
  border-radius: 13px;
  background: var(--ncp-primary-soft);
  color: var(--ncp-primary-strong);
}
h1 { margin: 0; font-size: clamp(1.2rem, 2vw, 1.55rem); font-weight: 780; letter-spacing: -.045em; line-height: 1.2; }
p { margin: 4px 0 0; color: var(--ncp-text-subtle); font-size: .78rem; }
.workspace-header__actions { display: flex; align-items: center; gap: 8px; }
.workspace-command {
  grid-column: 1 / -1;
  display: flex;
  min-height: 70px;
  align-items: center;
  gap: 20px;
  padding: 10px 14px;
}
.workspace-stats {
  display: flex;
  flex: 0 0 auto;
  align-items: stretch;
  margin: 0;
}
.workspace-stat {
  display: grid;
  min-width: 104px;
  align-content: center;
  gap: 1px;
  padding: 0 18px;
  border-right: 1px solid var(--ncp-line);
}
.workspace-stat:first-child { padding-left: 4px; }
.workspace-stat dt { color: var(--ncp-text-subtle); font-size: .72rem; font-weight: 650; }
.workspace-stat dd { order: -1; margin: 0; font-family: 'JetBrains Mono Variable', monospace; font-size: 1.08rem; font-weight: 750; }
.workspace-stat--success dd { color: var(--ncp-success); }
.workspace-stat--warning dd { color: var(--ncp-warning-strong); }
.workspace-tools { display: flex; min-width: 0; flex: 1; align-items: center; justify-content: flex-end; gap: 8px; }
@media (max-width: 900px) {
  .workspace-command { align-items: stretch; flex-direction: column; gap: 10px; }
  .workspace-stats { width: 100%; }
  .workspace-stat { min-width: 0; flex: 1; }
  .workspace-tools { width: 100%; justify-content: stretch; }
}
@media (max-width: 640px) {
  .workspace-header { grid-template-columns: 1fr; }
  .workspace-header__actions { display: none; }
  .workspace-command { padding: 10px; }
  .workspace-stat { padding: 0 10px; }
  .workspace-stat:first-child { padding-left: 2px; }
  .workspace-stat dt { font-size: .68rem; }
  .workspace-tools { flex-wrap: wrap; }
}
</style>
