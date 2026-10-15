<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, shallowRef, watch } from 'vue'
import { Box, Cpu, HardDrive, Network, Plus, Shield, Trash2 } from '@lucide/vue'
import { ElAlert, ElDialog, ElDrawer, ElEmpty, ElInput, ElInputNumber, ElMessage, ElSwitch, ElTooltip } from 'element-plus'

import {
  NcpApiError,
  CreateContainerOutcomeUnknownError,
  createDockerContainer,
  requestDockerInventory,
  requestDockerResources,
  requestPathEntries,
  type DockerContainerCreateInput,
  type DockerInventory,
  type DockerImageSummary,
  type DockerResources,
  type FileEntriesPage,
  type FileEntry,
} from '@/api/system'
import ActionButton from '@/components/ActionButton.vue'
import NcpSelect, { type NcpSelectOption } from '@/components/NcpSelect.vue'
import SectionHeader from '@/components/SectionHeader.vue'
import { hostPortBindingKey } from '@/domain/dockerPorts'
import { createScopedReadRequest } from '@/domain/scopedReadRequest'
import { captureSession, isCurrentSession } from '@/session/sessionLifecycle'

type EnvironmentRow = { key: string; value: string }
type MountRow = { type: 'bind' | 'volume' | 'tmpfs'; source: string; target: string; readOnly: boolean }
type PortRow = { hostIp: string; hostPort: number | undefined; containerPort: number | undefined; protocol: 'tcp' | 'udp' | 'sctp' }
type DeviceRow = { hostPath: string; containerPath: string; permissions: string }

const props = defineProps<{ modelValue: boolean; image: DockerImageSummary | null }>()
const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  created: [containerId: string]
}>()

type UnresolvedAttempt = Readonly<{ name: string; image: string; runContainer: boolean; time: string }>
const unresolvedAttempt = shallowRef<UnresolvedAttempt | null>(null)
type VerificationContainer = Pick<DockerInventory['containers'][number], 'id' | 'name' | 'image' | 'state' | 'status'>
const verificationInventory = shallowRef<{ containers: VerificationContainer[] } | null>(null)
const verificationLoading = ref(false)
const verificationError = ref('')
const verificationRead = createScopedReadRequest()
let releaseAttemptSession: (() => void) | undefined

const submitting = ref<'create' | 'start' | null>(null)
const advancedOpen = ref(false)
const previewOpen = ref(false)
const pendingRunContainer = ref(false)
const pendingInput = ref<DockerContainerCreateInput | null>(null)
const resourcesLoading = ref(false)
const resourcesError = ref('')
const dockerResources = ref<DockerResources | null>(null)
const dockerInventory = ref<DockerInventory | null>(null)
const pathBrowserOpen = ref(false)
const pathBrowserLoading = ref(false)
const pathBrowserError = ref('')
const pathBrowserPage = ref<FileEntriesPage | null>(null)
const pathBrowserTarget = shallowRef<MountRow | null>(null)
const resourceRead = createScopedReadRequest()
const pathRead = createScopedReadRequest()
let alive = true
let opening = 0
let revision = 0
let draftSession: number | null = null
let previewOwner: { opening: number; revision: number; session: number } | null = null
let releaseSession: (() => void) | undefined
const environment = ref<EnvironmentRow[]>([])
const mounts = ref<MountRow[]>([])
const ports = ref<PortRow[]>([])
const devices = ref<DeviceRow[]>([])
const commandText = ref('')
const restartPolicyOptions: NcpSelectOption[] = [
  { label: '不自动重启', value: 'no' },
  { label: '除非手动停止', value: 'unless-stopped' },
  { label: '始终重启', value: 'always' },
  { label: '失败时重启', value: 'on-failure' },
]
const mountTypeOptions: NcpSelectOption[] = [
  { label: '主机目录', value: 'bind' },
  { label: 'Docker 卷', value: 'volume' },
  { label: '临时目录', value: 'tmpfs' },
]
const protocolOptions: NcpSelectOption[] = [
  { label: 'TCP', value: 'tcp' },
  { label: 'UDP', value: 'udp' },
  { label: 'SCTP', value: 'sctp' },
]
const form = reactive({
  name: '',
  cpu: undefined as number | undefined,
  memoryMiB: undefined as number | undefined,
  restartPolicy: 'no' as NonNullable<DockerContainerCreateInput['restartPolicy']>,
  restartMaxRetries: 0,
  networkEnabled: false,
  networkName: 'bridge',
  networkCreateDedicated: false,
  networkDriver: '',
  networkSubnet: '',
  networkGateway: '',
  networkIp: '',
  privileged: false,
  capAdd: '',
  capDrop: '',
  gpuEnabled: false,
  gpuDriver: '',
  gpuCount: 1,
  gpuCapabilities: 'gpu',
})

const imageReference = computed(() => props.image?.repoTags[0] || props.image?.id || '')
const drawerTitle = computed(() => imageReference.value ? `从 ${imageReference.value} 创建容器` : '创建容器')
const networkOptions = computed<NcpSelectOption[]>(() => {
  const options = (dockerResources.value?.networks || []).map((network) => ({
    label: `${network.name} · ${network.driver || '默认驱动'}`,
    value: network.name,
  }))
  for (const builtin of [
    { label: 'bridge · Docker 默认网络', value: 'bridge' },
    { label: 'host · 使用主机网络', value: 'host' },
    { label: 'none · 禁用容器网络', value: 'none' },
  ]) {
    if (!options.some((option) => option.value === builtin.value)) options.push(builtin)
  }
  if (form.networkName && !options.some((option) => option.value === form.networkName)) {
    options.unshift({ label: `${form.networkName} · 手动输入`, value: form.networkName })
  }
  return options
})
const volumeOptions = computed<NcpSelectOption[]>(() => {
  const options = (dockerResources.value?.volumes || []).map((volume) => ({
    label: `${volume.name} · ${volume.driver || 'local'}`,
    value: volume.name,
  }))
  if (mounts.value.some((mount) => mount.type === 'volume' && mount.source && !options.some((option) => option.value === mount.source))) {
    for (const mount of mounts.value) {
      if (mount.type === 'volume' && mount.source && !options.some((option) => option.value === mount.source)) {
        options.push({ label: `${mount.source} · 手动输入`, value: mount.source })
      }
    }
  }
  return options
})
const portConflictNotice = computed(() => {
  const bindings = new Set(
    (dockerInventory.value?.containers || [])
      .flatMap((container) => container.ports || [])
      .filter((port) => port.publicPort > 0)
        .map((port) => hostPortBindingKey(port.hostIp, port.publicPort, port.protocol)),
  )
  const conflicts = ports.value
    .filter((port) => Number.isInteger(port.hostPort) && Number(port.hostPort) > 0)
    .map((port) => hostPortBindingKey(port.hostIp, port.hostPort, port.protocol))
    .filter((binding) => bindings.has(binding))
  return [...new Set(conflicts)]
})
const currentPath = computed(() => pathBrowserPage.value?.path || '/')
const pathEntries = computed(() => pathBrowserPage.value?.entries || [])
const previewInput = computed(() => pendingInput.value)

