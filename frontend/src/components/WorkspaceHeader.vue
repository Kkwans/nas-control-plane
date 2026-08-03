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
        <span class="workspace-header__icon" aria-hidden="true">
          <component :is="icon" :size="20" :stroke-width="1.8" />
        </span>
        <div class="workspace-header__copy">
          <h1 id="workspace-title">{{ title }}</h1>
          <p>{{ description }}</p>
        </div>
      </div>

      <dl v-if="stats.length" class="workspace-stats" aria-label="页面摘要">
        <div
          v-for="stat in stats"
          :key="stat.label"
          :class="`workspace-stat workspace-stat--${stat.tone ?? 'default'}`"
        >
          <dt>{{ stat.label }}</dt>
          <dd class="tabular-number">{{ stat.value }}</dd>
        </div>
      </dl>

      <div class="workspace-header__actions">
        <slot name="actions" />
      </div>
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
  width: 100%;
  max-width: 100%;
  overflow: hidden;
  border-color: var(--ncp-line-strong);
  background:
    linear-gradient(135deg, var(--ncp-surface) 58%, var(--ncp-primary-wash)),
    var(--ncp-surface);
}

.workspace-header__main {
  display: grid;
  min-height: 88px;
  grid-template-columns: minmax(0, 1fr) auto auto;
  align-items: center;
  gap: var(--ncp-space-5);
  padding: var(--ncp-space-4) var(--ncp-space-5);
}

.workspace-header__title {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: var(--ncp-space-3);
}

.workspace-header__icon {
  display: grid;
  width: 44px;
  height: 44px;
  flex: 0 0 auto;
  place-items: center;
  border: 1px solid var(--ncp-primary-border);
  border-radius: var(--ncp-radius-control);
  background: var(--ncp-primary-soft);
  color: var(--ncp-primary-strong);
}

.workspace-header__copy {
  min-width: 0;
}

h1 {
  margin: 0;
  overflow: hidden;
  color: var(--ncp-text);
  font-size: clamp(1.35rem, 1.8vw, 1.62rem);
  font-weight: 760;
  letter-spacing: -.04em;
  line-height: 1.18;
  text-overflow: ellipsis;
  white-space: nowrap;
}

