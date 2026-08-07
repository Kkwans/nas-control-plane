<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { Box, Cpu, HardDrive, Network, Plus, Shield, Trash2 } from '@lucide/vue'
import { ElDrawer, ElInput, ElInputNumber, ElMessage, ElSwitch, ElTooltip } from 'element-plus'

import {
  NcpApiError,
  createDockerContainer,
  type DockerContainerCreateInput,
  type DockerImageSummary,
} from '@/api/system'
import ActionButton from '@/components/ActionButton.vue'

type EnvironmentRow = { key: string; value: string }
type MountRow = { type: 'bind' | 'volume' | 'tmpfs'; source: string; target: string; readOnly: boolean }
type PortRow = { hostIp: string; hostPort: number | undefined; containerPort: number | undefined; protocol: 'tcp' | 'udp' | 'sctp' }
type DeviceRow = { hostPath: string; containerPath: string; permissions: string }

const props = defineProps<{ modelValue: boolean; image: DockerImageSummary | null }>()
const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  created: [containerId: string]
}>()

const submitting = ref<'create' | 'start' | null>(null)
const advancedOpen = ref(false)
const environment = ref<EnvironmentRow[]>([])
const mounts = ref<MountRow[]>([])
const ports = ref<PortRow[]>([])
const devices = ref<DeviceRow[]>([])
const commandText = ref('')
const form = reactive({
  name: '',
  cpu: undefined as number | undefined,
  memoryMiB: undefined as number | undefined,
  restartPolicy: 'no' as DockerContainerCreateInput['restartPolicy'],
  restartMaxRetries: 0,
  networkEnabled: false,
  networkName: 'bridge',
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

watch(() => props.modelValue, (open) => {
  if (open) resetForm()
})

function resetForm() {
  form.name = suggestedName(imageReference.value)
  form.cpu = undefined
  form.memoryMiB = undefined
  form.restartPolicy = 'no'
  form.restartMaxRetries = 0
  form.networkEnabled = false
  form.networkName = 'bridge'
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
}

function suggestedName(reference: string) {
  const repository = reference.split('@')[0]?.split(':')[0]?.split('/').at(-1) || 'container'
  return repository.replace(/[^a-zA-Z0-9_.-]+/g, '-').slice(0, 100)
}

function addEnvironment() { environment.value.push({ key: '', value: '' }) }
function addMount() { mounts.value.push({ type: 'bind', source: '', target: '', readOnly: false }) }
function addPort() { ports.value.push({ hostIp: '', hostPort: undefined, containerPort: undefined, protocol: 'tcp' }) }
function addDevice() { devices.value.push({ hostPath: '', containerPath: '', permissions: 'rwm' }) }

function removeRow<T>(rows: T[], index: number) { rows.splice(index, 1) }

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
      source: row.type === 'tmpfs' ? undefined : row.source.trim(),
      target: row.target.trim(),
      readOnly: row.readOnly,
    })),
    ports: ports.value.map((row) => ({
      hostIp: row.hostIp.trim() || undefined,
      hostPort: row.hostPort,
      containerPort: Number(row.containerPort),
      protocol: row.protocol,
    })),
    command: commandText.value.trim() ? commandText.value.split('\n').map((item) => item.trim()).filter(Boolean) : undefined,
    privileged: form.privileged,
    capAdd: commaList(form.capAdd),
    capDrop: commaList(form.capDrop),
    devices: devices.value.map((row) => ({
      hostPath: row.hostPath.trim(),
      containerPath: row.containerPath.trim(),
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
      driver: form.networkDriver.trim() || undefined,
      subnet: form.networkSubnet.trim() || undefined,
      gateway: form.networkGateway.trim() || undefined,
      ip: form.networkIp.trim() || undefined,
    }
  }
  return input
}