function invalidatePreview() {
  previewOwner = null
  pendingInput.value = null
  previewOpen.value = false
}

function invalidateReads() {
  verificationRead.invalidate()
  verificationLoading.value = false
  verificationInventory.value = null
  verificationError.value = ''
  resourceRead.invalidate()
  pathRead.invalidate()
  resourcesLoading.value = false
  pathBrowserLoading.value = false
  pathBrowserOpen.value = false
  pathBrowserTarget.value = null
}

watch([form, environment, mounts, ports, devices, commandText], () => {
  revision += 1
  invalidatePreview()
}, { deep: true, flush: 'sync' })

watch(() => [props.modelValue, props.image?.id, imageReference.value] as const, ([open]) => {
  opening += 1
  releaseSession?.()
  draftSession = null
  invalidateReads()
  invalidatePreview()
  dockerResources.value = null
  dockerInventory.value = null
  if (!open) return
  const session = captureSession()
  draftSession = session.generation
  const abort = () => {
    draftSession = null
    opening += 1
    invalidateReads()
    invalidatePreview()
    dockerResources.value = null
    dockerInventory.value = null
  }
  session.signal.addEventListener('abort', abort, { once: true })
  releaseSession = () => session.signal.removeEventListener('abort', abort)
  resetForm()
  void loadResources()
}, { immediate: true, flush: 'sync' })

watch(pathBrowserOpen, (open) => {
  if (open) return
  pathRead.invalidate()
  pathBrowserLoading.value = false
  pathBrowserPage.value = null
  pathBrowserTarget.value = null
}, { flush: 'sync' })
watch(previewOpen, (open) => { if (!open) { previewOwner = null; pendingInput.value = null } }, { flush: 'sync' })

onBeforeUnmount(() => {
  releaseAttemptSession?.()
  unresolvedAttempt.value = null
  alive = false
  opening += 1
  releaseSession?.()
  invalidateReads()
  invalidatePreview()
})

function resetForm() {
  form.name = suggestedName(imageReference.value)
  form.cpu = undefined
  form.memoryMiB = undefined
  form.restartPolicy = 'no'
  form.restartMaxRetries = 0
  form.networkEnabled = false
  form.networkName = 'bridge'
  form.networkCreateDedicated = false
  form.networkDriver = ''
  form.networkSubnet = ''
  form.networkGateway = ''
  form.networkIp = ''
  form.privileged = false
  form.capAdd = ''
  form.capDrop = ''
  form.gpuEnabled = false
  form.gpuDriver = ''
  form.gpuCount = 1
  form.gpuCapabilities = 'gpu'
  environment.value = []
  mounts.value = []
  ports.value = []
  devices.value = []
  commandText.value = ''
  advancedOpen.value = false
  pendingInput.value = null
  pendingRunContainer.value = false
  resourcesError.value = ''
  pathBrowserError.value = ''
  pathBrowserPage.value = null
  pathBrowserTarget.value = null
}

async function loadResources() {
  const request = resourceRead.begin()
  resourcesLoading.value = true
  resourcesError.value = ''
  try {
    const [resourceResult, inventoryResult] = await Promise.allSettled([
      requestDockerResources(undefined, request.signal), requestDockerInventory(undefined, request.signal),
    ])
    if (!request.isCurrent()) return
    if (resourceResult.status === 'fulfilled') {
      dockerResources.value = resourceResult.value
    } else {
      dockerResources.value = null
      resourcesError.value = 'Docker 网络和卷清单暂不可用，可继续使用高级手输。'
    }
    dockerInventory.value = inventoryResult.status === 'fulfilled' ? inventoryResult.value : null
  } finally {
    if (request.isCurrent()) resourcesLoading.value = false
    request.finish()
  }
}

function suggestedName(reference: string) {
  const repository = reference.split('@')[0]?.split(':')[0]?.split('/').at(-1) || 'container'
  return repository.replace(/[^a-zA-Z0-9_.-]+/g, '-').slice(0, 100)
}

function addEnvironment() { environment.value.push({ key: '', value: '' }) }
function addMount() { mounts.value.push({ type: 'bind', source: '', target: '', readOnly: false }) }
function addPort() { ports.value.push({ hostIp: '', hostPort: undefined, containerPort: undefined, protocol: 'tcp' }) }
function addDevice() { devices.value.push({ hostPath: '', containerPath: '', permissions: 'rwm' }) }

function removeRow<T>(rows: T[], index: number) {
  if (rows[index] === pathBrowserTarget.value) pathBrowserOpen.value = false
  rows.splice(index, 1)
}

function setMountType(row: MountRow, value: string) {
  if (row === pathBrowserTarget.value) pathBrowserOpen.value = false
  row.type = value as MountRow['type']
  if (row.type === 'tmpfs') row.source = ''
}

function setNetworkName(value: string) {
  form.networkName = value
  if (value === 'host' || value === 'none') form.networkCreateDedicated = false
}

async function openPathBrowser(index: number) {
  if (!alive || !props.modelValue || draftSession === null || !isCurrentSession(draftSession)) return
  pathRead.invalidate()
  pathBrowserPage.value = null
  pathBrowserTarget.value = mounts.value[index] || null
  pathBrowserOpen.value = true
  await loadPath('/')
}

async function loadPath(path: string, cursor = '') {
  const target = pathBrowserTarget.value
  if (!props.modelValue || !pathBrowserOpen.value || !target || !mounts.value.includes(target)) return
  const request = pathRead.begin()
  const current = () => request.isCurrent() && pathBrowserOpen.value && pathBrowserTarget.value === target && mounts.value.includes(target) && target.type === 'bind'
  const append = Boolean(cursor)
  const previous = pathBrowserPage.value
  pathBrowserLoading.value = true
  pathBrowserError.value = ''
  try {
    const result = await requestPathEntries(path, cursor, 100, undefined, request.signal)
    if (!current()) return
    if (append && previous?.path === result.path) {
      const seen = new Set(previous.entries.map((entry) => entry.path))
      pathBrowserPage.value = {
        ...result,
        entries: [...previous.entries, ...result.entries.filter((entry) => !seen.has(entry.path))],
      }
    } else {
      pathBrowserPage.value = result
    }
  } catch (caught) {
    if (!current()) return
    pathBrowserError.value = caught instanceof NcpApiError ? caught.message : 'NAS 目录暂不可读取，请重试。'
    if (!append) pathBrowserPage.value = null
  } finally {
    if (current()) pathBrowserLoading.value = false
    request.finish()
  }
}

function choosePath(entry: FileEntry) {
  const row = pathBrowserTarget.value
  if (!props.modelValue || !pathBrowserOpen.value || !row || !mounts.value.includes(row) || row.type !== 'bind') return
  row.source = entry.path
  pathBrowserOpen.value = false
}

