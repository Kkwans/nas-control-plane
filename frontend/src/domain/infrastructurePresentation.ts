import type { SystemDetails } from '@/api/system'

type NetworkInterface = SystemDetails['network']['interfaces'][number]
type ListeningPort = SystemDetails['network']['listeningPorts'][number]
type Disk = SystemDetails['storage']['disks'][number]

export function formatBytes(value: number) {
  if (!Number.isFinite(value) || value <= 0) return '—'
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let size = value
  let index = 0
  while (size >= 1024 && index < units.length - 1) {
    size /= 1024
    index += 1
  }
  return `${size >= 100 || index === 0 ? size.toFixed(0) : size.toFixed(1)} ${units[index]}`
}

export function formatDuration(seconds: number) {
  if (!Number.isFinite(seconds) || seconds <= 0) return '—'
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor(seconds % 86400 / 3600)
  const minutes = Math.floor(seconds % 3600 / 60)
  return [days ? `${days} 天` : '', hours ? `${hours} 小时` : '', !days && minutes ? `${minutes} 分钟` : ''].filter(Boolean).join(' ')
}

export function interfaceIsOnline(item: NetworkInterface) {
  if (item.lowerUpKnown === true) return item.lowerUp === true && (!item.state || item.state === 'up' || item.state === 'unknown')
  return item.state === 'up'
}

export function interfaceStateLabel(item: NetworkInterface) {
  if (interfaceIsOnline(item) && /^tailscale/i.test(item.name)) return 'Overlay 可用'
  return interfaceIsOnline(item) ? '已连接' : '未连接'
}

export function interfaceAddress(item: NetworkInterface) {
  return item.addresses.find((address) => address.family === 'ipv4') ?? item.addresses[0]
}

export function listeningPortOwners(item: ListeningPort) {
  if (item.containerName) {
    return [{ label: item.containerName, detail: item.processName ? `Docker 容器 · ${item.processName}` : 'Docker 容器' }]
  }
  if (item.containerId) {
    return [{ label: shortIdentifier(item.containerId), detail: `Docker 容器名未知 · ${portDetectionReason(item)}` }]
  }
  if (item.systemdUnit) {
    return [{ label: item.systemdUnit.replace(/\.service$/, ''), detail: item.processName ? `系统服务 · ${item.processName}` : 'systemd 服务' }]
  }
  if (item.processName) return [{ label: item.processName, detail: item.service && item.service !== item.processName ? item.service : '进程' }]
  if (item.executable) return [{ label: item.executable, detail: '可执行文件' }]
  return [{ label: item.pid > 0 ? `PID ${item.pid}` : '未知监听者', detail: portDetectionReason(item) }]
}

function shortIdentifier(value: string) {
  return value.length > 12 ? value.slice(0, 12) : value
}

function portDetectionReason(item: ListeningPort) {
  switch (item.detectionErrorCode) {
    case 'LISTENING_PORT_PID_UNAVAILABLE': return 'PID 不可用'
    case 'LISTENING_PORT_CONTAINER_NAME_UNAVAILABLE': return 'Docker CLI 未返回容器名称'
    case 'LISTENING_PORT_PROCESS_METADATA_UNAVAILABLE': return 'proc 元数据不可读'
    case 'LISTENING_PORT_PROCESS_METADATA_EMPTY': return 'proc 元数据为空'
    case 'LISTENING_PORT_PROCESS_METADATA_PARTIAL': return 'proc 元数据不完整'
    default: return item.detectionErrorCode || '映射原因未知'
  }
}

export function proxyStateLabel(value: string, detected: boolean) {
  if (detected && value === 'running') return '代理核心运行中'
  if (value === 'not-found') return '未发现代理核心'
  return detected ? '已发现代理核心' : '代理状态未知'
}

function diskKind(rotational: boolean) {
  return rotational ? '机械硬盘' : '固态 / 闪存'
}

export function blockDeviceKindLabel(disk: Disk) {
  switch (disk.kind) {
    case 'physical': return disk.rotational ? '机械数据盘' : '固态数据盘'
    case 'emmc': return '系统 eMMC'
    case 'emmc-boot': return 'eMMC 启动区'
    case 'compressed-memory': return '压缩内存交换设备'
    case 'virtual': return '系统虚拟设备'
    default: return diskKind(disk.rotational)
  }
}

export function blockDeviceDescription(disk: Disk) {
  return disk.description || `${blockDeviceKindLabel(disk)}，用途由系统管理`
}

export function blockDeviceTransport(disk: Disk) {
  if (!disk.transport) return '接口未知'
  if (disk.transport === 'memory') return '内存'
  if (disk.transport === 'emmc') return 'eMMC'
  if (disk.transport === 'block') return '块设备'
  return disk.transport.toUpperCase()
}

export function listeningSourceLabel(sources: string[]) {
  const labels = sources.map((source) => {
    if (/docker/i.test(source)) return 'Docker 容器映射'
    if (/systemd|proc|cgroup/i.test(source)) return '进程与系统服务'
    if (/gopsutil|connection|socket/i.test(source)) return '系统连接表'
    return source
  }).filter(Boolean)
  return [...new Set(labels)].join('、') || '系统监听信息'
}

export function formatTime(value: string) {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString('zh-CN', { hour12: false })
}

export function mihomoModeLabel(value: string | undefined) {
  if (value === 'rule') return '规则模式'
  if (value === 'global') return '全局模式'
  if (value === 'direct') return '直连模式'
  return '模式未确认'
}