function validationMessage(input: DockerContainerCreateInput) {
  if (!input.image) return '镜像引用不能为空。'
  if (input.name && !/^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$/.test(input.name)) return '容器名称只能包含字母、数字、点、下划线和连字符。'
  const rawEnvironmentKeys = environment.value.map((row) => row.key.trim()).filter(Boolean)
  if (environment.value.some((row) => !row.key.trim() && row.value) || rawEnvironmentKeys.some((key) => !/^[A-Za-z_][A-Za-z0-9_]*$/.test(key)) || new Set(rawEnvironmentKeys).size !== rawEnvironmentKeys.length) return '环境变量名称无效、缺失或重复。'
  if (input.mounts?.some((mount) => !mount.target.startsWith('/') || (mount.type !== 'tmpfs' && !mount.source))) return '挂载项必须填写来源和绝对目标路径。'
  const mountTargets = input.mounts?.map((mount) => mount.target) ?? []
  if (new Set(mountTargets).size !== mountTargets.length) return '同一个容器路径不能重复挂载。'
  if (input.ports?.some((port) => !Number.isInteger(port.containerPort) || port.containerPort < 1 || port.containerPort > 65535)) return '容器端口必须是 1–65535 的整数。'
  const portTargets = input.ports?.map((port) => `${port.containerPort}/${port.protocol ?? 'tcp'}`) ?? []
  if (new Set(portTargets).size !== portTargets.length) return '同一个容器端口和协议不能重复映射。'
  if (input.network && !input.network.name) return '启用网络配置后必须填写网络名称。'
  if (input.network && ['host', 'none'].includes(input.network.name) && (input.network.driver || input.network.subnet || input.network.gateway || input.network.ip)) return 'host 和 none 网络不能配置驱动、子网、网关或固定 IP。'
  if (input.devices?.some((device) => !device.hostPath.startsWith('/') || !device.containerPath.startsWith('/'))) return '设备路径必须使用绝对路径。'
  const deviceTargets = input.devices?.map((device) => device.containerPath) ?? []
  if (new Set(deviceTargets).size !== deviceTargets.length) return '同一个容器设备路径不能重复映射。'
  return ''
}

async function submit(runContainer: boolean) {
  if (submitting.value) return
  const input = buildInput(runContainer)
  const message = validationMessage(input)
  if (message) {
    ElMessage.warning(message)
    return
  }
  submitting.value = runContainer ? 'start' : 'create'
  try {
    const result = await createDockerContainer(input)
    ElMessage.success(result.started ? `容器“${result.name || result.containerId.slice(0, 12)}”已创建并启动` : `容器“${result.name || result.containerId.slice(0, 12)}”已创建`)
    emit('created', result.containerId)
    emit('update:modelValue', false)
  } catch (caught) {
    ElMessage.error(caught instanceof NcpApiError ? caught.message : '容器创建失败，请检查配置后重试。')
  } finally {
    submitting.value = null
  }
}
</script>

