<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { KeyRound, MoreHorizontal, Plus, ShieldCheck, UserRound, UserRoundCheck } from '@lucide/vue'
import { ElButton, ElDialog, ElDropdown, ElDropdownItem, ElDropdownMenu, ElInput, ElInputNumber, ElMessage, ElSwitch } from 'element-plus'

import {
  createUser,
  deleteUser,
  requestPasswordPolicy,
  requestUsers,
  updatePasswordPolicy,
  updateUserPassword,
  updateUserStatus,
  type ManagedUser,
  type PasswordPolicy,
} from '@/api/control'
import WorkspaceHeader from '@/components/WorkspaceHeader.vue'
import ResourceState from '@/components/ResourceState.vue'
import ConfirmDangerDialog from '@/components/ConfirmDangerDialog.vue'
import { createRequestSequenceGate } from '@/domain/requestSequence'
import { captureSession, isCurrentSession } from '@/session/sessionLifecycle'
import { parsePasswordPolicyResponse } from '@/api/userResponse'
import { useAuthStore } from '@/stores/auth'

const authStore = useAuthStore()
const users = ref<ManagedUser[]>([])
const usersLoading = ref(true)
const usersError = ref('')
const policyLoading = ref(true)
const policyError = ref('')
const createOpen = ref(false)
const passwordOpen = ref(false)
// 提交锁与弹窗观察权分开：关闭只清草稿，不取消已发送的写入。
const createPending = ref(false)
const createGeneration = ref(0)
const passwordGeneration = ref(0)
const dangerGeneration = ref(0)
const pendingUsers = ref(new Set<number>())
const selectedUser = ref<ManagedUser | null>(null)
const createForm = ref({ username: '', password: '' })
const passwordForm = ref({ currentPassword: '', newPassword: '', confirmation: '' })
const confirmedPolicy = ref<PasswordPolicy | null>(null)
const policyDraft = ref<PasswordPolicy | null>(null)
let draftRevision = 0
watch(policyDraft, () => { draftRevision += 1 }, { deep: true, flush: 'sync' })
const policySaving = ref(false)
const dangerOpen = ref(false)

const dangerUser = ref<ManagedUser | null>(null)
const dangerAction = ref<'status' | 'delete'>('status')

const createBusy = computed(() => createPending.value)
const passwordBusy = computed(() => !!selectedUser.value && pendingUsers.value.has(selectedUser.value.id))
const dangerBusy = computed(() => !!dangerUser.value && pendingUsers.value.has(dangerUser.value.id))
watch(createOpen, () => {
  createGeneration.value += 1
  if (!createOpen.value) createForm.value = { username: '', password: '' }
}, { flush: 'sync' })
watch(passwordOpen, () => {
  passwordGeneration.value += 1
  if (!passwordOpen.value) passwordForm.value = { currentPassword: '', newPassword: '', confirmation: '' }
}, { flush: 'sync' })
watch(dangerOpen, () => { dangerGeneration.value += 1 }, { flush: 'sync' })

const currentUserId = computed(() => authStore.user?.id ?? 0)
const enabledCount = computed(() => users.value.filter((user) => !user.disabled).length)
const stats = computed(() => [
  { label: '全部账号', value: users.value.length },
  { label: '可用账号', value: enabledCount.value, tone: 'success' as const },
  { label: '权限模型', value: '等权 Root' },
])

const session = captureSession()
let active = true
const usersGate = createRequestSequenceGate()
const policyGate = createRequestSequenceGate()
let usersController: AbortController | undefined
let policyController: AbortController | undefined
let reloadTimer: ReturnType<typeof setTimeout> | undefined
const ownsPage = () => active && !session.signal.aborted && isCurrentSession(session.generation)
const policyReady = computed(() => ownsPage() && confirmedPolicy.value !== null && !policyLoading.value && !policyError.value)
const passwordSubmitReady = computed(() => policyReady.value && !policySaving.value)
const policyDirty = computed(() => JSON.stringify(policyDraft.value) !== JSON.stringify(confirmedPolicy.value))