function previewLabel(value: string | undefined, fallback: string) {
  return value?.trim() || fallback
}

function openSubmitPreview(runContainer: boolean) {
  if (unresolvedAttempt.value || submitting.value || !alive || !props.modelValue || draftSession === null || !isCurrentSession(draftSession)) return
  const input = buildInput(runContainer)
  const message = validationMessage(input)
  if (message) {
    ElMessage.warning(message)
    return
  }
  pendingInput.value = input
  previewOwner = { opening, revision, session: captureSession().generation }
  pendingRunContainer.value = runContainer
  previewOpen.value = true
}

function commaList(value: string) {
  return value.split(',').map((item) => item.trim()).filter(Boolean)
}

function buildInput(runContainer: boolean): DockerContainerCreateInput {
  const input: DockerContainerCreateInput = {
    image: imageReference.value,
    name: form.name.trim() || undefined,
    cpu: form.cpu || undefined,
    memoryBytes: form.memoryMiB ? Math.round(form.memoryMiB * 1024 * 1024) : undefined,
    restartPolicy: form.restartPolicy,
    restartMaxRetries: form.restartPolicy === 'on-failure' ? form.restartMaxRetries : undefined,
    environment: Object.fromEntries(environment.value.filter((row) => row.key.trim()).map((row) => [row.key.trim(), row.value])),
    mounts: mounts.value.map((row) => ({
      type: row.type,
      source: row.type === 'tmpfs' ? undefined : row.type === 'bind' ? row.source : row.source.trim(),
      target: row.target,
      readOnly: row.readOnly,
    })),
    ports: ports.value.map((row) => ({
      hostIp: row.hostIp.trim() || undefined,
      hostPort: row.hostPort,
      containerPort: Number(row.containerPort),
      protocol: row.protocol,
    })),
    command: commandText.value.trim() ? commandText.value.split('\n').filter((item) => item !== '') : undefined,
    privileged: form.privileged,
    capAdd: commaList(form.capAdd),
    capDrop: commaList(form.capDrop),
    devices: devices.value.map((row) => ({
      hostPath: row.hostPath,
      containerPath: row.containerPath,
      cgroupPermissions: row.permissions.trim() || 'rwm',
    })),
    gpus: form.gpuEnabled ? [{
      driver: form.gpuDriver.trim() || undefined,
      count: form.gpuCount,
      capabilities: commaList(form.gpuCapabilities),
    }] : undefined,
    runContainer,
  }
  if (form.networkEnabled) {
    input.network = {
      name: form.networkName.trim(),
      driver: form.networkCreateDedicated ? form.networkDriver.trim() || undefined : undefined,
      subnet: form.networkCreateDedicated ? form.networkSubnet.trim() || undefined : undefined,
      gateway: form.networkCreateDedicated ? form.networkGateway.trim() || undefined : undefined,
      ip: form.networkCreateDedicated ? form.networkIp.trim() || undefined : undefined,
    }
  }
  return input
}

function validationMessage(input: DockerContainerCreateInput) {
  if (!input.image) return '镜像引用不能为空。'
  if (input.name && !/^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$/.test(input.name)) return '容器名称只能包含字母、数字、点、下划线和连字符。'
  if (input.command?.some((argument) => ['\0', '\r', '\n'].some((character) => argument.includes(character)))) return '启动参数不能包含 NUL 或换行控制字符。'
  const rawEnvironmentKeys = environment.value.map((row) => row.key.trim()).filter(Boolean)
  if (environment.value.some((row) => !row.key.trim() && row.value) || rawEnvironmentKeys.some((key) => !/^[A-Za-z_][A-Za-z0-9_]*$/.test(key)) || new Set(rawEnvironmentKeys).size !== rawEnvironmentKeys.length) return '环境变量名称无效、缺失或重复。'
  if (input.mounts?.some((mount) => !mount.target.startsWith('/') || (mount.type !== 'tmpfs' && !mount.source) || (mount.type === 'bind' && !mount.source?.startsWith('/')))) return '挂载项必须填写来源和绝对目标路径。'
  const mountTargets = input.mounts?.map((mount) => mount.target) ?? []
  if (new Set(mountTargets).size !== mountTargets.length) return '同一个容器路径不能重复挂载。'
  if (input.ports?.some((port) => !Number.isInteger(port.containerPort) || port.containerPort < 1 || port.containerPort > 65535)) return '容器端口必须是 1–65535 的整数。'
  const portTargets = input.ports?.map((port) => `${port.containerPort}/${port.protocol ?? 'tcp'}`) ?? []
  if (new Set(portTargets).size !== portTargets.length) return '同一个容器端口和协议不能重复映射。'
  if (input.network && !input.network.name) return '启用网络配置后必须填写网络名称。'
  if (form.networkCreateDedicated && input.network && !input.network.subnet) return '创建或复用专用网络时必须填写子网。'
  if (input.network && ['host', 'none'].includes(input.network.name) && (input.network.driver || input.network.subnet || input.network.gateway || input.network.ip)) return 'host 和 none 网络不能配置驱动、子网、网关或固定 IP。'
  if (input.devices?.some((device) => !device.hostPath.startsWith('/') || !device.containerPath.startsWith('/'))) return '设备路径必须使用绝对路径。'
  const deviceTargets = input.devices?.map((device) => device.containerPath) ?? []
  if (new Set(deviceTargets).size !== deviceTargets.length) return '同一个容器设备路径不能重复映射。'
  for (const gpu of input.gpus ?? []) {
    if (typeof gpu.count !== 'number' || !Number.isInteger(gpu.count) || (gpu.count !== -1 && (gpu.count < 1 || gpu.count > 128))) return 'GPU 数量必须是 -1（全部）或 1–128 的整数。'
    if (gpu.driver && !/^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$/.test(gpu.driver)) return 'GPU 驱动必须以字母或数字开头，仅含字母、数字、点、下划线和连字符，且不超过 64 个字符。'
    const capabilities = (gpu.capabilities ?? []).map((capability) => capability.toLowerCase())
    if (capabilities.some((capability) => !/^[a-z0-9_]+$/.test(capability))) return 'GPU 能力只能包含字母、数字和下划线。'
    if (new Set(capabilities).size !== capabilities.length) return 'GPU 能力不能重复（不区分大小写）。'
  }
  return ''
}