<template>
  <ElDrawer
    :model-value="modelValue"
    :size="'min(820px, 96vw)'"
    class="create-container-drawer"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <template #header>
      <div class="drawer-heading"><span><Box :size="21" /></span><div><strong>{{ drawerTitle }}</strong><small>取消不会创建任何 Docker 对象</small></div></div>
    </template>

    <form class="container-form" @submit.prevent="submit(true)">
      <section class="form-section">
        <header><div><Box :size="18" /><span><strong>基础信息</strong><small>名称、资源限制和重启策略</small></span></div></header>
        <div class="form-grid">
          <label class="field field--wide"><span>镜像</span><ElInput :model-value="imageReference" disabled /></label>
          <label class="field field--wide"><span>容器名称</span><ElInput v-model="form.name" maxlength="128" placeholder="例如 mysql-1" /></label>
          <label class="field"><span>CPU 限制（核）</span><ElInputNumber v-model="form.cpu" :min="0.01" :max="256" :step="0.25" controls-position="right" placeholder="无限制" /></label>
          <label class="field"><span>内存限制（MiB）</span><ElInputNumber v-model="form.memoryMiB" :min="4" :max="1048576" :step="128" controls-position="right" placeholder="无限制" /></label>
          <label class="field"><span>重启策略</span><select v-model="form.restartPolicy"><option value="no">不自动重启</option><option value="unless-stopped">除非手动停止</option><option value="always">始终重启</option><option value="on-failure">失败时重启</option></select></label>
          <label v-if="form.restartPolicy === 'on-failure'" class="field"><span>最大重试次数</span><ElInputNumber v-model="form.restartMaxRetries" :min="0" :max="1000" controls-position="right" /></label>
        </div>
      </section>

      <section class="form-section">
        <header><div><HardDrive :size="18" /><span><strong>环境与存储</strong><small>变量、目录、卷和临时文件系统</small></span></div></header>
        <div class="row-group">
          <div v-for="(row, index) in environment" :key="`env-${index}`" class="repeat-row repeat-row--env">
            <ElInput v-model="row.key" placeholder="变量，例如 APP_MODE" /><ElInput v-model="row.value" placeholder="值" />
            <ElTooltip content="删除环境变量"><button type="button" class="row-remove" @click="removeRow(environment, index)"><Trash2 :size="16" /></button></ElTooltip>
          </div>
          <button type="button" class="add-row" @click="addEnvironment"><Plus :size="15" />添加环境变量</button>
        </div>
        <div class="row-group">
          <div v-for="(row, index) in mounts" :key="`mount-${index}`" class="repeat-row repeat-row--mount">
            <select v-model="row.type"><option value="bind">主机目录</option><option value="volume">Docker 卷</option><option value="tmpfs">临时目录</option></select>
            <ElInput v-if="row.type !== 'tmpfs'" v-model="row.source" :placeholder="row.type === 'bind' ? '/volume2/data' : 'volume-name'" />
            <span v-else class="row-placeholder">无需来源</span>
            <ElInput v-model="row.target" placeholder="容器路径，例如 /data" />
            <label class="inline-switch"><ElSwitch v-model="row.readOnly" />只读</label>
            <ElTooltip content="删除挂载"><button type="button" class="row-remove" @click="removeRow(mounts, index)"><Trash2 :size="16" /></button></ElTooltip>
          </div>
          <button type="button" class="add-row" @click="addMount"><Plus :size="15" />添加挂载</button>
        </div>
      </section>

      <section class="form-section">
        <header><div><Network :size="18" /><span><strong>网络与端口</strong><small>连接现有网络，或用子网配置创建专用网络</small></span></div><ElSwitch v-model="form.networkEnabled" /></header>
        <div v-if="form.networkEnabled" class="form-grid">
          <label class="field"><span>网络名称</span><ElInput v-model="form.networkName" placeholder="bridge" /></label>
          <label class="field"><span>驱动</span><ElInput v-model="form.networkDriver" placeholder="默认 bridge" /></label>
          <label class="field"><span>子网</span><ElInput v-model="form.networkSubnet" placeholder="172.30.0.0/24" /></label>
          <label class="field"><span>网关</span><ElInput v-model="form.networkGateway" placeholder="172.30.0.1" /></label>
          <label class="field field--wide"><span>容器固定 IP</span><ElInput v-model="form.networkIp" placeholder="留空自动分配" /></label>
        </div>
        <div class="row-group">
          <div v-for="(row, index) in ports" :key="`port-${index}`" class="repeat-row repeat-row--port">
            <ElInput v-model="row.hostIp" placeholder="主机 IP（可选）" />
            <ElInputNumber v-model="row.hostPort" :min="0" :max="65535" controls-position="right" placeholder="主机端口" />
            <span class="port-arrow">→</span>
            <ElInputNumber v-model="row.containerPort" :min="1" :max="65535" controls-position="right" placeholder="容器端口" />
            <select v-model="row.protocol"><option value="tcp">TCP</option><option value="udp">UDP</option><option value="sctp">SCTP</option></select>
            <ElTooltip content="删除端口映射"><button type="button" class="row-remove" @click="removeRow(ports, index)"><Trash2 :size="16" /></button></ElTooltip>
          </div>
          <button type="button" class="add-row" @click="addPort"><Plus :size="15" />添加端口映射</button>
        </div>
      </section>

      <section class="form-section">
        <header><div><Cpu :size="18" /><span><strong>启动命令</strong><small>每行一个 argv 参数；可保留镜像默认命令</small></span></div></header>
        <textarea v-model="commandText" rows="4" placeholder="示例：&#10;sh&#10;-c&#10;echo $APP_MODE && exec /app/server"></textarea>
      </section>

      <section class="form-section form-section--advanced">
        <button type="button" class="advanced-toggle" @click="advancedOpen = !advancedOpen"><span><Shield :size="18" /><span><strong>权限与硬件</strong><small>特权模式、Linux capabilities、设备与 GPU</small></span></span><span>{{ advancedOpen ? '收起' : '展开' }}</span></button>
        <div v-if="advancedOpen" class="advanced-body">
          <label class="switch-line"><span><strong>特权模式</strong><small>容器将获得广泛的宿主机设备访问权限，仅在明确需要时启用。</small></span><ElSwitch v-model="form.privileged" /></label>
          <div class="form-grid">
            <label class="field"><span>增加 Capabilities</span><ElInput v-model="form.capAdd" placeholder="NET_ADMIN,SYS_PTRACE" /></label>
            <label class="field"><span>移除 Capabilities</span><ElInput v-model="form.capDrop" placeholder="SYS_ADMIN" /></label>
          </div>
          <div class="row-group">
            <div v-for="(row, index) in devices" :key="`device-${index}`" class="repeat-row repeat-row--device">
              <ElInput v-model="row.hostPath" placeholder="主机设备 /dev/..." /><ElInput v-model="row.containerPath" placeholder="容器设备 /dev/..." /><ElInput v-model="row.permissions" placeholder="rwm" />
              <ElTooltip content="删除设备"><button type="button" class="row-remove" @click="removeRow(devices, index)"><Trash2 :size="16" /></button></ElTooltip>
            </div>
            <button type="button" class="add-row" @click="addDevice"><Plus :size="15" />添加设备</button>
          </div>
          <label class="switch-line"><span><strong>GPU</strong><small>仅当宿主机已安装兼容运行时和驱动时启用。</small></span><ElSwitch v-model="form.gpuEnabled" /></label>
          <div v-if="form.gpuEnabled" class="form-grid">
            <label class="field"><span>驱动</span><ElInput v-model="form.gpuDriver" placeholder="例如 nvidia；留空自动" /></label>
            <label class="field"><span>数量</span><ElInputNumber v-model="form.gpuCount" :min="-1" :max="128" controls-position="right" /></label>
            <label class="field field--wide"><span>能力</span><ElInput v-model="form.gpuCapabilities" placeholder="gpu,compute,utility" /></label>
          </div>
        </div>
      </section>
    </form>

    <template #footer>
      <div class="drawer-actions">
        <ActionButton variant="secondary" :disabled="Boolean(submitting)" @click="emit('update:modelValue', false)">取消</ActionButton>
        <ActionButton variant="secondary" :loading="submitting === 'create'" :disabled="Boolean(submitting)" @click="submit(false)">仅创建</ActionButton>
        <ActionButton variant="primary" :loading="submitting === 'start'" :disabled="Boolean(submitting)" @click="submit(true)">创建并启动</ActionButton>
      </div>
    </template>
  </ElDrawer>
