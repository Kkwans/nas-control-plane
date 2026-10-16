<script setup lang="ts">
import { projectDeletionCanRelease, type ProjectDeletionAdmission } from '@/composables/useDockerProjectDeletionProtection'
defineProps<{ entries: ProjectDeletionAdmission[] }>()
defineEmits<{ check: [entry: ProjectDeletionAdmission]; release: [entry: ProjectDeletionAdmission] }>()
const labels = { confirming: '等待确认删除', submitting: '正在提交删除，请勿重复操作', unknown: '删除结果未知，防重复保护仍保留' }
</script>
<template>
  <section v-if="entries.length" class="deletion-protection panel" aria-label="项目删除防重复保护">
    <h2>项目删除与结果核对</h2>
    <p>本登录会话跨路由保留保护；整页刷新、其他标签页和其他客户端不在保护范围内。离开页面不会取消已提交的删除。</p>
    <article v-for="entry in entries" :key="entry.projectId" role="status">
      <strong>{{ labels[entry.phase] }}</strong>
      <p>{{ entry.name }} · 项目 ID：{{ entry.projectId }}</p>
      <p v-if="entry.notice">{{ entry.notice }}</p>
      <p>清单缺失或同名项目不能证明删除结果；项目注册状态可能仍需人工核对。解除只允许本会话再次删除，不会补偿、重发或取消原操作。</p>
      <div class="protection-actions">
        <button type="button" :disabled="entry.phase !== 'unknown' || entry.checking" @click="$emit('check', entry)">{{ entry.checking ? '正在只读核对…' : '只读核对项目清单' }}</button>
        <button type="button" :disabled="!projectDeletionCanRelease(entry)" @click="$emit('release', entry)">解除本会话删除保护</button>
      </div>
    </article>
  </section>
</template>
<style scoped>
.deletion-protection { display: grid; gap: 12px; padding: 18px; min-width: 0; }
h2 { margin: 0; font-size: 1rem; }
p { margin: 6px 0; color: var(--ncp-text-muted); font-size: .82rem; overflow-wrap: anywhere; }
article { border-top: 1px solid var(--ncp-line); padding-top: 12px; min-width: 0; }
.protection-actions { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 10px; }
button { min-height: max(44px, var(--ncp-touch-target)); padding: 8px 12px; border: 1px solid var(--ncp-line); border-radius: 8px; background: var(--ncp-surface); color: var(--ncp-text); cursor: pointer; }
button:disabled { opacity: .5; cursor: not-allowed; }
button:focus-visible { outline: 2px solid var(--ncp-primary); outline-offset: 2px; }
@media (max-width: 600px) { .protection-actions { flex-direction: column; } button { width: 100%; } }
</style>
