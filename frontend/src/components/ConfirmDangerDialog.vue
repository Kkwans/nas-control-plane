<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { AlertTriangle } from '@lucide/vue'
import { ElButton, ElDialog, ElInput } from 'element-plus'

const props = withDefaults(defineProps<{
  modelValue: boolean
  title: string
  description?: string
  impact?: string[]
  retained?: string[]
  actionLabel?: string
  cancelLabel?: string
  confirmationTarget?: string
  confirmationLabel?: string
  busy?: boolean
}>(), {
  description: '',
  impact: () => [],
  retained: () => [],
  actionLabel: '确认操作',
  cancelLabel: '取消',
  confirmationTarget: '',
  confirmationLabel: '输入目标名称确认',
  busy: false,
})

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  confirm: []
}>()

const confirmation = ref('')
const canConfirm = computed(() => !props.confirmationTarget || confirmation.value.trim() === props.confirmationTarget)

watch(() => props.modelValue, (open) => {
  if (!open) confirmation.value = ''
})

function close() {
  if (!props.busy) emit('update:modelValue', false)
}

function confirm() {
  if (props.busy || !canConfirm.value) return
  emit('confirm')
}
</script>

<template>
  <ElDialog
    :model-value="modelValue"
    :title="title"
    width="min(520px, calc(100vw - 28px))"
    destroy-on-close
    class="confirm-danger-dialog"
    @update:model-value="emit('update:modelValue', $event)"
    @close="close"
  >
    <div class="confirm-danger-dialog__body">
      <div class="confirm-danger-dialog__notice">
        <AlertTriangle :size="20" aria-hidden="true" />
        <p v-if="description">{{ description }}</p>
        <p v-else>这是一个可能影响数据或运行状态的操作，请确认影响范围后继续。</p>
      </div>
      <div v-if="impact.length" class="confirm-danger-dialog__section">
        <strong>将执行</strong>
        <ul><li v-for="item in impact" :key="item">{{ item }}</li></ul>
      </div>
      <div v-if="retained.length" class="confirm-danger-dialog__section confirm-danger-dialog__section--retained">
        <strong>不会影响</strong>
        <ul><li v-for="item in retained" :key="item">{{ item }}</li></ul>
      </div>
      <label v-if="confirmationTarget" class="confirm-danger-dialog__confirmation">
        <span>{{ confirmationLabel }}：<code>{{ confirmationTarget }}</code></span>
        <ElInput v-model="confirmation" :placeholder="confirmationTarget" autocomplete="off" @keyup.enter="confirm" />
      </label>
    </div>
    <template #footer>
      <ElButton :disabled="busy" @click="close">{{ cancelLabel }}</ElButton>
      <ElButton type="danger" :loading="busy" :disabled="!canConfirm" @click="confirm">{{ actionLabel }}</ElButton>
    </template>
  </ElDialog>
</template>

<style scoped>
.confirm-danger-dialog__body { display: grid; gap: 14px; color: var(--ncp-text-muted); }
.confirm-danger-dialog__notice { display: flex; align-items: flex-start; gap: 10px; padding: 12px 13px; border: 1px solid var(--ncp-warning-border); border-radius: 10px; background: var(--ncp-warning-soft); color: var(--ncp-warning-strong); }
.confirm-danger-dialog__notice svg { flex: 0 0 auto; margin-top: 1px; }
.confirm-danger-dialog__notice p { margin: 0; font-size: .82rem; line-height: 1.5; }
.confirm-danger-dialog__section { display: grid; gap: 6px; padding: 11px 13px; border: 1px solid var(--ncp-danger-border); border-radius: 9px; background: var(--ncp-danger-soft); color: var(--ncp-danger-strong); }
.confirm-danger-dialog__section--retained { border-color: var(--ncp-line); background: var(--ncp-surface-quiet); color: var(--ncp-text-muted); }
.confirm-danger-dialog__section strong { font-size: .78rem; }
.confirm-danger-dialog__section ul { display: grid; gap: 3px; margin: 0; padding-left: 18px; font-size: .78rem; line-height: 1.45; }
.confirm-danger-dialog__confirmation { display: grid; gap: 7px; color: var(--ncp-text); font-size: .78rem; font-weight: 700; }
.confirm-danger-dialog__confirmation code { padding: 2px 5px; border-radius: 4px; background: var(--ncp-surface-quiet); font-family: var(--ncp-font-mono); font-size: .74rem; }
</style>