p {
  margin: var(--ncp-space-1) 0 0;
  overflow: hidden;
  color: var(--ncp-text-muted);
  font-size: .86rem;
  line-height: 1.45;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.workspace-stats {
  display: flex;
  min-width: 0;
  flex: 0 0 auto;
  align-items: stretch;
  margin: 0;
  padding: 0;
  border: 0;
  background: transparent;
}

.workspace-stat {
  display: grid;
  min-width: 92px;
  align-content: center;
  gap: 2px;
  padding: var(--ncp-space-1) var(--ncp-space-4);
  border-left: 1px solid var(--ncp-line);
}

.workspace-stat:first-child {
  border-left: 0;
}

.workspace-stat dt {
  color: var(--ncp-text-subtle);
  font-size: .76rem;
  font-weight: 650;
  line-height: 1.3;
  white-space: nowrap;
}

.workspace-stat dd {
  order: -1;
  margin: 0;
  color: var(--ncp-text);
  font-family: var(--ncp-font-latin);
  font-size: 1.18rem;
  font-weight: 760;
  line-height: 1.2;
}

.workspace-stat--success dd {
  color: var(--ncp-success);
}

.workspace-stat--warning dd {
  color: var(--ncp-warning-strong);
}

.workspace-header__actions,
.workspace-filters,
.workspace-tools {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: var(--ncp-space-2);
}

.workspace-header__actions {
  justify-content: flex-end;
}

.workspace-header__actions:empty,
.workspace-filters:empty,
.workspace-tools:empty {
  display: none;
}

.workspace-toolbar {
  display: flex;
  min-width: 0;
  min-height: var(--ncp-toolbar-height);
  align-items: center;
  justify-content: space-between;
  gap: var(--ncp-space-4);
  padding: var(--ncp-space-2) var(--ncp-space-4);
  border-top: 1px solid var(--ncp-line);
  background: var(--ncp-surface-quiet);
}

.workspace-filters {
  justify-content: flex-start;
}

.workspace-tools {
  margin-left: auto;
  justify-content: flex-end;
}

.workspace-header__actions :deep(.el-button),
.workspace-toolbar :deep(.el-button) {
  min-height: var(--ncp-control-height);
  margin: 0;
  padding-inline: var(--ncp-space-4);
  border-radius: var(--ncp-radius-control);
  font-weight: 680;
}

.workspace-header__actions :deep(.el-button:not(.el-button--primary)),
.workspace-toolbar :deep(.el-button:not(.el-button--primary)) {
  border-color: var(--ncp-control-border);
  background: var(--ncp-control-surface);
  color: var(--ncp-text-muted);
}

.workspace-header__actions :deep(.el-button:not(.el-button--primary):hover),
.workspace-toolbar :deep(.el-button:not(.el-button--primary):hover) {
  border-color: var(--ncp-control-border-hover);
  background: var(--ncp-control-hover);
  color: var(--ncp-text);
}

.workspace-header__actions :deep(.el-button--primary),
.workspace-toolbar :deep(.el-button--primary) {
  border-color: var(--ncp-primary);
  background: var(--ncp-primary);
  color: var(--ncp-on-primary);
  box-shadow: var(--ncp-shadow-control);
}

.workspace-header__actions :deep(.el-button--primary:hover),
.workspace-toolbar :deep(.el-button--primary:hover) {
  border-color: var(--ncp-primary-strong);
  background: var(--ncp-primary-strong);
  color: var(--ncp-on-primary);
}

.workspace-header__actions :deep(.el-button:disabled),
.workspace-toolbar :deep(.el-button:disabled),
.workspace-header__actions :deep(.el-button.is-disabled),
.workspace-toolbar :deep(.el-button.is-disabled) {
  background: var(--ncp-control-disabled);
  border-color: var(--ncp-line);
  color: var(--ncp-text-disabled);
  box-shadow: none;
}

.workspace-toolbar :deep(.el-input__wrapper),
.workspace-toolbar :deep(.el-select__wrapper) {
  min-height: var(--ncp-control-height);
  border-radius: var(--ncp-radius-control);
}

.workspace-toolbar :deep(.source-filter),
.workspace-toolbar :deep(.state-filter) {
  min-height: var(--ncp-control-height);
  padding: 2px;
  border: 0;
  border-radius: var(--ncp-radius-control);
  background: var(--ncp-surface-sunken);
}

.workspace-toolbar :deep(.source-filter button),
.workspace-toolbar :deep(.state-filter button) {
  min-height: calc(var(--ncp-control-height) - 4px);
  padding-inline: var(--ncp-space-3);
  border-radius: var(--ncp-radius-sm);
}

@media (max-width: 1040px) {
  .workspace-header__main {
    grid-template-columns: minmax(0, 1fr) auto;
  }

  .workspace-stats {
    width: 100%;
    grid-column: 1 / -1;
  }

  .workspace-stat {
    min-width: 0;
    flex: 1;
  }
}

@media (max-width: 900px) {
  .workspace-toolbar {
    align-items: stretch;
    flex-wrap: wrap;
  }

  .workspace-filters,
  .workspace-tools {
    width: 100%;
  }

  .workspace-tools {
    margin-left: 0;
  }
}

@media (max-width: 640px) {
  .workspace-header__main {
    display: grid;
    min-height: 0;
    grid-template-columns: 1fr;
    gap: var(--ncp-space-3);
    padding: var(--ncp-space-3);
  }

  .workspace-header__title {
    width: 100%;
  }

  h1,
  p {
    white-space: normal;
  }

  h1 {
    font-size: 1.3rem;
  }

  p {
    display: -webkit-box;
    -webkit-box-orient: vertical;
    -webkit-line-clamp: 2;
  }

  .workspace-header__actions {
    width: 100%;
    justify-content: stretch;
  }

  .workspace-header__actions :deep(.el-button) {
    flex: 1;
  }

  .workspace-stats {
    width: 100%;
  }

  .workspace-stat {
    padding-inline: var(--ncp-space-2);
  }

  .workspace-stat:first-child {
    padding-left: 0;
  }

  .workspace-stat dt {
    overflow: hidden;
    font-size: .7rem;
    text-overflow: ellipsis;
  }

  .workspace-stat dd {
    font-size: 1.08rem;
  }

  .workspace-toolbar {
    flex-direction: column;
    gap: var(--ncp-space-2);
    padding: var(--ncp-space-3);
  }

  .workspace-filters,
  .workspace-tools {
    align-items: stretch;
    flex-wrap: wrap;
  }

  .workspace-filters :deep(.el-select),
  .workspace-filters :deep(.el-input),
  .workspace-tools :deep(.el-select),
  .workspace-tools :deep(.el-input) {
    min-width: 0;
    flex: 1 1 160px;
  }
}

@media (max-width: 390px) {
  .workspace-header__icon {
    width: 40px;
    height: 40px;
  }

  .workspace-stat dt {
    font-size: .68rem;
  }
}
</style>