async function loadUsers() {
  if (!ownsPage()) return
  const sequence = usersGate.begin()
  usersController?.abort()
  usersController = new AbortController()
  const signal = AbortSignal.any([session.signal, usersController.signal])
  const ownsRead = () => ownsPage() && usersGate.isLatest(sequence) && !signal.aborted
  usersLoading.value = true
  usersError.value = ''
  try {
    const items = await requestUsers(signal)
    if (ownsRead()) users.value = items
  } catch {
    if (ownsRead()) usersError.value = '账号列表加载失败，请稍后重试。'
  } finally {
    if (ownsRead()) usersLoading.value = false
  }
}

async function loadPasswordPolicy() {
  if (!ownsPage() || policySaving.value) return
  const sequence = policyGate.begin()
  const revision = draftRevision
  const wasDirty = policyDirty.value
  policyController?.abort()
  policyController = new AbortController()
  const signal = AbortSignal.any([session.signal, policyController.signal])
  const ownsRead = () => ownsPage() && policyGate.isLatest(sequence) && !signal.aborted
  policyLoading.value = true
  policyError.value = ''
  try {
    const policy = parsePasswordPolicyResponse(await requestPasswordPolicy(signal))
    if (!ownsRead()) return
    confirmedPolicy.value = policy
    if (!wasDirty && revision === draftRevision) policyDraft.value = { ...policy }
  } catch {
    if (ownsRead()) policyError.value = '密码规则加载失败；恢复前不能保存规则、创建账号或修改密码。'
  } finally {
    if (ownsRead()) policyLoading.value = false
  }
}

async function savePasswordPolicy() {
  if (!ownsPage() || !policyReady.value || policySaving.value || !policyDraft.value) return
  let snapshot: PasswordPolicy
  try {
    snapshot = parsePasswordPolicyResponse({ ...policyDraft.value })
  } catch {
    ElMessage.warning('请输入有效的密码规则。')
    return
  }
  const revision = draftRevision
  policyGate.invalidate()
  policyController?.abort()
  policySaving.value = true
  try {
    const policy = parsePasswordPolicyResponse(await updatePasswordPolicy(snapshot))
    if (!ownsPage()) return
    confirmedPolicy.value = policy
    if (revision === draftRevision) policyDraft.value = { ...policy }
    ElMessage.success('密码规则已保存。')
  } catch {
    if (!ownsPage()) return
    confirmedPolicy.value = null
    policyError.value = '规则保存结果未确认，请只读重新读取当前规则。'
    ElMessage.warning(policyError.value)
  } finally {
    if (ownsPage()) policySaving.value = false
  }
}

// 已确认的写入使更早的目录读取失效；仅重读，不重放写请求。
function invalidateUsersRead() {
  usersGate.invalidate()
  usersController?.abort()
  usersLoading.value = false
}

function reconcileUsers() {
  invalidateUsersRead()
  void loadUsers()
}

function dispose() {
  active = false
  usersGate.invalidate()
  policyGate.invalidate()
  usersController?.abort()
  policyController?.abort()
  if (reloadTimer !== undefined) clearTimeout(reloadTimer)
  usersLoading.value = false
  policyLoading.value = false
  policySaving.value = false
  createPending.value = false
  pendingUsers.value.clear()
  users.value = []
  confirmedPolicy.value = null
  policyDraft.value = null
  createForm.value = { username: '', password: '' }
  passwordForm.value = { currentPassword: '', newPassword: '', confirmation: '' }
  createOpen.value = passwordOpen.value = dangerOpen.value = false
  session.signal.removeEventListener('abort', dispose)
}
session.signal.addEventListener('abort', dispose, { once: true })
onBeforeUnmount(dispose)

