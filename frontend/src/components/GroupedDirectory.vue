<script setup lang="ts">
export interface GroupedDirectoryGroup {
  key: string
  title: string
  count: number
}

defineProps<{
  groups: readonly GroupedDirectoryGroup[]
  label: string
}>()
</script>

<template>
  <div class="grouped-directory panel" :aria-label="label">
    <section v-for="group in groups" :key="group.key" class="grouped-directory__group">
      <header class="grouped-directory__header">
        <div class="grouped-directory__title">
          <h3>{{ group.title }}</h3>
          <span :aria-label="`${group.count} 项`">{{ group.count }}</span>
        </div>
        <div v-if="$slots.actions" class="grouped-directory__actions">
          <slot name="actions" :group="group" />
        </div>
      </header>
      <div class="grouped-directory__items">
        <slot name="items" :group="group" />
      </div>
    </section>
  </div>
</template>

<style scoped>
.grouped-directory {
  overflow: hidden;
}

.grouped-directory__group + .grouped-directory__group {
  border-top: 1px solid var(--ncp-line-strong);
}

.grouped-directory__header {
  display: flex;
  min-height: 48px;
  align-items: center;
  justify-content: space-between;
  gap: var(--ncp-space-3);
  padding: 0 var(--ncp-space-4);
  border-bottom: 1px solid var(--ncp-line);
  background: linear-gradient(180deg, var(--ncp-surface), var(--ncp-surface-quiet));
}

.grouped-directory__title,
.grouped-directory__actions {
  display: flex;
  min-width: 0;
  align-items: center;
}

.grouped-directory__title {
  gap: var(--ncp-space-2);
}

.grouped-directory__title h3 {
  overflow: hidden;
  margin: 0;
  color: var(--ncp-text);
  font-size: .88rem;
  font-weight: 760;
  letter-spacing: -.015em;
  line-height: 1.35;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.grouped-directory__title > span {
  display: grid;
  min-width: 24px;
  height: 24px;
  place-items: center;
  padding: 0 6px;
  border: 1px solid var(--ncp-line);
  border-radius: var(--ncp-radius-xs);
  background: var(--ncp-surface);
  color: var(--ncp-text-subtle);
  font-family: var(--ncp-font-mono);
  font-size: .7rem;
  line-height: 1;
}

.grouped-directory__actions {
  flex: 0 0 auto;
  gap: var(--ncp-space-2);
}

@media (max-width: 680px) {
  .grouped-directory {
    overflow: visible;
    border: 0;
    background: transparent;
    box-shadow: none;
  }

  .grouped-directory__group + .grouped-directory__group {
    margin-top: var(--ncp-space-3);
    border-top: 0;
  }

  .grouped-directory__header {
    min-height: 50px;
    padding-inline: var(--ncp-space-3);
    border: 1px solid var(--ncp-line);
    border-radius: var(--ncp-radius-control);
    background: var(--ncp-surface-quiet);
  }
}
</style>
