export type ListenerScope = 'public' | 'all-interfaces' | 'lan' | 'overlay' | 'container' | 'loopback' | 'specific'

export interface ListenerScopePresentation {
  value: ListenerScope
  label: string
  description: string
  tone: 'danger' | 'warning' | 'info' | 'success' | 'neutral'
  rank: number
}

export function editableDNSNameservers(capability: {
  nameservers: readonly string[]
  configuredNameservers?: readonly string[]
}) {
  const configured = normalizedUniqueValues(capability.configuredNameservers ?? [])
  return configured.length ? configured : normalizedUniqueValues(capability.nameservers)
}

export function isAuxiliaryNetworkInterface(name: string) {
  return /^(br-|docker|veth|virbr|tun|tap|meta$)/i.test(name.trim())
}

export function networkInterfaceKindLabel(name: string) {
  const normalized = name.trim()
  if (/^meta$/i.test(normalized)) return 'Mihomo TUN 虚拟接口'
  if (/^(br-|docker|veth)/i.test(normalized)) return 'Docker 虚拟接口'
  if (/^tailscale/i.test(normalized)) return 'Tailscale Overlay 接口'
  if (/^(tun|tap)/i.test(normalized)) return '代理虚拟接口'
  if (normalized === 'lo') return '本机回环'
  return '主机网络接口'
}

export function isSubscriptionStatusNodeName(name: string) {
  return /(?:剩余|已用|总计)?流量|到期|套餐|订阅|traffic|quota|expire|subscription/i.test(name.trim())
}

const presentations: Record<ListenerScope, ListenerScopePresentation> = {
  public: {
    value: 'public', label: '公网地址', description: '直接监听公网地址', tone: 'danger', rank: 0,
  },
  'all-interfaces': {
    value: 'all-interfaces', label: '所有接口', description: '监听主机全部网络接口', tone: 'warning', rank: 1,
  },
  lan: {
    value: 'lan', label: '局域网', description: '仅监听局域网地址', tone: 'info', rank: 2,
  },
  overlay: {
    value: 'overlay', label: 'Tailscale', description: '仅监听 Tailscale Overlay 地址', tone: 'success', rank: 3,
  },
  container: {
    value: 'container', label: '容器网络', description: '仅监听 Docker 容器网段', tone: 'neutral', rank: 4,
  },
  loopback: {
    value: 'loopback', label: '仅本机', description: '仅允许 NAS 本机访问', tone: 'neutral', rank: 5,
  },
  specific: {
    value: 'specific', label: '指定地址', description: '监听一个指定网络地址', tone: 'neutral', rank: 6,
  },
}

export function classifyListenerScope(addresses: readonly string[], overlayIps: readonly string[] = []): ListenerScopePresentation {
  const normalized = addresses.map(normalizeAddress).filter(Boolean)
  if (!normalized.length || normalized.some(isWildcardAddress)) return presentations['all-interfaces']
  if (normalized.some(isPublicAddress)) return presentations.public
  if (normalized.every(isLoopbackAddress)) return presentations.loopback
  if (normalized.every((address) => isOverlayAddress(address, overlayIps))) return presentations.overlay
  if (normalized.every(isContainerAddress)) return presentations.container
  if (normalized.every(isLANAddress)) return presentations.lan
  return presentations.specific
}

export function listenerScopePresentation(scope: ListenerScope): ListenerScopePresentation {
  return presentations[scope]
}

function normalizeAddress(value: string) {
  return value.trim().replace(/^\[|\]$/g, '').split('%', 1)[0]?.toLowerCase() ?? ''
}

function isWildcardAddress(value: string) {
  return value === '*' || value === '0.0.0.0' || value === '::' || value === '0:0:0:0:0:0:0:0'
}

function isLoopbackAddress(value: string) {
  return value === '::1' || /^127\./.test(value)
}

function isOverlayAddress(value: string, overlayIps: readonly string[]) {
  const known = overlayIps.map(normalizeAddress)
  if (known.includes(value)) return true
  if (value.startsWith('fd7a:115c:a1e0:')) return true
  const octets = ipv4Octets(value)
  return Boolean(octets && octets[0] === 100 && octets[1] !== undefined && octets[1] >= 64 && octets[1] <= 127)
}

function isContainerAddress(value: string) {
  const octets = ipv4Octets(value)
  return Boolean(octets && octets[0] === 172 && octets[1] !== undefined && octets[1] >= 16 && octets[1] <= 31)
}

function isLANAddress(value: string) {
  if (/^(fe80:|169\.254\.)/.test(value)) return true
  if (/^(fc|fd)[0-9a-f]{2}:/.test(value)) return true
  const octets = ipv4Octets(value)
  if (!octets) return false
  return octets[0] === 10 || (octets[0] === 192 && octets[1] === 168)
}

function isPublicAddress(value: string) {
  if (isWildcardAddress(value) || isLoopbackAddress(value) || isContainerAddress(value) || isLANAddress(value)) return false
  if (isOverlayAddress(value, [])) return false
  return Boolean(ipv4Octets(value) || value.includes(':'))
}

function ipv4Octets(value: string) {
  if (!/^\d{1,3}(?:\.\d{1,3}){3}$/.test(value)) return null
  const octets = value.split('.').map(Number)
  return octets.every((part) => part >= 0 && part <= 255) ? octets : null
}

function normalizedUniqueValues(values: readonly string[]) {
  return [...new Set(values.map((value) => value.trim()).filter(Boolean))]
}
