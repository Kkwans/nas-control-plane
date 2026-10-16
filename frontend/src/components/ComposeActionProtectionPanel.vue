<script setup lang="ts">
import { computed } from 'vue'
import { ShieldCheck } from '@lucide/vue'
import { composeActionCanRelease, type ComposeActionAdmission, type ComposeActionCompletion } from '@/composables/useComposeActionJobs'

const props = defineProps<{ entry: ComposeActionAdmission | null; result: ComposeActionCompletion | null }>()
defineEmits<{ check: [entry: ComposeActionAdmission]; release: [entry: ComposeActionAdmission] }>()
const actions = { start: '启动', stop: '停止', restart: '重启' }
const phases = { confirming: '等待确认冻结意图', submitting: '正在提交，尚未确认接收', unknown: '接收结果未知，请勿重复提交', known: '任务已接收，仍需核对结果', resolved: '该次调用结果已确认' }
const statuses = { queued: '排队中', running: '执行中', completed: '任务执行结束，须核对已保存结果', failed: '任务失败，防重复保护仍保留', interrupted: '任务中断，执行结果可能未知，防重复保护仍保留' }
const states = { running: '运行', stopped: '停止' }
const resultLimits = '仅证明命令返回和一次状态观察，不保证健康、持续状态、配置服务完整性或重启次数。没有逐服务结果，当前清单不能证明该次操作的执行结果。'
const entryResult = computed(() => {
  const job = props.entry?.job
  return job?.status === 'completed' && job.persistenceStatus !== 'pending' ? job.composeActionResult : undefined
})
const uncertain = computed(() => props.entry?.phase === 'unknown' || (props.entry?.job?.persistenceStatus !== 'pending' && ['failed', 'interrupted'].includes(props.entry?.job?.status ?? '')))
</script>

<template>
  <section v-if="entry || result" class="compose-action-protection panel" aria-label="Compose 操作防重复保护">
    <h2><ShieldCheck :size="19" aria-hidden="true" />Compose 操作与结果核对</h2>
    <p>本登录会话跨路由保留保护。整页刷新、其他标签页和其他客户端不在保护范围内。离开页面不会取消任务。</p>
    <div v-if="result" class="completed-result protection-status" role="status" aria-label="已确认的 Compose 操作结果">
      <strong>{{ actions[result.action] }}：该次调用聚合结果已保存</strong>
      <p class="identity">项目 ID：{{ result.projectId }}</p>
      <p>该次观察状态：{{ states[result.state] }} · 观察到的服务数：{{ result.serviceCount }}</p>
      <p>{{ resultLimits }}</p>
    </div>
    <div v-if="entry" class="admission-status protection-status" role="status" aria-label="Compose 操作保护状态">
      <strong>{{ phases[entry.phase] }}</strong>
      <p>冻结操作：{{ actions[entry.action] }}</p>
      <p v-if="entry.job" class="identity">任务 ID：{{ entry.job.id }} · {{ entry.job.persistenceStatus === 'pending' ? '结果保存中，尚未确认结果' : statuses[entry.job.status] }}</p>
      <div class="protection-actions">
        <button type="button" :disabled="entry.checking || !['known', 'unknown'].includes(entry.phase)" @click="$emit('check', entry)">{{ entry.checking ? '正在只读核对…' : '只读核对任务与容器清单' }}</button>
        <button type="button" :disabled="!composeActionCanRelease(entry)" @click="$emit('release', entry)">确认重复执行风险并解除本会话保护</button>
      </div>
      <p>以下是本会话冻结的提交意图，不是持久化执行证据。实际目标由工作目录和有序配置文件决定；项目 ID 仅用于关联，不是 Compose -p 参数。</p>
      <details :open="entry.configFiles.length <= 8" class="frozen-details">
        <summary>查看冻结项目与有序配置路径（{{ entry.configFiles.length }} 个文件）</summary>
        <div class="frozen-intent" role="region" aria-label="冻结的 Compose 提交意图" tabindex="0">
          <p class="identity">项目 ID：{{ entry.projectId }}</p>
          <p class="identity">显示名称：{{ entry.displayName }}</p>
          <p>操作：{{ actions[entry.action] }}</p>
          <p class="identity">工作目录：{{ entry.workingDirectory }}</p>
          <ol aria-label="按提交顺序冻结的配置文件">
            <li v-for="(path, index) in entry.configFiles" :key="index" class="identity">{{ path }}</li>
          </ol>
        </div>
      </details>
      <div v-if="entryResult" class="saved-results">
        <strong>已保存的聚合观察结果</strong>
        <p>该次观察状态：{{ states[entryResult.state] }} · 观察到的服务数：{{ entryResult.serviceCount }}</p>
        <p>{{ resultLimits }}</p>
      </div>
      <p v-if="entry.checked && !entry.lastReadSucceeded">任务读取失败，不能判断未接收或未执行。</p>
      <p v-if="entry.checked && !entry.inventoryChecked">容器清单未成功刷新，仍须保留保护。</p>
      <p v-if="entry.observedActive">观察到活动 Compose 任务，不能解除保护；此观察不会自动认领未知提交。</p>
      <p>已接收不代表成功。当前容器清单、空清单或状态变化都不能证明先前操作未执行。</p>
      <p>解除保护存在重复执行风险，仅允许本会话再次提交，不会取消、重试或重发任务。解除前须明确确认风险。</p>
    </div>
    <div v-if="uncertain" class="uncertainty" role="alert">接收或执行尚有不确定性，防重复保护仍保留。请先只读核对任务与容器清单，再判断是否接受重复执行风险。</div>
  </section>
</template>

<style scoped>
.compose-action-protection { display: grid; gap: 12px; padding: 18px; min-width: 0; }
h2 { display: flex; align-items: center; gap: 8px; margin: 0; font-size: 1rem; }
p, li, .uncertainty { color: var(--ncp-text-muted); font-size: .82rem; overflow-wrap: anywhere; }
p { margin: 6px 0; }
ol { margin: 8px 0; padding-left: 24px; min-width: 0; }
li + li { margin-top: 8px; }
.protection-status { border-top: 1px solid var(--ncp-line); padding-top: 12px; min-width: 0; }
.identity { overflow-wrap: anywhere; font-family: var(--ncp-font-mono); }
.protection-actions { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 10px; }
.frozen-details { min-width: 0; }
.frozen-intent { max-height: 20rem; overflow: auto; min-width: 0; padding: 4px; }
summary { min-height: max(44px, var(--ncp-touch-target)); align-content: center; padding: 8px 0; cursor: pointer; overflow-wrap: anywhere; color: var(--ncp-text); font-size: .82rem; }
summary:focus-visible, .frozen-intent:focus-visible { outline: 2px solid var(--ncp-primary); outline-offset: 2px; }
button { min-height: max(44px, var(--ncp-touch-target)); min-width: 0; white-space: normal; overflow-wrap: anywhere; padding: 8px 12px; border: 1px solid var(--ncp-line); border-radius: 8px; background: var(--ncp-surface); color: var(--ncp-text); cursor: pointer; }
button:disabled { opacity: .5; cursor: not-allowed; }
button:focus-visible { outline: 2px solid var(--ncp-primary); outline-offset: 2px; }
@media (max-width: 600px) { .protection-actions { flex-direction: column; } button { width: 100%; } }
</style>
