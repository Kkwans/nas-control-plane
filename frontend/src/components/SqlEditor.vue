<script setup lang="ts">
import { EditorView, basicSetup } from 'codemirror'
import { sql } from '@codemirror/lang-sql'
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'

const props = withDefaults(defineProps<{
  modelValue: string
  disabled?: boolean
}>(), {
  disabled: false,
})

const emit = defineEmits<{
  'update:modelValue': [value: string]
  execute: []
}>()

const editorHost = ref<HTMLElement | null>(null)
let editor: EditorView | null = null

onMounted(() => {
  if (!editorHost.value) return

  editor = new EditorView({
    parent: editorHost.value,
    doc: props.modelValue,
    extensions: [
      basicSetup,
      sql(),
      EditorView.lineWrapping,
      EditorView.editable.of(!props.disabled),
      EditorView.updateListener.of((update) => {
        if (update.docChanged) emit('update:modelValue', update.state.doc.toString())
      }),
      EditorView.domEventHandlers({
        keydown(event) {
          if (event.key === 'Enter' && (event.ctrlKey || event.metaKey)) {
            event.preventDefault()
            emit('execute')
            return true
          }
          return false
        },
      }),
      EditorView.theme({
        '&': {
          height: '100%',
          color: 'var(--ncp-text)',
          backgroundColor: 'var(--ncp-surface)',
          fontSize: '13px',
        },
        '&.cm-focused': { outline: 'none' },
        '.cm-scroller': {
          fontFamily: 'var(--ncp-font-mono)',
          lineHeight: '1.72',
          overflow: 'auto',
        },
        '.cm-content': { padding: '14px 0 28px' },
        '.cm-line': { padding: '0 18px 0 10px' },
        '.cm-gutters': {
          color: 'var(--ncp-text-subtle)',
          backgroundColor: 'var(--ncp-surface-quiet)',
          borderRight: '1px solid var(--ncp-line)',
        },
        '.cm-activeLine, .cm-activeLineGutter': { backgroundColor: 'var(--ncp-primary-wash)' },
        '.cm-selectionBackground, ::selection': { backgroundColor: 'var(--ncp-primary-active) !important' },
        '.cm-cursor': { borderLeftColor: 'var(--ncp-primary)' },
      }),
    ],
  })
  syncDisabledState(props.disabled)
})

watch(() => props.modelValue, (value) => {
  if (!editor || value === editor.state.doc.toString()) return
  editor.dispatch({
    changes: { from: 0, to: editor.state.doc.length, insert: value },
  })
})

watch(() => props.disabled, (disabled) => syncDisabledState(disabled))

function syncDisabledState(disabled: boolean) {
  const content = editorHost.value?.querySelector<HTMLElement>('.cm-content')
  content?.setAttribute('contenteditable', String(!disabled))
  editorHost.value?.setAttribute('aria-disabled', String(disabled))
}

onBeforeUnmount(() => editor?.destroy())
</script>

<template>
  <div ref="editorHost" :class="['sql-editor-host', { 'sql-editor-host--disabled': disabled }]" aria-label="SQL 编辑器" role="group" />
</template>

<style scoped>
.sql-editor-host {
  min-height: 220px;
  height: 100%;
  overflow: hidden;
  background: var(--ncp-surface);
}

.sql-editor-host :deep(.cm-editor) {
  height: 100%;
}

.sql-editor-host--disabled :deep(.cm-content) {
  cursor: default;
}

.sql-editor-host--disabled :deep(.cm-cursor) {
  display: none;
}
</style>
