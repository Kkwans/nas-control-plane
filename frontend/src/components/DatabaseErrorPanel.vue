<script setup lang="ts">
import { CircleAlert } from '@lucide/vue'

export interface DatabaseErrorState {
  code: string
  message: string
  nextStep: string
}

defineProps<{
  title: string
  error: DatabaseErrorState
}>()
</script>

<template>
  <div class="database-error" role="alert">
    <CircleAlert :size="18" aria-hidden="true" />
    <div class="database-error__body">
      <strong>{{ title }}</strong>
      <span>{{ error.message }}</span>
      <small>下一步：{{ error.nextStep }}</small>
    </div>
    <code>代码 {{ error.code }}</code>
  </div>
</template>

<style scoped>
.database-error {
  display: flex;
  min-width: 0;
  align-items: flex-start;
  gap: 10px;
  padding: 12px 14px;
  border: 1px solid var(--ncp-danger-border);
  border-radius: var(--ncp-radius-md);
  background: var(--ncp-danger-soft);
  color: var(--ncp-danger-strong);
}

.database-error > svg {
  flex: 0 0 auto;
  margin-top: 1px;
}

.database-error__body {
  display: grid;
  min-width: 0;
  gap: 2px;
}

.database-error__body small {
  color: var(--ncp-danger-strong);
  font-size: .72rem;
  line-height: 1.4;
}

.database-error strong {
  font-size: .8rem;
}

.database-error span {
  color: var(--ncp-danger-strong);
  font-size: .78rem;
  line-height: 1.45;
}

.database-error code {
  margin-left: auto;
  padding: 2px 6px;
  border: 1px solid var(--ncp-danger-border);
  border-radius: var(--ncp-radius-xs);
  color: var(--ncp-danger-strong);
  font-family: var(--ncp-font-mono);
  font-size: .67rem;
  white-space: nowrap;
}

@media (max-width: 500px) {
  .database-error {
    flex-wrap: wrap;
  }

  .database-error code {
    width: 100%;
    margin-left: 28px;
  }
}
</style>