function passwordRuleText() {
  const policy = confirmedPolicy.value
  if (!policy) return '等待确认密码规则'
  const rules = [`至少 ${policy.minLength} 个字符`]
  if (policy.requireUppercase) rules.push('大写字母')
  if (policy.requireLowercase) rules.push('小写字母')
  if (policy.requireDigit) rules.push('数字')
  if (policy.requireSpecial) rules.push('特殊字符')
  return rules.join('、')
}

async function submitCreate() {
  if (!ownsPage() || !createOpen.value || !passwordSubmitReady.value || !confirmedPolicy.value || createPending.value) return
  const payload = { ...createForm.value }
  if (!payload.username.trim() || payload.password.length < confirmedPolicy.value.minLength) {
    ElMessage.warning(`请输入用户名，密码需${passwordRuleText()}。`)
    return
  }
  const generation = createGeneration.value
  const ownsDialog = () => ownsPage() && createOpen.value && generation === createGeneration.value
  createPending.value = true
  try {
    const user = await createUser(payload)
    if (!ownsPage()) return
    users.value = [...users.value.filter((item) => item.id !== user.id), user].sort((left, right) => left.username.localeCompare(right.username))
    reconcileUsers()
    if (ownsDialog()) {
      createOpen.value = false
      ElMessage.success('账号已创建。')
    }
  } catch (reason) {
    if (ownsDialog()) ElMessage.error(reason instanceof Error ? reason.message : '账号创建失败。')
  } finally {
    if (ownsPage()) createPending.value = false
  }
}

function openPassword(user: ManagedUser) {
  passwordGeneration.value += 1
  selectedUser.value = { ...user }
  passwordForm.value = { currentPassword: '', newPassword: '', confirmation: '' }
  passwordOpen.value = true
}

async function submitPassword() {
  const targetId = selectedUser.value?.id
  if (!ownsPage() || !passwordOpen.value || !passwordSubmitReady.value || !confirmedPolicy.value || targetId === undefined || pendingUsers.value.has(targetId)) return
  const draft = { ...passwordForm.value }
  if (draft.newPassword.length < confirmedPolicy.value.minLength || draft.newPassword !== draft.confirmation) {
    ElMessage.warning(`新密码需${passwordRuleText()}，且两次输入必须一致。`)
    return
  }
  const actorId = currentUserId.value
  const isCurrent = targetId === actorId
  const payload = { currentPassword: isCurrent ? draft.currentPassword : '', newPassword: draft.newPassword }
  const generation = passwordGeneration.value
  const ownsDialog = () => ownsPage() && passwordOpen.value && generation === passwordGeneration.value
  pendingUsers.value.add(targetId)
  try {
    await updateUserPassword(targetId, payload)
    if (!ownsPage()) return
    const observed = ownsDialog()
    if (observed) passwordOpen.value = false
    if (isCurrent) {
      // 原会话已撤销，即使原弹窗关闭仍须重新登录；不读取受认证目录。
      invalidateUsersRead()
      ElMessage.success('密码已修改，请重新登录。')
      if (reloadTimer !== undefined) clearTimeout(reloadTimer)
      reloadTimer = setTimeout(() => {
        reloadTimer = undefined
        if (ownsPage() && currentUserId.value === actorId) window.location.reload()
      }, 600)
    } else {
      reconcileUsers()
      if (observed) ElMessage.success('密码已重置，该账号的现有会话已撤销。')
    }
  } catch (reason) {
    if (ownsDialog()) ElMessage.error(reason instanceof Error ? reason.message : '密码修改失败。')
  } finally {
    if (ownsPage()) pendingUsers.value.delete(targetId)
  }
}

function requestStatusChange(user: ManagedUser) {
  dangerGeneration.value += 1
  dangerUser.value = { ...user }
  dangerAction.value = 'status'
  dangerOpen.value = true
}

function requestDelete(user: ManagedUser) {
  dangerGeneration.value += 1
  dangerUser.value = { ...user }
  dangerAction.value = 'delete'
  dangerOpen.value = true
}

