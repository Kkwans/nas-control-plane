<script setup lang="ts">
import { ShieldCheck } from '@lucide/vue'
import { containerCreateCanRelease, type ContainerCreateAdmission } from '@/composables/useDockerContainerCreateJobs'
defineProps<{ entry: ContainerCreateAdmission | null; result: { containerId: string; created: true; started: boolean } | null }>()
defineEmits<{ check: [entry: ContainerCreateAdmission]; release: [entry: ContainerCreateAdmission] }>()
const statusLabels = { queued: '排队中', running: '执行中', completed: '创建已完成', failed: '任务失败，仍需核对', interrupted: '任务中断，创建结果未知' }
const phaseLabels = { confirming: '等待确认配置', submitting: '正在提交，尚未确认受理', known: '任务已受理，尚未确认创建完成', unknown: '受理结果未知，容器可能已经创建', resolved: '创建结果已确认' }
</script>
<template>
  <section v-if="entry || result" class="create-protection panel" aria-label="容器创建防重复保护">
    <h2><ShieldCheck :size="19" />容器创建与结果核对</h2>
    <p>本登录会话跨路由保留保护。整页刷新、其他标签页和其他客户端不在保护范围内。关闭抽屉或离开页面不会取消任务。</p>
    <article v-if="result" class="creation-result" role="status" aria-label="已确认的创建结果">
      <strong>{{ result.started ? '创建调用已确认创建并启动' : '创建调用已确认创建' }}</strong>
      <p class="identity">容器 ID：{{ result.containerId }}</p>
      <p>结果仅确认该次创建调用，不保证容器持续运行。下方清单为独立只读观察；同名容器或清单存在不能证明其他创建尝试的结果。</p>
    </article>
    <article v-if="entry" role="status">
      <strong>{{ phaseLabels[entry.phase] }}</strong>
      <p>{{ entry.runContainer ? '创建并启动' : '仅创建' }} · 提交保留时间：{{ new Date(entry.reservedAt).toLocaleString('zh-CN', { hour12: false }) }}</p>
      <p v-if="entry.job" class="identity">任务 ID：{{ entry.job.id }} · 状态：{{ statusLabels[entry.job.status] }}</p>
      <p v-if="entry.job?.persistenceStatus === 'pending'">结果保存中，仍须保留保护。</p>
      <p v-if="entry.notice">{{ entry.notice }}</p>
      <p v-if="entry.observedActive">观察到活动创建任务，不能解除保护；此任务不会自动绑定为本次提交。</p>
      <p>受理不是成功。核对失败、空清单或同名容器都不能证明本次创建未发生。解除仅允许本会话再次提交，不会取消、重试或重发任务。</p>
      <div class="protection-actions">
        <button type="button" :disabled="entry.checking || entry.phase === 'confirming' || entry.phase === 'submitting'" @click="$emit('check', entry)">{{ entry.checking ? '正在只读核对…' : '只读核对任务与容器' }}</button>
        <button type="button" :disabled="!containerCreateCanRelease(entry)" @click="$emit('release', entry)">解除本会话防重复保护</button>
      </div>
    </article>
  </section>
</template>
<style scoped>
.create-protection { display: grid; gap: 12px; padding: 18px; min-width: 0; }
h2 { display: flex; align-items: center; gap: 8px; margin: 0; font-size: 1rem; }
p { margin: 6px 0; color: var(--ncp-text-muted); font-size: .82rem; overflow-wrap: anywhere; }
article { border-top: 1px solid var(--ncp-line); padding-top: 12px; min-width: 0; }
.identity { overflow-wrap: anywhere; font-family: var(--ncp-font-mono); }
.protection-actions { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 10px; }
button { min-height: max(44px, var(--ncp-touch-target)); padding: 8px 12px; border: 1px solid var(--ncp-line); border-radius: 8px; background: var(--ncp-surface); color: var(--ncp-text); cursor: pointer; }
button:disabled { opacity: .5; cursor: not-allowed; }
button:focus-visible { outline: 2px solid var(--ncp-primary); outline-offset: 2px; }
@media (max-width: 600px) { .protection-actions { flex-direction: column; } button { width: 100%; } }
</style>
