<script setup lang="ts">
import { computed } from 'vue'
import { ShieldCheck } from '@lucide/vue'
import { projectActionCanRelease, type ProjectActionAdmission, type ProjectActionCompletion } from '@/composables/useDockerProjectActionJobs'

const props = defineProps<{ entry: ProjectActionAdmission | null; result: ProjectActionCompletion | null }>()
defineEmits<{ check: [entry: ProjectActionAdmission]; release: [entry: ProjectActionAdmission] }>()
const actions = { start: '启动', stop: '停止', restart: '重启' }
const phases = { confirming: '等待确认冻结范围', submitting: '正在提交，尚未确认接收', unknown: '接收结果未知，请勿重复提交', known: '任务已接收，仍需核对结果', resolved: '该次调用结果已确认' }
const statuses = { queued: '排队中', running: '执行中', completed: '任务执行结束，须核对已保存结果', failed: '任务失败，仍需核对', interrupted: '任务中断，部分结果可能未知' }
const outcomes = { succeeded: '该次调用回读符合目标状态', 'state-mismatch': '该次调用回读与目标状态不符', unknown: '执行结果未知，可能已执行' }
const states = { running: '运行', stopped: '停止', unknown: '未知' }
function resultSummary(result: ProjectActionCompletion) {
  const count = (outcome: 'succeeded' | 'state-mismatch' | 'unknown') => result.items.filter(item => item.outcome === outcome).length
  return `逐项回读（${result.items.length} 项）：符合目标 ${count('succeeded')} · 状态不符 ${count('state-mismatch')} · 结果未知 ${count('unknown')}`
}
const entryResult = computed(() => {
  const job = props.entry?.job
  return job && job.persistenceStatus !== 'pending' && ['completed', 'failed', 'interrupted'].includes(job.status) ? job.projectActionResult : undefined
})
</script>

<template>
  <section v-if="entry || result" class="project-action-protection panel" aria-label="项目操作防重复保护">
    <h2><ShieldCheck :size="19" aria-hidden="true" />项目操作与结果核对</h2>
    <p>本登录会话跨路由保留保护。整页刷新、其他标签页和其他客户端不在保护范围内。离开页面不会取消任务。</p>
    <div v-if="result" class="completed-result protection-status" role="status" aria-label="已确认的项目操作结果">
      <strong>{{ actions[result.action] }}：该次调用结果已保存</strong>
      <p>冻结范围：{{ result.items.length }} 个容器</p>
      <details :open="result.items.length <= 8" class="result-details">
        <summary>{{ resultSummary(result) }}</summary>
        <ul aria-label="已保存的逐项结果">
          <li v-for="item in result.items" :key="item.containerId">
            <p class="identity">容器 ID：{{ item.containerId }}</p>
            <p>{{ outcomes[item.outcome] }} · 回读状态：{{ states[item.state] }}</p>
          </li>
        </ul>
      </details>
      <p>这些结果仅确认该次调用的回读，不保证容器持续运行、持续停止或健康。当前容器清单不能证明该次操作的执行结果。</p>
    </div>
    <div v-if="entry" class="admission-status protection-status" role="status" aria-label="项目操作保护状态">
      <strong>{{ phases[entry.phase] }}</strong>
      <p>{{ actions[entry.action] }} · 冻结范围：{{ entry.containerIds.length }} 个容器</p>
      <p v-if="entry.job" class="identity">任务 ID：{{ entry.job.id }} · {{ entry.job.persistenceStatus === 'pending' ? '结果保存中，尚未确认结果' : statuses[entry.job.status] }}</p>
      <div class="protection-actions">
        <button type="button" :disabled="entry.checking || !['known', 'unknown'].includes(entry.phase)" @click="$emit('check', entry)">{{ entry.checking ? '正在只读核对…' : '只读核对任务与容器' }}</button>
        <button type="button" :disabled="!projectActionCanRelease(entry)" @click="$emit('release', entry)">确认重复执行风险并解除本会话保护</button>
      </div>
      <details :open="entry.containerIds.length <= 8" class="frozen-details">
        <summary>查看冻结的完整容器 ID（{{ entry.containerIds.length }} 个）</summary>
        <ul aria-label="冻结的容器 ID">
          <li v-for="id in entry.containerIds" :key="id" class="identity">{{ id }}</li>
        </ul>
      </details>
      <div v-if="entryResult" class="saved-results">
        <strong>已保存的逐项回读结果</strong>
        <details :open="entryResult.items.length <= 8" class="result-details">
          <summary>{{ resultSummary(entryResult) }}</summary>
          <ul aria-label="任务逐项结果">
            <li v-for="item in entryResult.items" :key="item.containerId">
              <p class="identity">容器 ID：{{ item.containerId }}</p>
              <p>{{ outcomes[item.outcome] }} · 回读状态：{{ states[item.state] }}</p>
            </li>
          </ul>
        </details>
        <p>逐项结果只说明该次调用的回读，不保证持续运行、持续停止或健康；部分符合目标不代表全部成功。</p>
      </div>
      <p v-if="entry.checked && !entry.lastReadSucceeded">任务读取失败，不能判断未接收或未执行。</p>
      <p v-if="entry.checked && !entry.inventoryChecked">容器清单未成功刷新，仍须保留保护。</p>
      <p v-if="entry.observedActive">观察到活动项目任务，不能解除保护；此观察不会自动认领未知提交。</p>
      <p>已接收不代表成功。当前容器清单、空清单或状态变化都不能证明先前操作未执行。</p>
      <p>解除保护存在重复执行风险，仅允许本会话再次提交，不会取消、重试或重发任务。解除前须明确确认风险。</p>
    </div>
  </section>
</template>

<style scoped>
.project-action-protection { display: grid; gap: 12px; padding: 18px; min-width: 0; }
h2 { display: flex; align-items: center; gap: 8px; margin: 0; font-size: 1rem; }
p, li { color: var(--ncp-text-muted); font-size: .82rem; overflow-wrap: anywhere; }
p { margin: 6px 0; }
ul { margin: 8px 0; padding-left: 20px; min-width: 0; }
li + li { margin-top: 8px; }
.protection-status { border-top: 1px solid var(--ncp-line); padding-top: 12px; min-width: 0; }
.identity { overflow-wrap: anywhere; font-family: var(--ncp-font-mono); }
.protection-actions { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 10px; }
summary { min-height: max(44px, var(--ncp-touch-target)); align-content: center; padding: 8px 0; cursor: pointer; overflow-wrap: anywhere; color: var(--ncp-text); font-size: .82rem; }
summary:focus-visible { outline: 2px solid var(--ncp-primary); outline-offset: 2px; }
button { min-height: max(44px, var(--ncp-touch-target)); min-width: 0; white-space: normal; overflow-wrap: anywhere; padding: 8px 12px; border: 1px solid var(--ncp-line); border-radius: 8px; background: var(--ncp-surface); color: var(--ncp-text); cursor: pointer; }
button:disabled { opacity: .5; cursor: not-allowed; }
button:focus-visible { outline: 2px solid var(--ncp-primary); outline-offset: 2px; }
@media (max-width: 600px) { .protection-actions { flex-direction: column; } button { width: 100%; } }
</style>