async function confirmDanger() {
  const user = dangerUser.value ? { ...dangerUser.value } : null
  if (!ownsPage() || !dangerOpen.value || !user || pendingUsers.value.has(user.id)) return
  const action = dangerAction.value
  const disabled = !user.disabled
  const generation = dangerGeneration.value
  const ownsDialog = () => ownsPage() && dangerOpen.value && generation === dangerGeneration.value
  pendingUsers.value.add(user.id)
  try {
    if (action === 'status') {
      const updated = await updateUserStatus(user.id, disabled)
      if (!ownsPage()) return
      users.value = users.value.map((item) => item.id === user.id ? updated : item)
    } else {
      await deleteUser(user.id)
      if (!ownsPage()) return
      users.value = users.value.filter((item) => item.id !== user.id)
    }
    reconcileUsers()
    if (ownsDialog()) {
      dangerOpen.value = false
      ElMessage.success(action === 'delete' ? '账号已删除。' : `账号已${disabled ? '禁用' : '启用'}。`)
    }
  } catch (reason) {
    if (ownsDialog()) ElMessage.error(reason instanceof Error ? reason.message : action === 'status' ? '账号状态更新失败。' : '删除失败。')
  } finally {
    if (ownsPage()) pendingUsers.value.delete(user.id)
  }
}

function formatTime(value?: string | null) {
  if (!value) return '从未登录'
  return new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value))
}

onMounted(() => { void loadUsers(); void loadPasswordPolicy() })
</script>

