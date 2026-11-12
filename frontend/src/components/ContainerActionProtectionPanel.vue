<script setup lang="ts">
import type { ContainerActionAdmission } from '@/composables/useDockerContainerActionJobs'
import { containerActionAdmissionLabel, containerActionCanRelease } from '@/composables/useDockerContainerActionJobs'
import { dockerActionLabel } from '@/domain/dockerLifecycle'
defineProps<{ entries: ContainerActionAdmission[] }>()
defineEmits<{ check: [entry: ContainerActionAdmission]; release: [entry: ContainerActionAdmission] }>()
</script>
<template>
  <section v-if="entries.length" class="action-protection panel" aria-label="单容器操作防重复保护">
    <h2>单容器操作防重复保护</h2>
    <p>本登录会话跨路由保留保护。整页重载、多标签页及其他客户端不在保护范围内；解除保护不会取消、重试或重发操作。</p>
    <article v-for="entry in entries" :key="entry.containerId" :aria-label="`${entry.name} 操作保护`">
      <strong>{{ entry.name }} · {{ dockerActionLabel(entry.action) }} · {{ containerActionAdmissionLabel(entry) }}</strong>
      <p class="identity">容器 ID：{{ entry.containerId }}</p>
      <p v-if="entry.job" class="identity">任务 ID：{{ entry.job.id }}</p>
      <p v-if="entry.job?.errorCode">结果代码：{{ entry.job.errorCode }}</p>
      <p v-if="entry.notice" role="status">{{ entry.notice }}</p>
      <p v-if="entry.observedActive">已观察到此容器仍有活动任务，暂不能解除保护；不会自动绑定为本次提交。</p>
      <div class="protection-actions">
        <button type="button" :disabled="entry.checking || entry.phase === 'confirming' || entry.phase === 'submitting'" @click="$emit('check', entry)">{{ entry.checking ? '正在只读核对…' : '只读核对任务与状态' }}</button>
        <button type="button" :disabled="!containerActionCanRelease(entry)" @click="$emit('release', entry)">解除本页防重复保护</button>
      </div>
    </article>
  </section>
</template>
<style scoped>
.action-protection { display: grid; gap: 12px; padding: 18px; }
h2 { margin: 0; font-size: 1rem; }
p { margin: 5px 0; color: var(--ncp-text-muted); font-size: .82rem; }
article { border-top: 1px solid var(--ncp-line); padding-top: 12px; }
.identity { overflow-wrap: anywhere; font-family: var(--ncp-font-mono); }
.protection-actions { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 8px; }
button { min-height: var(--ncp-touch-target); padding: 8px 12px; border: 1px solid var(--ncp-line); border-radius: 8px; background: var(--ncp-surface); color: var(--ncp-text); cursor: pointer; }
button:disabled { opacity: .5; cursor: not-allowed; }
button:focus-visible { outline: 2px solid var(--ncp-primary); outline-offset: 2px; }
</style>