async function executeSubmit() {
  if (submitting.value || unresolvedAttempt.value) return
  const input = pendingInput.value
  const owner = previewOwner
  if (!input || !owner || !alive || !props.modelValue || owner.opening !== opening || owner.revision !== revision || !isCurrentSession(owner.session)) {
    previewOpen.value = false
    return
  }
  const runContainer = pendingRunContainer.value
  // 不保留环境变量、挂载等完整配置；结果未知属于已提交尝试，不属于可编辑草稿。
  const attempt: UnresolvedAttempt = Object.freeze({ name: input.name || '自动生成', image: input.image, runContainer, time: new Date().toISOString() })
  const currentPage = () => alive && isCurrentSession(owner.session)
  const currentDraft = () => currentPage() && props.modelValue && owner.opening === opening && owner.revision === revision
  // 提交消费预览快照；关闭界面不会取消或重放真实 POST。
  previewOpen.value = false
  submitting.value = runContainer ? 'start' : 'create'
  try {
    const result = await createDockerContainer(input)
    if (!currentPage()) return
    if (currentDraft()) ElMessage.success(result.started ? `容器“${result.name || result.containerId.slice(0, 12)}”已创建并启动` : `容器“${result.name || result.containerId.slice(0, 12)}”已创建`)
    emit('created', result.containerId)
    if (currentDraft()) emit('update:modelValue', false)
  } catch (caught) {
    if (currentPage() && caught instanceof CreateContainerOutcomeUnknownError) {
      unresolvedAttempt.value = attempt
      invalidatePreview()
      const session = captureSession()
      const clearAttempt = () => {
        unresolvedAttempt.value = null
        verificationRead.invalidate()
        verificationLoading.value = false
        verificationInventory.value = null
        verificationError.value = ''
      }
      session.signal.addEventListener('abort', clearAttempt, { once: true })
      releaseAttemptSession = () => session.signal.removeEventListener('abort', clearAttempt)
    } else if (currentDraft()) ElMessage.error(caught instanceof NcpApiError ? caught.message : '容器创建失败，请检查配置后重试。')
  } finally {
    submitting.value = null
  }
}
// 即使共享清单已验证，此处仍仅保留五个展示字段，避免未知尝试额外持有配置。
function projectVerificationInventory(value: unknown): { containers: VerificationContainer[] } {
  const invalid = () => new Error('容器列表格式无效')
  if (!value || typeof value !== 'object' || !('containers' in value) || !Array.isArray(value.containers)) throw invalid()
  const containers = value.containers.map((row: unknown): VerificationContainer => {
    if (!row || typeof row !== 'object' || Array.isArray(row)) throw invalid()
    const item = row as Record<string, unknown>
    if (typeof item.id !== 'string' || typeof item.name !== 'string' || typeof item.image !== 'string' || typeof item.state !== 'string' || typeof item.status !== 'string') throw invalid()
    return { id: item.id, name: item.name, image: item.image, state: item.state, status: item.status }
  })
  return { containers }
}

// 只读观察不证明这次 POST 的结果；空清单、同名对象和读取失败均不解锁创建。
async function refreshUnresolvedInventory() {
  const attempt = unresolvedAttempt.value
  if (!attempt || !alive || !props.modelValue || draftSession === null || !isCurrentSession(draftSession)) return
  const request = verificationRead.begin()
  const current = () => request.isCurrent() && alive && props.modelValue && unresolvedAttempt.value === attempt
  verificationLoading.value = true
  verificationError.value = ''
  verificationInventory.value = null
  try {
    const inventory = await requestDockerInventory(undefined, request.signal)
    if (current()) verificationInventory.value = projectVerificationInventory(inventory)
  } catch {
    if (current()) verificationError.value = '容器列表读取失败，创建结果仍未知。可以再次只读查看。'
  } finally {
    if (current()) verificationLoading.value = false
    request.finish()
  }
}
</script>