<template>
  <div class="page workspace-page users-page">
    <WorkspaceHeader title="用户管理" description="管理可登录 NCP 的等权 Root 账号与会话安全" :icon="UserRound" :stats="stats" />

    <section class="password-policy panel">
      <header>
        <div><h2>密码规则</h2><p>新密码和新账号按服务器已确认的规则校验；未保存的修改不会用于密码提示。</p></div>
        <ElButton type="primary" plain :loading="policySaving" :disabled="!policyReady || policySaving" @click="savePasswordPolicy">保存规则</ElButton>
      </header>
      <p v-if="policyLoading" role="status">正在加载密码规则…</p>
      <ResourceState v-else-if="policyError" state="error" title="密码规则暂不可用" :message="policyError" @retry="loadPasswordPolicy" />
      <p v-if="policyDirty && policyDraft" role="status">有未保存的规则修改</p>
      <div v-if="policyDraft" class="password-policy__controls">
        <label><span>最小长度</span><ElInputNumber :disabled="!policyReady" v-model="policyDraft.minLength" :min="1" :max="128" controls-position="right" /></label>
        <label><span>必须有大写字母</span><ElSwitch :disabled="!policyReady" v-model="policyDraft.requireUppercase" /></label>
        <label><span>必须有小写字母</span><ElSwitch :disabled="!policyReady" v-model="policyDraft.requireLowercase" /></label>
        <label><span>必须有数字</span><ElSwitch :disabled="!policyReady" v-model="policyDraft.requireDigit" /></label>
        <label><span>必须有特殊字符</span><ElSwitch :disabled="!policyReady" v-model="policyDraft.requireSpecial" /></label>
      </div>
    </section>

    <section class="users-panel panel">
      <header class="users-panel__header">
        <div><h2>Root 账号</h2><p>所有账号拥有相同管理权限；系统始终保留至少一个可用账号。</p></div>
        <div class="users-panel__toolbar">
          <span><ShieldCheck :size="17" />{{ enabledCount }} 个账号可登录</span>
          <ElButton type="primary" :icon="Plus" :disabled="!passwordSubmitReady" @click="createOpen = true">新增账号</ElButton>
        </div>
      </header>

      <div v-if="usersLoading && users.length === 0" class="user-skeleton" aria-label="正在加载账号">
        <div v-for="item in 4" :key="item"><span class="ncp-skeleton"></span><i class="ncp-skeleton"></i><i class="ncp-skeleton"></i></div>
      </div>
      <ResourceState v-if="usersError" state="error" title="账号列表加载失败" :message="usersError" next-step="确认服务可用后重试。" @retry="loadUsers" />
      <ResourceState v-if="!usersLoading && !usersError && users.length === 0" state="empty" title="暂无账号" message="创建第一个 Root 管理账号后即可登录控制面。" />
      <div v-if="users.length > 0" class="user-table">
        <div class="user-table__head">
          <span>账号</span><span>状态</span><span>最近登录</span><span>创建时间</span><span>操作</span>
        </div>
        <article v-for="user in users" :key="user.id" class="user-row">
          <div class="user-identity">
            <span><UserRoundCheck :size="20" /></span>
            <div><strong>{{ user.username }}</strong><small>{{ user.id === currentUserId ? '当前账号 · Root' : 'Root 管理账号' }}</small></div>
          </div>
          <span :class="['account-status', { 'account-status--disabled': user.disabled }]"><i></i>{{ user.disabled ? '已禁用' : '可用' }}</span>
          <dl class="user-date">
            <dt class="user-date__label">最近登录</dt>
            <dd class="user-date__value"><time>{{ formatTime(user.lastLoginAt) }}</time></dd>
          </dl>
          <dl class="user-date">
            <dt class="user-date__label">创建时间</dt>
            <dd class="user-date__value"><time>{{ formatTime(user.createdAt) }}</time></dd>
          </dl>
          <div class="user-actions">
            <ElButton :icon="KeyRound" :disabled="!passwordSubmitReady" plain @click="openPassword(user)">{{ user.id === currentUserId ? '修改密码' : '重置密码' }}</ElButton>
            <ElDropdown v-if="user.id !== currentUserId" trigger="click">
              <ElButton :icon="MoreHorizontal" circle aria-label="更多账号操作" />
              <template #dropdown>
                <ElDropdownMenu>
                  <ElDropdownItem @click="requestStatusChange(user)">{{ user.disabled ? '启用账号' : '禁用账号' }}</ElDropdownItem>
                  <ElDropdownItem divided class="danger-action" @click="requestDelete(user)">删除账号</ElDropdownItem>
                </ElDropdownMenu>
              </template>
            </ElDropdown>
          </div>
        </article>
      </div>
    </section>

    <ElDialog v-model="createOpen" title="新增 Root 账号" width="440px" destroy-on-close>
      <div class="dialog-form">
        <label><span>用户名</span><ElInput v-model="createForm.username" maxlength="64" autocomplete="username" placeholder="例如 operator" /></label>
        <label><span>初始密码</span><ElInput v-model="createForm.password" type="password" show-password autocomplete="new-password" :placeholder="passwordRuleText()" /></label>
      </div>
      <template #footer><ElButton @click="createOpen = false">取消</ElButton><ElButton type="primary" :loading="createBusy" :disabled="!passwordSubmitReady || createBusy" @click="submitCreate">创建账号</ElButton></template>
    </ElDialog>

    <ElDialog v-model="passwordOpen" :title="selectedUser?.id === currentUserId ? '修改自己的密码' : `重置 ${selectedUser?.username} 的密码`" width="440px" destroy-on-close>
      <div class="dialog-form">
        <label v-if="selectedUser?.id === currentUserId"><span>当前密码</span><ElInput v-model="passwordForm.currentPassword" type="password" show-password autocomplete="current-password" /></label>
        <label><span>新密码</span><ElInput v-model="passwordForm.newPassword" type="password" show-password autocomplete="new-password" :placeholder="passwordRuleText()" /></label>
        <label><span>确认新密码</span><ElInput v-model="passwordForm.confirmation" type="password" show-password autocomplete="new-password" /></label>
        <p class="dialog-note">保存后，该账号的全部登录会话将立即失效。</p>
      </div>
      <template #footer><ElButton @click="passwordOpen = false">取消</ElButton><ElButton type="primary" :loading="passwordBusy" :disabled="!passwordSubmitReady || passwordBusy" @click="submitPassword">保存新密码</ElButton></template>
    </ElDialog>

    <ConfirmDangerDialog
      :key="dangerGeneration"
      v-model="dangerOpen"
      :title="dangerAction === 'delete' ? '删除账号' : `${dangerUser?.disabled ? '启用' : '禁用'}账号`"
      :description="dangerAction === 'delete' ? `账号“${dangerUser?.username}”的全部会话会失效，删除后账号本身无法恢复。` : `确认${dangerUser?.disabled ? '启用' : '禁用'}账号“${dangerUser?.username}”？`"
      :impact="dangerAction === 'delete' ? ['永久删除该 Root 账号', '使该账号的全部会话失效'] : dangerUser?.disabled ? ['恢复该账号的登录能力'] : ['立即停止该账号登录', '使该账号的现有会话失效']"
      :retained="['不会删除 Docker、数据库或站点数据', '历史数据和配置保持不变']"
      :action-label="dangerAction === 'delete' ? '永久删除' : dangerUser?.disabled ? '启用账号' : '禁用账号'"
      :confirmation-target="dangerAction === 'delete' ? (dangerUser?.username ?? '') : ''"
      :confirmation-label="dangerAction === 'delete' ? '输入账号名确认' : undefined"
      :busy="dangerBusy"
      @confirm="confirmDanger"
    />
  </div>
