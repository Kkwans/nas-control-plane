<script setup lang="ts">
import { EditorView, basicSetup } from 'codemirror'
import { indentWithTab } from '@codemirror/commands'
import { yaml } from '@codemirror/lang-yaml'
import { keymap } from '@codemirror/view'
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'

const props = defineProps<{ modelValue: string }>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const host = ref<HTMLElement | null>(null)
let editor: EditorView | null = null

onMounted(() => {
  if (!host.value) return
  editor = new EditorView({
    parent: host.value,
    doc: props.modelValue,
    extensions: [
      basicSetup,
      yaml(),
      keymap.of([indentWithTab]),
      EditorView.lineWrapping,
      EditorView.updateListener.of((update) => {
        if (update.docChanged) emit('update:modelValue', update.state.doc.toString())
      }),
      EditorView.theme({
        '&': { height: '100%', color: 'var(--ncp-text)', backgroundColor: 'var(--ncp-surface)', fontSize: '13px' },
        '&.cm-focused': { outline: 'none' },
        '.cm-scroller': { fontFamily: 'var(--ncp-font-mono)', lineHeight: '1.72', overflow: 'auto' },
        '.cm-content': { padding: '14px 0 36px' },
        '.cm-line': { padding: '0 18px 0 10px' },
        '.cm-gutters': { color: 'var(--ncp-text-subtle)', backgroundColor: 'var(--ncp-surface-quiet)', borderRight: '1px solid var(--ncp-line)' },
        '.cm-activeLine, .cm-activeLineGutter': { backgroundColor: 'var(--ncp-primary-wash)' },
        '.cm-selectionBackground, ::selection': { backgroundColor: 'var(--ncp-primary-active) !important' },
        '.cm-cursor': { borderLeftColor: 'var(--ncp-primary)' },
        '.tok-keyword, .tok-bool, .tok-null': { color: 'var(--ncp-object-storage)' },
        '.tok-string': { color: 'var(--ncp-success-strong)' },
        '.tok-number': { color: 'var(--ncp-warning-strong)' },
        '.tok-comment': { color: 'var(--ncp-text-subtle)', fontStyle: 'italic' },
        '.tok-propertyName, .tok-labelName': { color: 'var(--ncp-info-strong)' },
      }),
    ],
  })
})

watch(() => props.modelValue, (value) => {
  if (!editor || value === editor.state.doc.toString()) return
  editor.dispatch({ changes: { from: 0, to: editor.state.doc.length, insert: value } })
})

onBeforeUnmount(() => editor?.destroy())
</script>

<template><div ref="host" class="plain-code-editor" aria-label="配置编辑器" role="group" /></template>

<style scoped>
.plain-code-editor {
  height: 100%;
  min-height: 0;
  overflow: hidden;
  border: 1px solid var(--ncp-line);
  border-radius: var(--ncp-radius-control);
  background: var(--ncp-surface);
  box-shadow: var(--ncp-shadow-control);
}

.plain-code-editor :deep(.cm-editor) {
  height: 100%;
}
</style>