<template>
  <ElDrawer
    :model-value="modelValue"
    :title="drawerTitle"
    :size="'min(820px, 96vw)'"
    class="create-container-drawer"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <template #header>
      <div class="drawer-heading"><span><Box :size="21" /></span><div><h2>{{ drawerTitle }}</h2><small>确认前可取消；提交后关闭不会取消容器创建</small></div></div>
    </template>

    <section v-if="unresolvedAttempt" class="unresolved-attempt" aria-label="创建结果未知" role="status">
      <strong>容器可能已经创建，结果尚未确认</strong>
      <p>无法确认本次容器创建是否完成。为避免重复创建，本页已暂停新的创建提交。</p>
      <dl>
        <dt>提交名称</dt><dd>{{ unresolvedAttempt.name }}</dd>
        <dt>提交镜像</dt><dd>{{ unresolvedAttempt.image }}</dd>
        <dt>提交操作</dt><dd>{{ unresolvedAttempt.runContainer ? '创建并启动' : '仅创建' }}</dd>
        <dt>提交时间</dt><dd><time :datetime="unresolvedAttempt.time">{{ unresolvedAttempt.time }}</time></dd>
      </dl>
      <p>只能只读查看列表，不能自动确认本次创建结果；空列表或同名容器都不能证明成功或失败。请人工核查，勿重复提交。</p>
      <p>此提示仅保留在当前页面和登录会话中。关闭抽屉后仍会保留；离开页面不会取消已提交的请求。</p>
      <ActionButton variant="secondary" @click="refreshUnresolvedInventory">只读查看容器列表</ActionButton>
      <p v-if="verificationLoading">正在只读获取容器列表…</p>
      <p v-else-if="verificationError" role="alert">{{ verificationError }}</p>
      <template v-else-if="verificationInventory">
        <p>本次读取 {{ verificationInventory.containers.length }} 个容器；创建结果仍未知。</p>
        <ul class="unresolved-inventory">
          <li v-for="container in verificationInventory.containers" :key="container.id">{{ container.name }} · {{ container.image }} · {{ container.state }} · {{ container.status }}</li>
        </ul>
      </template>
    </section>

    <form class="container-form" @submit.prevent="openSubmitPreview(true)">
      <section class="form-section">
        <SectionHeader title="基础信息" description="设置名称、资源限制和重启策略" :icon="Box" heading-tag="h3" />
        <div class="form-grid">
          <label class="field field--wide"><span>镜像</span><ElInput :model-value="imageReference" disabled /></label>
          <label class="field field--wide"><span>容器名称</span><ElInput v-model="form.name" maxlength="128" placeholder="例如 mysql-1" /></label>
          <label class="field"><span>CPU 限制（核）</span><ElInputNumber v-model="form.cpu" aria-label="CPU 限制（核）" :min="0.01" :max="256" :step="0.25" controls-position="right" placeholder="无限制" /></label>
          <label class="field"><span>内存限制（MiB）</span><ElInputNumber v-model="form.memoryMiB" aria-label="内存限制（MiB）" :min="4" :max="1048576" :step="128" controls-position="right" placeholder="无限制" /></label>
          <label class="field"><span>重启策略</span><NcpSelect :model-value="form.restartPolicy" :options="restartPolicyOptions" accessible-label="容器重启策略" @update:model-value="form.restartPolicy = $event as NonNullable<DockerContainerCreateInput['restartPolicy']>" /></label>
          <label v-if="form.restartPolicy === 'on-failure'" class="field"><span>最大重试次数</span><ElInputNumber v-model="form.restartMaxRetries" aria-label="最大重试次数" :min="0" :max="1000" controls-position="right" /></label>
        </div>
      </section>

      <section class="form-section">
        <SectionHeader title="环境与存储" description="管理环境变量、目录、卷和临时文件系统" :icon="HardDrive" heading-tag="h3" />
        <div class="row-group">
          <div v-for="(row, index) in environment" :key="`env-${index}`" class="repeat-row repeat-row--env">
            <ElInput v-model="row.key" placeholder="变量，例如 APP_MODE" /><ElInput v-model="row.value" placeholder="值" />
            <ElTooltip content="删除环境变量"><button type="button" class="row-remove" :aria-label="`删除第 ${index + 1} 个环境变量`" @click="removeRow(environment, index)"><Trash2 :size="16" /></button></ElTooltip>
          </div>
          <button type="button" class="add-row" @click="addEnvironment"><Plus :size="15" />添加环境变量</button>
        </div>
        <div class="row-group">
          <div v-for="(row, index) in mounts" :key="`mount-${index}`" class="repeat-row repeat-row--mount">
            <NcpSelect :model-value="row.type" :options="mountTypeOptions" :accessible-label="`第 ${index + 1} 个挂载类型`" @update:model-value="setMountType(row, $event)" />
            <div v-if="row.type === 'bind'" class="source-picker">
              <ElInput v-model="row.source" placeholder="/volume2/data" />
              <button type="button" class="browse-button" @click="openPathBrowser(index)">浏览</button>
            </div>
            <NcpSelect v-else-if="row.type === 'volume' && volumeOptions.length" v-model="row.source" :options="volumeOptions" :accessible-label="`第 ${index + 1} 个 Docker 卷`" filterable clearable placeholder="选择已有卷" />
            <ElInput v-else-if="row.type === 'volume'" v-model="row.source" placeholder="volume-name" />
            <span v-else class="row-placeholder">无需来源</span>
            <ElInput v-model="row.target" placeholder="容器路径，例如 /data" />
            <label class="inline-switch"><ElSwitch v-model="row.readOnly" />只读</label>
            <ElTooltip content="删除挂载"><button type="button" class="row-remove" :aria-label="`删除第 ${index + 1} 个挂载`" @click="removeRow(mounts, index)"><Trash2 :size="16" /></button></ElTooltip>
          </div>
          <button type="button" class="add-row" @click="addMount"><Plus :size="15" />添加挂载</button>
        </div>
      </section>

      <section class="form-section">
        <SectionHeader title="网络与端口" description="连接现有网络，或使用子网配置创建或复用专用网络" :icon="Network" heading-tag="h3">
          <template #actions><ElSwitch v-model="form.networkEnabled" aria-label="启用自定义网络" /></template>
        </SectionHeader>
        <div v-if="form.networkEnabled" class="form-grid">
          <label v-if="!form.networkCreateDedicated" class="field field--wide"><span>现有网络</span><NcpSelect :model-value="form.networkName" :options="networkOptions" accessible-label="选择现有 Docker 网络" filterable @update:model-value="setNetworkName" /></label>
          <label class="switch-line network-mode-switch field--wide"><span><strong>创建或复用专用网络（高级）</strong><small>提供子网后，仅在同名网络不存在时创建；已有网络须匹配驱动、子网及显式网关，不会修改现有网络。</small></span><ElSwitch v-model="form.networkCreateDedicated" /></label>
          <template v-if="form.networkCreateDedicated">
            <label class="field"><span>专用网络名称</span><ElInput v-model="form.networkName" placeholder="例如 media-net" /></label>
            <label class="field"><span>驱动</span><ElInput v-model="form.networkDriver" placeholder="默认 bridge" /></label>
            <label class="field"><span>子网</span><ElInput v-model="form.networkSubnet" placeholder="172.30.0.0/24" /></label>
            <label class="field"><span>网关</span><ElInput v-model="form.networkGateway" placeholder="172.30.0.1" /></label>
            <label class="field field--wide"><span>容器固定 IP</span><ElInput v-model="form.networkIp" placeholder="留空自动分配" /></label>
          </template>
        </div>
        <ElAlert v-if="resourcesLoading" title="正在读取 Docker 网络、卷和端口清单…" type="info" :closable="false" />
        <ElAlert v-else-if="resourcesError" :title="resourcesError" type="warning" :closable="false" />
        <ElAlert v-if="portConflictNotice.length" :title="`检测到可能的主机端口冲突：${portConflictNotice.join('、')}`" description="提交时 Root Agent 仍会进行最终校验；可更换主机端口后重试。" type="warning" :closable="false" />
        <div class="row-group">
          <div v-for="(row, index) in ports" :key="`port-${index}`" class="repeat-row repeat-row--port">
            <ElInput v-model="row.hostIp" placeholder="主机 IP（可选）" />
            <ElInputNumber v-model="row.hostPort" :min="0" :max="65535" controls-position="right" placeholder="主机端口" />
            <span class="port-arrow">→</span>
            <ElInputNumber v-model="row.containerPort" :min="1" :max="65535" controls-position="right" placeholder="容器端口" />
            <NcpSelect :model-value="row.protocol" :options="protocolOptions" :accessible-label="`第 ${index + 1} 个端口映射协议`" @update:model-value="row.protocol = $event as PortRow['protocol']" />
            <ElTooltip content="删除端口映射"><button type="button" class="row-remove" :aria-label="`删除第 ${index + 1} 个端口映射`" @click="removeRow(ports, index)"><Trash2 :size="16" /></button></ElTooltip>
          </div>
          <button type="button" class="add-row" @click="addPort"><Plus :size="15" />添加端口映射</button>
        </div>
      </section>

      <section class="form-section">
        <SectionHeader title="启动命令" description="每行填写一个 argv 参数；全空白时使用镜像默认命令；空行忽略，其余参数保留首尾空白，不解析 Shell 语法" :icon="Cpu" heading-tag="h3" />
        <textarea v-model="commandText" aria-label="容器启动参数（每行一个）" rows="4" placeholder="示例：&#10;sh&#10;-c&#10;echo $APP_MODE && exec /app/server"></textarea>
      </section>

      <section class="form-section form-section--advanced">
        <button type="button" class="advanced-toggle" :aria-expanded="advancedOpen" @click="advancedOpen = !advancedOpen"><span class="section-heading__main"><span class="section-heading__icon"><Shield :size="18" /></span><span class="section-heading__copy"><strong>权限与硬件</strong><small>按需配置特权模式、Linux capabilities、设备与 GPU</small></span></span><span>{{ advancedOpen ? '收起' : '展开' }}</span></button>
        <div v-if="advancedOpen" class="advanced-body">
          <label class="switch-line"><span><strong>特权模式</strong><small>容器将获得广泛的宿主机设备访问权限，仅在明确需要时启用。</small></span><ElSwitch v-model="form.privileged" /></label>
          <div class="form-grid">
            <label class="field"><span>增加 Capabilities</span><ElInput v-model="form.capAdd" placeholder="NET_ADMIN,SYS_PTRACE" /></label>
            <label class="field"><span>移除 Capabilities</span><ElInput v-model="form.capDrop" placeholder="SYS_ADMIN" /></label>
          </div>
          <div class="row-group">
            <div v-for="(row, index) in devices" :key="`device-${index}`" class="repeat-row repeat-row--device">
              <ElInput v-model="row.hostPath" placeholder="主机设备 /dev/..." /><ElInput v-model="row.containerPath" placeholder="容器设备 /dev/..." /><ElInput v-model="row.permissions" placeholder="rwm" />
              <ElTooltip content="删除设备"><button type="button" class="row-remove" :aria-label="`删除第 ${index + 1} 个设备`" @click="removeRow(devices, index)"><Trash2 :size="16" /></button></ElTooltip>
            </div>
            <button type="button" class="add-row" @click="addDevice"><Plus :size="15" />添加设备</button>
          </div>
          <label class="switch-line"><span><strong>GPU</strong><small>仅当宿主机已安装兼容运行时和驱动时启用。</small></span><ElSwitch v-model="form.gpuEnabled" /></label>
          <div v-if="form.gpuEnabled" class="form-grid">
            <label class="field"><span>驱动</span><ElInput v-model="form.gpuDriver" placeholder="例如 nvidia；留空自动" /></label>
            <label class="field"><span>数量（-1 表示全部）</span><ElInputNumber v-model="form.gpuCount" aria-label="GPU 数量（-1 表示全部）" :min="-1" :max="128" controls-position="right" /></label>
            <label class="field field--wide"><span>能力</span><ElInput v-model="form.gpuCapabilities" placeholder="gpu,compute,utility" /></label>
          </div>
        </div>
      </section>
    </form>

    <template #footer>
      <div class="drawer-actions">
        <ActionButton variant="secondary" :disabled="Boolean(submitting)" @click="emit('update:modelValue', false)">取消</ActionButton>
        <ActionButton variant="secondary" :loading="submitting === 'create'" :disabled="Boolean(submitting) || Boolean(unresolvedAttempt)" @click="openSubmitPreview(false)">仅创建</ActionButton>
        <ActionButton variant="primary" :loading="submitting === 'start'" :disabled="Boolean(submitting) || Boolean(unresolvedAttempt)" @click="openSubmitPreview(true)">创建并启动</ActionButton>
      </div>
    </template>
  </ElDrawer>

  <!-- 原生 section 为弹窗标题提供局部语义；整体传送到 body，避免抽屉层叠与裁切。 -->
  <Teleport to="body">
  <section class="create-container-dialogs">
  <ElDialog v-model="pathBrowserOpen" title="选择 NAS 路径" width="min(640px, 92vw)">
    <div class="path-browser">
      <div class="path-browser__toolbar">
        <code>{{ currentPath }}</code>
        <button type="button" class="browse-button" :disabled="currentPath === '/' || pathBrowserLoading" @click="loadPath(pathBrowserPage?.parent || '/')">上一级</button>
      </div>
      <ElAlert v-if="pathBrowserError" :title="pathBrowserError" type="error" :closable="false" />
      <div v-if="pathBrowserLoading" class="path-browser__state">正在读取目录…</div>
      <ElEmpty v-else-if="!pathEntries.length" description="当前目录为空" />
      <div v-else class="path-browser__entries">
        <div v-for="entry in pathEntries" :key="entry.path" class="path-entry">
          <button v-if="entry.type === 'directory'" type="button" class="path-entry__name" @click="loadPath(entry.path)"><span class="path-entry__icon">DIR</span>{{ entry.name }}</button>
          <span v-else class="path-entry__name"><span class="path-entry__icon">{{ entry.type === 'symlink' ? 'LINK' : 'FILE' }}</span>{{ entry.name }}</span>
          <button type="button" class="browse-button" :disabled="!entry.readable" @click="choosePath(entry)">选择</button>
        </div>
      </div>
      <button v-if="pathBrowserPage?.nextCursor" type="button" class="load-more" :disabled="pathBrowserLoading" @click="loadPath(currentPath, pathBrowserPage?.nextCursor || '')">加载更多</button>
    </div>
  </ElDialog>

  <ElDialog v-model="previewOpen" class="create-confirmation-dialog" title="确认容器配置" width="min(680px, 92vw)" top="16px">
    <div v-if="previewInput" class="create-preview" role="region" aria-label="容器配置详情" tabindex="0">
      <p class="preview-intro">请确认以下配置；取消不会创建任何 Docker 对象。</p>
      <dl class="preview-grid">
        <dt>镜像</dt><dd><code>{{ previewInput.image }}</code></dd>
        <dt>容器名称</dt><dd>{{ previewLabel(previewInput.name, '自动生成') }}</dd>
        <dt>CPU 限制</dt><dd>{{ previewInput.cpu === undefined ? '未设置限制' : `${previewInput.cpu} 核` }}</dd>
        <dt>内存限制</dt><dd>{{ previewInput.memoryBytes === undefined ? '未设置限制' : `${previewInput.memoryBytes} 字节（${previewInput.memoryBytes / 1024 / 1024} MiB）` }}</dd>
        <dt>重启策略</dt><dd>{{ restartPolicyOptions.find((option) => option.value === (previewInput?.restartPolicy || 'no'))?.label }}（{{ previewInput.restartPolicy || 'no' }}）</dd>
        <template v-if="previewInput.restartPolicy === 'on-failure'">
          <dt>最大重试次数</dt><dd>{{ previewInput.restartMaxRetries || 0 }}{{ previewInput.restartMaxRetries ? ' 次' : '（不限制重试次数）' }}</dd>
        </template>
        <dt>挂载</dt><dd>
          {{ previewInput.mounts?.length || 0 }} 项
          <ol v-if="previewInput.mounts?.length" class="path-mappings" aria-label="挂载映射">
            <li v-for="(mount, index) in previewInput.mounts" :key="index">
              <span>{{ mount.type }} · {{ mount.readOnly ? '只读' : '读写' }}</span>
              <span v-if="mount.type !== 'tmpfs'">{{ mount.type === 'volume' ? '卷名' : '主机路径' }}：<code class="path-preview">{{ JSON.stringify(mount.source) }}</code></span>
              <span v-else>内存挂载，无需来源</span>
              <span>容器路径：<code class="path-preview">{{ JSON.stringify(mount.target) }}</code></span>
            </li>
          </ol>
        </dd>
        <dt>网络</dt><dd><code v-if="previewInput.network">{{ JSON.stringify(previewInput.network.name) }}</code><template v-else>Docker 默认网络</template></dd>
        <template v-if="previewInput.network">
          <dt>网络策略</dt><dd>{{ previewInput.network.subnet ? '仅在同名网络不存在时创建；已有网络须匹配驱动、子网及显式网关，否则失败，不修改现有网络' : '连接现有网络（不创建或修改网络）' }}</dd>
          <template v-if="previewInput.network.subnet">
            <dt>网络驱动</dt><dd><code>{{ previewInput.network.driver ? JSON.stringify(previewInput.network.driver) : 'bridge（默认）' }}</code></dd>
            <dt>子网</dt><dd><code>{{ JSON.stringify(previewInput.network.subnet) }}</code></dd>
            <dt>网关</dt><dd><code v-if="previewInput.network.gateway">{{ JSON.stringify(previewInput.network.gateway) }}</code><template v-else>新建时由 Docker 分配；复用时保留已有网关</template></dd>
            <dt>容器固定 IP</dt><dd><code v-if="previewInput.network.ip">{{ JSON.stringify(previewInput.network.ip) }}</code><template v-else>自动分配</template></dd>
          </template>
        </template>
        <dt>端口</dt><dd>
          <ol v-if="previewInput.ports?.length" class="path-mappings" aria-label="端口映射">
            <li v-for="(port, index) in previewInput.ports" :key="index">
              <span>主机 IP：<code v-if="port.hostIp">{{ port.hostIp }}</code><template v-else>未指定（由 Docker 默认配置决定）</template></span>
              <span>主机端口：{{ port.hostPort || '随机' }}</span>
              <span>容器端口：{{ port.containerPort }}/{{ port.protocol || 'tcp' }}</span>
            </li>
          </ol>
          <template v-else>未映射</template>
        </dd>
        <dt>启动参数</dt><dd><code class="command-preview">{{ previewInput.command?.length ? JSON.stringify(previewInput.command) : '使用镜像默认命令' }}</code></dd>
        <dt>设备映射</dt><dd>
          {{ previewInput.devices?.length || 0 }} 项
          <ol v-if="previewInput.devices?.length" class="path-mappings" aria-label="设备映射">
            <li v-for="(device, index) in previewInput.devices" :key="index">
              <span>主机路径：<code class="path-preview">{{ JSON.stringify(device.hostPath) }}</code></span>
              <span>容器路径：<code class="path-preview">{{ JSON.stringify(device.containerPath) }}</code></span>
              <span>权限：{{ device.cgroupPermissions || 'rwm' }}</span>
            </li>
          </ol>
        </dd>
        <dt>特权模式</dt><dd>{{ previewInput.privileged ? '是（privileged）' : '否' }}</dd>
        <dt>增加 Capabilities</dt><dd><code v-if="previewInput.capAdd?.length" class="capability-preview">{{ JSON.stringify(previewInput.capAdd) }}</code><template v-else>无</template></dd>
        <dt>移除 Capabilities</dt><dd><code v-if="previewInput.capDrop?.length" class="capability-preview">{{ JSON.stringify(previewInput.capDrop) }}</code><template v-else>无</template></dd>
        <dt>硬件请求</dt><dd>设备映射：{{ previewInput.devices?.length || 0 }} 项；GPU 请求：{{ previewInput.gpus?.length || 0 }} 项</dd>
      </dl>
      <p v-if="previewInput.mounts?.length || previewInput.devices?.length" class="preview-intro">路径和卷名以双引号标示边界，保留空格；反斜杠、引号和控制字符按 JSON 转义显示。</p>
    </div>
    <template #footer>
      <div class="dialog-actions"><ActionButton variant="secondary" :disabled="Boolean(submitting)" @click="previewOpen = false">返回修改</ActionButton><ActionButton variant="primary" :loading="Boolean(submitting)" :disabled="Boolean(unresolvedAttempt)" @click="executeSubmit">确认{{ pendingRunContainer ? '创建并启动' : '创建' }}</ActionButton></div>
    </template>
  </ElDialog>
  </section>
  </Teleport>