</template>

<style scoped>
.drawer-heading{display:flex;align-items:center;gap:11px}.drawer-heading>span{display:grid;width:42px;height:42px;place-items:center;border-radius:12px;background:var(--ncp-primary-soft);color:var(--ncp-primary-strong)}.drawer-heading>div{display:grid;gap:2px}.drawer-heading strong{font-size:.96rem}.drawer-heading small{color:var(--ncp-text-subtle);font-size:.72rem}
.container-form{display:grid;gap:14px}.form-section{display:grid;gap:13px;padding:16px;border:1px solid var(--ncp-line);border-radius:14px;background:#fff}.form-section>header{display:flex;align-items:center;justify-content:space-between;gap:12px}.form-section>header>div{display:flex;align-items:center;gap:9px;color:var(--ncp-primary-strong)}.form-section>header span span,.advanced-toggle>span:first-child>span{display:grid;gap:2px}.form-section header strong,.advanced-toggle strong{color:var(--ncp-text);font-size:.84rem}.form-section header small,.advanced-toggle small{color:var(--ncp-text-subtle);font-size:.68rem;font-weight:500}
.form-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:12px}.field{display:grid;gap:6px}.field--wide{grid-column:1/-1}.field>span{color:var(--ncp-text-muted);font-size:.72rem;font-weight:700}.field select,.repeat-row select{width:100%;min-height:40px;padding:0 11px;border:1px solid var(--ncp-control-border);border-radius:var(--ncp-radius-control);background:var(--ncp-control-surface);color:var(--ncp-text);font:inherit}
.row-group{display:grid;gap:8px}.repeat-row{display:grid;align-items:center;gap:8px}.repeat-row--env{grid-template-columns:minmax(150px,.8fr) minmax(220px,1.4fr) 40px}.repeat-row--mount{grid-template-columns:120px minmax(150px,1fr) minmax(150px,1fr) 82px 40px}.repeat-row--port{grid-template-columns:minmax(140px,1fr) 140px 22px 140px 90px 40px}.repeat-row--device{grid-template-columns:1fr 1fr 100px 40px}.row-remove{display:grid;width:40px;height:40px;place-items:center;border:1px solid var(--ncp-danger-border);border-radius:10px;background:var(--ncp-danger-soft);color:var(--ncp-danger-strong)}.add-row{display:flex;width:max-content;min-height:34px;align-items:center;gap:6px;padding:0 10px;border-radius:9px;background:var(--ncp-primary-soft);color:var(--ncp-primary-strong);font-size:.7rem;font-weight:750}.row-placeholder{display:flex;min-height:40px;align-items:center;padding:0 11px;border:1px dashed var(--ncp-line-strong);border-radius:10px;color:var(--ncp-text-subtle);font-size:.7rem}.inline-switch{display:flex;align-items:center;gap:7px;color:var(--ncp-text-muted);font-size:.7rem}.port-arrow{text-align:center;color:var(--ncp-text-subtle)}
textarea{width:100%;resize:vertical;padding:11px 12px;border:1px solid var(--ncp-control-border);border-radius:10px;background:var(--ncp-control-surface);color:var(--ncp-text);font-family:'JetBrains Mono Variable',monospace;font-size:.76rem;line-height:1.55;outline:none}textarea:focus{border-color:var(--ncp-primary);box-shadow:0 0 0 3px var(--ncp-primary-soft)}
.form-section--advanced{padding:0;overflow:hidden}.advanced-toggle{display:flex;width:100%;align-items:center;justify-content:space-between;gap:12px;padding:15px 16px;text-align:left}.advanced-toggle>span:first-child{display:flex;align-items:center;gap:9px;color:var(--ncp-primary-strong)}.advanced-toggle>span:last-child{color:var(--ncp-primary-strong);font-size:.7rem;font-weight:750}.advanced-body{display:grid;gap:13px;padding:0 16px 16px;border-top:1px solid var(--ncp-line)}.switch-line{display:flex;align-items:center;justify-content:space-between;gap:14px;padding-top:13px}.switch-line>span{display:grid;gap:2px}.switch-line strong{font-size:.76rem}.switch-line small{color:var(--ncp-text-subtle);font-size:.68rem}
.drawer-actions{display:flex;justify-content:flex-end;gap:8px}
@media(max-width:760px){.form-grid{grid-template-columns:1fr}.field--wide{grid-column:auto}.repeat-row{grid-template-columns:1fr}.row-remove{justify-self:end}.repeat-row--port .port-arrow{display:none}.drawer-actions{display:grid;grid-template-columns:1fr 1fr}.drawer-actions>:last-child{grid-column:1/-1}.inline-switch{min-height:40px}.form-section{padding:14px}}
</style>

<style>
.create-container-drawer .el-drawer__header{margin-bottom:0;padding:18px 20px 14px;border-bottom:1px solid var(--ncp-line)}.create-container-drawer .el-drawer__body{padding:16px 20px;background:var(--ncp-surface-quiet)}.create-container-drawer .el-drawer__footer{padding:13px 20px;border-top:1px solid var(--ncp-line)}.create-container-drawer .el-input-number{width:100%}
</style>
