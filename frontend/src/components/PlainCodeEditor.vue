<script setup lang="ts">
import { EditorView, basicSetup } from 'codemirror'
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
      EditorView.lineWrapping,
      EditorView.updateListener.of((update) => {
        if (update.docChanged) emit('update:modelValue', update.state.doc.toString())
      }),
      EditorView.theme({
        '&': { height: '100%', color: '#172033', backgroundColor: '#fff', fontSize: '13px' },
        '&.cm-focused': { outline: 'none' },
        '.cm-scroller': { fontFamily: '"JetBrains Mono Variable", ui-monospace, monospace', lineHeight: '1.72' },
        '.cm-content': { padding: '14px 0 36px' },
        '.cm-line': { padding: '0 18px 0 10px' },
        '.cm-gutters': { color: '#9aa6ba', backgroundColor: '#f8fafc', borderRight: '1px solid #e8edf5' },
        '.cm-activeLine, .cm-activeLineGutter': { backgroundColor: '#f2f6ff' },
        '.cm-selectionBackground, ::selection': { backgroundColor: '#dce9ff !important' },
        '.cm-cursor': { borderLeftColor: '#2468d8' },
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

<template><div ref="host" class="plain-code-editor" /></template>

<style scoped>
.plain-code-editor{height:100%;min-height:420px;overflow:hidden}.plain-code-editor :deep(.cm-editor){height:100%}
</style>