</template>

<style scoped>
.unresolved-attempt{display:grid;gap:10px;padding:16px;margin-bottom:14px;border:1px solid var(--ncp-line-strong);border-radius:12px;background:var(--ncp-surface-quiet);overflow-wrap:anywhere}.unresolved-attempt p{margin:0;font-size:.8rem;line-height:1.6}.unresolved-attempt dl{display:grid;grid-template-columns:80px minmax(0,1fr);gap:6px;margin:0;font-size:.78rem}.unresolved-attempt dd{margin:0}.unresolved-attempt .action-button{justify-self:start}.unresolved-inventory{max-height:240px;overflow:auto;margin:0;padding-left:20px;font-size:.78rem;line-height:1.7}

.drawer-heading{display:flex;align-items:center;gap:11px}.drawer-heading>span{display:grid;width:42px;height:42px;place-items:center;border-radius:12px;background:var(--ncp-primary-soft);color:var(--ncp-primary-strong)}.drawer-heading>div{display:grid;gap:2px}.drawer-heading h2{margin:0;font-size:.96rem}.drawer-heading small{color:var(--ncp-text-subtle);font-size:.72rem}
.container-form{display:grid;gap:12px}.form-section{display:grid;gap:15px;padding:17px 18px;border:1px solid var(--ncp-line);border-radius:14px;background:#fff}.section-heading{display:flex;align-items:center;justify-content:space-between;gap:14px}.section-heading__main{display:flex;min-width:0;align-items:center;gap:11px;color:var(--ncp-primary-strong)}.section-heading__icon{display:grid;width:34px;height:34px;flex:0 0 auto;place-items:center;border-radius:10px;background:var(--ncp-primary-soft);color:var(--ncp-primary-strong)}.section-heading__copy{display:grid;min-width:0;gap:4px}.section-heading strong,.advanced-toggle strong{color:var(--ncp-text);font-size:.86rem;line-height:1.25}.section-heading small,.advanced-toggle small{color:var(--ncp-text-subtle);font-size:.7rem;font-weight:500;line-height:1.4}
.form-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:13px}.field{display:grid;min-width:0;gap:7px}.field--wide{grid-column:1/-1}.field>span{color:var(--ncp-text-muted);font-size:.72rem;font-weight:700}.field :deep(.ncp-select),.repeat-row :deep(.ncp-select){width:100%;min-width:0}
.row-group{display:grid;gap:8px}.repeat-row{display:grid;align-items:center;gap:8px}.repeat-row--env{grid-template-columns:minmax(150px,.8fr) minmax(220px,1.4fr) 40px}.repeat-row--mount{grid-template-columns:120px minmax(180px,1fr) minmax(150px,1fr) 82px 40px}.repeat-row--port{grid-template-columns:minmax(140px,1fr) 140px 22px 140px 90px 40px}.repeat-row--device{grid-template-columns:1fr 1fr 100px 40px}.source-picker{display:grid;grid-template-columns:minmax(0,1fr) auto;gap:6px;min-width:0}.browse-button,.load-more{min-height:40px;padding:0 11px;border:1px solid var(--ncp-control-border);border-radius:10px;background:var(--ncp-control-surface);color:var(--ncp-primary-strong);font-size:.72rem;font-weight:700;white-space:nowrap}.browse-button:hover,.load-more:hover{background:var(--ncp-primary-soft)}.browse-button:focus-visible,.load-more:focus-visible,.path-entry__name:focus-visible{outline:2px solid var(--ncp-focus-ring);outline-offset:2px}.browse-button:disabled,.load-more:disabled{cursor:not-allowed;opacity:.55}.row-remove{display:grid;width:40px;height:40px;place-items:center;border:1px solid var(--ncp-danger-border);border-radius:10px;background:var(--ncp-danger-soft);color:var(--ncp-danger-strong)}.add-row{display:flex;width:max-content;min-height:34px;align-items:center;gap:6px;padding:0 10px;border-radius:9px;background:var(--ncp-primary-soft);color:var(--ncp-primary-strong);font-size:.7rem;font-weight:750}.row-placeholder{display:flex;min-height:40px;align-items:center;padding:0 11px;border:1px dashed var(--ncp-line-strong);border-radius:10px;color:var(--ncp-text-subtle);font-size:.7rem}.inline-switch{display:flex;align-items:center;gap:7px;color:var(--ncp-text-muted);font-size:.7rem}.network-mode-switch{align-items:center}.port-arrow{text-align:center;color:var(--ncp-text-subtle)}
textarea{width:100%;resize:vertical;padding:11px 12px;border:1px solid var(--ncp-control-border);border-radius:10px;background:var(--ncp-control-surface);color:var(--ncp-text);font-family:'JetBrains Mono Variable',monospace;font-size:.76rem;line-height:1.55;outline:none}textarea:focus{border-color:var(--ncp-primary);box-shadow:0 0 0 3px var(--ncp-primary-soft)}
.form-section--advanced{padding:0;overflow:hidden}.advanced-toggle{background:var(--ncp-control-surface);display:flex;width:100%;align-items:center;justify-content:space-between;gap:12px;padding:15px 18px;text-align:left}.advanced-toggle>span:last-child{color:var(--ncp-primary-strong);font-size:.7rem;font-weight:750}.advanced-body{display:grid;gap:13px;padding:0 18px 17px;border-top:1px solid var(--ncp-line)}.switch-line{display:flex;align-items:center;justify-content:space-between;gap:14px;padding-top:13px}.switch-line>span{display:grid;gap:2px}.switch-line strong{font-size:.76rem}.switch-line small{color:var(--ncp-text-subtle);font-size:.68rem}
.drawer-actions{display:flex;justify-content:flex-end;gap:8px}
.path-browser{display:grid;gap:12px}.path-browser__toolbar{display:flex;align-items:center;justify-content:space-between;gap:10px;padding:9px 11px;border:1px solid var(--ncp-line);border-radius:10px;background:var(--ncp-surface-quiet)}.path-browser__toolbar code{min-width:0;overflow:hidden;color:var(--ncp-text);font-size:.74rem;text-overflow:ellipsis;white-space:nowrap}.path-browser__state{padding:30px;text-align:center;color:var(--ncp-text-subtle);font-size:.78rem}.path-browser__entries{display:grid;max-height:360px;overflow:auto;border:1px solid var(--ncp-line);border-radius:10px}.path-entry{display:flex;min-width:0;align-items:center;justify-content:space-between;gap:10px;padding:8px 10px;border-bottom:1px solid var(--ncp-line)}.path-entry:last-child{border-bottom:0}.path-entry__name{display:flex;min-width:0;align-items:center;gap:8px;color:var(--ncp-text);font-size:.78rem;overflow:hidden;text-align:left;text-overflow:ellipsis;white-space:nowrap}.path-entry__icon{display:inline-grid;min-width:38px;height:22px;place-items:center;border-radius:6px;background:var(--ncp-primary-soft);color:var(--ncp-primary-strong);font-size:.58rem;font-weight:800;letter-spacing:.03em}.path-entry>span.path-entry__name .path-entry__icon{background:var(--ncp-surface-quiet);color:var(--ncp-text-subtle)}.load-more{width:100%}.create-preview{display:grid;gap:12px}.preview-intro{margin:0;color:var(--ncp-text-muted);font-size:.78rem}.preview-grid{display:grid;grid-template-columns:110px minmax(0,1fr);gap:0;margin:0;border:1px solid var(--ncp-line);border-radius:10px;overflow:hidden}.preview-grid dt,.preview-grid dd{margin:0;padding:10px 11px;border-bottom:1px solid var(--ncp-line);font-size:.76rem;line-height:1.45}.preview-grid dt{background:var(--ncp-surface-quiet);color:var(--ncp-text-muted);font-weight:750}.preview-grid dd{min-width:0;overflow-wrap:anywhere}.preview-grid dt:last-of-type,.preview-grid dd:last-of-type{border-bottom:0}.preview-grid code{font-family:'JetBrains Mono Variable',monospace;font-size:.7rem}.dialog-actions{display:flex;justify-content:flex-end;gap:8px}
.command-preview{white-space:pre-wrap}
.capability-preview{white-space:pre-wrap;overflow-wrap:anywhere}
.path-preview{display:block;white-space:break-spaces;overflow-wrap:anywhere}
.path-mappings{display:grid;gap:10px;margin:8px 0 0;padding-left:20px}.path-mappings li{min-width:0}.path-mappings li>span{display:block}.path-mappings li+li{padding-top:10px;border-top:1px solid var(--ncp-line)}
@media(max-width:760px){.form-grid{grid-template-columns:1fr}.field--wide{grid-column:auto}.repeat-row{grid-template-columns:1fr}.row-remove{justify-self:end}.repeat-row--port .port-arrow{display:none}.drawer-actions{display:grid;grid-template-columns:1fr 1fr}.drawer-actions>:last-child{grid-column:1/-1}.inline-switch{min-height:40px}.form-section{padding:14px}}
</style>