</template>

<style scoped>
.password-policy{overflow:hidden}.password-policy>p{padding:10px 20px;overflow-wrap:anywhere}.password-policy>header{display:flex;align-items:center;justify-content:space-between;gap:20px;padding:16px 20px;border-bottom:1px solid var(--ncp-line)}.password-policy h2{margin:0;font-size:1rem}.password-policy p{margin:4px 0 0;color:var(--ncp-text-subtle);font-size:.82rem}.password-policy__controls{display:grid;grid-template-columns:1.25fr repeat(4,1fr);gap:0}.password-policy__controls label{display:flex;min-height:64px;align-items:center;justify-content:space-between;gap:12px;padding:10px 16px;border-right:1px solid var(--ncp-line)}.password-policy__controls label:last-child{border-right:0}.password-policy__controls span{font-size:.82rem;font-weight:650}.password-policy__controls :deep(.el-input-number){width:112px}
.users-panel{overflow:hidden}.users-panel__header{display:flex;align-items:center;justify-content:space-between;gap:20px;padding:18px 22px 10px}.users-panel__header h2{margin:0;font-size:1rem}.users-panel__header p{margin:3px 0 0;font-size:.84rem}.users-panel__toolbar{display:flex;align-items:center;justify-content:space-between;gap:16px;padding:0 22px 14px;border-bottom:1px solid var(--ncp-line)}.users-panel__toolbar>span{display:flex;align-items:center;gap:7px;color:var(--ncp-success);font-size:.82rem;font-weight:700}.user-table__head,.user-row{display:grid;grid-template-columns:minmax(240px,1.4fr) 120px minmax(170px,.7fr) minmax(170px,.7fr) 190px;align-items:center;gap:16px;padding-inline:22px}.user-table__head{min-height:48px;background:var(--ncp-surface-quiet);color:var(--ncp-text-subtle);font-size:.78rem;font-weight:750}.user-table__head span:nth-child(2),.user-table__head span:last-child{text-align:center}.user-row{min-height:82px;border-top:1px solid var(--ncp-line);transition:background-color var(--ncp-duration-fast)}.user-row:first-of-type{border-top:0}.user-row:hover{background:#fafcff}.user-identity{display:flex;align-items:center;gap:12px;min-width:0}.user-identity>span{display:grid;width:42px;height:42px;flex:0 0 auto;place-items:center;border-radius:12px;background:var(--ncp-primary-soft);color:var(--ncp-primary-strong)}.user-identity div{display:grid;min-width:0}.user-identity strong{font-size:.92rem;overflow-wrap:anywhere}.user-identity small,.user-row time{color:var(--ncp-text-subtle);font-size:.8rem}.account-status{display:inline-flex;width:max-content;align-items:center;justify-self:center;gap:7px;padding:5px 10px;border:1px solid rgba(35,134,111,.18);border-radius:999px;background:var(--ncp-success-soft);color:var(--ncp-success-strong);font-size:.78rem;font-weight:700}.account-status i{width:6px;height:6px;border-radius:50%;background:currentColor}.account-status--disabled{border-color:var(--ncp-line);background:var(--ncp-surface-quiet);color:var(--ncp-text-subtle)}.user-actions{display:flex;flex-wrap:wrap;min-width:0;align-items:center;justify-content:center;gap:8px}.user-actions :deep(.el-button){min-height:36px}.users-empty{display:grid;min-height:300px;place-items:center;align-content:center;gap:10px;color:var(--ncp-text-subtle)}.user-skeleton>div{display:grid;grid-template-columns:280px 120px 180px;gap:36px;align-items:center;min-height:82px;padding:0 22px;border-top:1px solid var(--ncp-line)}.user-skeleton span{width:210px;height:38px}.user-skeleton i{width:100px;height:14px}.dialog-form{display:grid;gap:16px}.dialog-form label{display:grid;gap:7px}.dialog-form label>span{color:var(--ncp-text);font-size:.84rem;font-weight:700}.dialog-note{margin:0;padding:10px 12px;border-radius:9px;background:var(--ncp-warning-soft);color:var(--ncp-warning);font-size:.8rem}.danger-action{color:var(--ncp-danger-strong)}@media(max-width:1050px){.password-policy__controls{grid-template-columns:repeat(2,1fr)}.password-policy__controls label{border-bottom:1px solid var(--ncp-line)}}@media(max-width:600px){.password-policy>header{align-items:flex-start;flex-direction:column}.password-policy__controls{grid-template-columns:1fr}.password-policy__controls label{border-right:0}.users-panel__header{align-items:flex-start;flex-direction:column}.users-panel__toolbar{align-items:flex-start;flex-direction:column}.users-page :deep(.el-dialog){width:calc(100vw - 28px)!important}}
.users-panel__header{flex-wrap:wrap;align-items:center;padding:18px 22px;border-bottom:1px solid var(--ncp-line)}.users-panel__header>div:first-child{min-width:0}.users-panel__toolbar{flex:0 1 auto;flex-wrap:wrap;min-width:0;align-items:center;justify-content:flex-end;gap:14px;padding:0;border:0}.users-panel__toolbar>span{min-width:0;overflow-wrap:anywhere}.users-panel__toolbar>span>svg{flex-shrink:0}.users-panel__toolbar :deep(.el-button){min-height:40px;margin:0}.user-table__head{min-height:46px}.user-row{min-height:78px}.user-actions :deep(.el-button){margin:0}.users-panel__header p{color:var(--ncp-text-subtle)}
@media(max-width:760px){.users-panel__header{align-items:flex-start;gap:14px;padding:16px}.users-panel__toolbar{width:100%;flex-direction:row;justify-content:space-between}.users-panel__toolbar>span{font-size:.78rem}.users-panel__toolbar :deep(.el-button){min-height:42px}.user-row{min-height:0}}
.user-date{min-width:0;margin:0;overflow-wrap:anywhere}
.user-date__label{display:none;color:var(--ncp-text-subtle);font-size:.8rem}
.user-date__value{min-width:0;margin:0}
@media(max-width:1050px){
  .user-table__head{display:none}
  .user-row{min-width:0;grid-template-columns:minmax(0,1fr) auto;gap:12px;padding:16px}
  .account-status{grid-column:2;grid-row:1;align-self:start}
  .user-date{display:grid;grid-column:1/-1;grid-template-columns:minmax(0,.55fr) minmax(0,1fr);align-items:baseline;gap:12px}
  .user-date__label{display:block}
  .user-actions{grid-column:1/-1;justify-content:flex-start}
}
@media(max-width:479px){
  .user-identity{grid-column:1/-1}
  .account-status{grid-column:1/-1;grid-row:auto;justify-self:start}
}
</style>
