<script setup lang="ts">
import type { Component } from 'vue'

withDefaults(defineProps<{
  title: string
  description?: string
  eyebrow?: string
  icon?: Component
  headingTag?: 'h2' | 'h3' | 'h4'
  id?: string
}>(), {
  description: undefined,
  eyebrow: undefined,
  icon: undefined,
  headingTag: 'h2',
  id: undefined,
})
</script>

<template>
  <header class="section-header">
    <div class="section-header__content">
      <span v-if="icon" class="section-header__icon" aria-hidden="true">
        <component :is="icon" :size="17" :stroke-width="1.8" />
      </span>
      <div class="section-header__copy">
        <p v-if="eyebrow" class="section-header__eyebrow">{{ eyebrow }}</p>
        <component :is="headingTag" :id="id" class="section-header__title">{{ title }}</component>
        <p v-if="description" class="section-header__description">{{ description }}</p>
      </div>
    </div>
    <div v-if="$slots.actions || $slots.default" class="section-header__actions">
      <slot name="actions" />
      <slot />
    </div>
  </header>
</template>

<style scoped>
.section-header {
  display: flex;
  min-width: 0;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--ncp-space-4);
}

.section-header__content {
  display: flex;
  min-width: 0;
  align-items: flex-start;
  gap: var(--ncp-space-3);
}

.section-header__icon {
  display: grid;
  width: 34px;
  height: 34px;
  flex: 0 0 auto;
  place-items: center;
  border: 1px solid var(--ncp-primary-border);
  border-radius: var(--ncp-radius-sm);
  background: var(--ncp-primary-soft);
  color: var(--ncp-primary-strong);
}

.section-header__copy {
  min-width: 0;
}

.section-header__eyebrow {
  margin: 0 0 var(--ncp-space-1);
  color: var(--ncp-primary-strong);
  font-family: var(--ncp-font-mono);
  font-size: .68rem;
  font-weight: 750;
  letter-spacing: .08em;
  line-height: 1.3;
  text-transform: uppercase;
}

.section-header__title {
  margin: 0;
  color: var(--ncp-text);
  font-size: 1rem;
  font-weight: 750;
  letter-spacing: -.025em;
  line-height: 1.3;
}

.section-header__description {
  max-width: 68ch;
  margin: var(--ncp-space-1) 0 0;
  color: var(--ncp-text-subtle);
  font-size: .78rem;
  line-height: 1.5;
}

.section-header__actions {
  display: flex;
  flex: 0 0 auto;
  align-items: center;
  gap: var(--ncp-space-2);
}

@media (max-width: 640px) {
  .section-header {
    align-items: stretch;
    flex-direction: column;
  }

  .section-header__actions {
    justify-content: flex-start;
  }
}
</style>