<style>
/* 确认内容独立滚动，标题和操作始终位于视口内；保留 Element Plus 的模态与焦点管理。 */
.create-confirmation-dialog.el-dialog{display:flex;flex-direction:column;max-height:calc(100vh - 32px);max-height:calc(100dvh - 32px);margin-bottom:16px;box-sizing:border-box}
.create-confirmation-dialog .el-dialog__header,.create-confirmation-dialog .el-dialog__footer{flex:none}
.create-confirmation-dialog .el-dialog__body{display:flex;flex-direction:column;min-height:0;overflow:hidden}
.create-confirmation-dialog .create-preview{grid-auto-rows:max-content;min-height:0;overflow:auto;overscroll-behavior:contain;scrollbar-gutter:stable}
.create-confirmation-dialog .create-preview:focus-visible{outline:2px solid var(--ncp-primary);outline-offset:-2px}
.create-confirmation-dialog .dialog-actions{flex-wrap:wrap}
.create-container-drawer .el-drawer__header{margin-bottom:0;padding:18px 20px 14px;border-bottom:1px solid var(--ncp-line)}.create-container-drawer .el-drawer__body{padding:14px 18px;background:var(--ncp-surface-quiet)}.create-container-drawer .el-drawer__footer{padding:13px 20px;border-top:1px solid var(--ncp-line)}.create-container-drawer .el-input-number{width:100%}
</style>
