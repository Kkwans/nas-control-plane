import { computed, ref, watch, type ComputedRef, type Ref } from 'vue'

import type { SystemDetails, TailscaleCapability } from '@/api/system'
import {
  classifyListenerScope,
  isAuxiliaryNetworkInterface,
  type ListenerScope,
  type ListenerScopePresentation,
} from '@/domain/network'
import {
  interfaceIsOnline,
  listeningPortOwners,
} from '@/domain/infrastructurePresentation'

export type ListenerScopeFilter = 'all' | 'exposed' | ListenerScope

export interface ListeningPortGroup {
  port: number
  protocol: string
  addresses: string[]
  pids: number[]
  owners: Array<{ label: string; detail: string }>
  sources: string[]
  scope: ListenerScopePresentation
}

export function useInfrastructureNetwork(
  details: Ref<SystemDetails | null>,
  tailscaleDetails: ComputedRef<TailscaleCapability>,
) {
  const listenerQuery = ref('')
  const listenerProtocol = ref('all')
  const listenerScope = ref<ListenerScopeFilter>('all')
  const listenerVisibleLimit = ref(24)

  const listenerProtocolOptions = [
    { label: '全部协议', value: 'all' },
    { label: 'TCP', value: 'tcp' },
    { label: 'UDP', value: 'udp' },
  ]

  const networkInterfaces = computed(() => details.value?.network.interfaces ?? [])
  const primaryInterfaces = computed(() => {
    const candidates = networkInterfaces.value.filter((item) => item.name !== 'lo' && !isAuxiliaryNetworkInterface(item.name))
    const tailscale = candidates.filter((item) => /^tailscale/i.test(item.name))
    const physical = candidates
      .filter((item) => !/^tailscale/i.test(item.name))
      .sort((left, right) => Number(interfaceIsOnline(right)) - Number(interfaceIsOnline(left)))
    const reserved = Math.min(tailscale.length, 1)
    return [...physical.slice(0, 4 - reserved), ...tailscale.slice(0, reserved)]
  })
  const primaryInterfaceNames = computed(() => new Set(primaryInterfaces.value.map((item) => item.name)))
  const secondaryInterfaces = computed(() => networkInterfaces.value.filter((item) => !primaryInterfaceNames.value.has(item.name)))
  const primaryActiveInterfaceCount = computed(() => primaryInterfaces.value.filter(interfaceIsOnline).length)
  const listeningPortGroups = computed(() => {
    const groups = new Map<string, Omit<ListeningPortGroup, 'scope'>>()
    for (const item of details.value?.network.listeningPorts ?? []) {
      const key = `${item.protocol}:${item.port}`
      const group = groups.get(key) ?? { port: item.port, protocol: item.protocol, addresses: [], pids: [], owners: [], sources: [] }
      if (item.address && !group.addresses.includes(item.address)) group.addresses.push(item.address)
      if (item.pid && !group.pids.includes(item.pid)) group.pids.push(item.pid)
      for (const source of [...(item.detectionSources ?? []), item.detectionSource ?? ''].filter(Boolean)) {
        if (!group.sources.includes(source)) group.sources.push(source)
      }
      for (const owner of listeningPortOwners(item)) {
        if (!group.owners.some((current) => current.label === owner.label && current.detail === owner.detail)) group.owners.push(owner)
      }
      groups.set(key, group)
    }
    return [...groups.values()]
      .map<ListeningPortGroup>((group) => ({
        ...group,
        scope: classifyListenerScope(group.addresses, tailscaleDetails.value.overlayIps),
      }))
      .sort((left, right) => left.scope.rank - right.scope.rank || left.port - right.port)
  })
  const filteredListeningPortGroups = computed(() => {
    const query = listenerQuery.value.trim().toLocaleLowerCase('zh-CN')
    return listeningPortGroups.value.filter((item) => {
      if (listenerProtocol.value !== 'all' && item.protocol.toLowerCase() !== listenerProtocol.value) return false
      if (listenerScope.value === 'exposed' && !['public', 'all-interfaces'].includes(item.scope.value)) return false
      if (listenerScope.value !== 'all' && listenerScope.value !== 'exposed' && item.scope.value !== listenerScope.value) return false
      if (!query) return true
      return [
        String(item.port), item.protocol, ...item.addresses, ...item.sources,
        ...item.owners.flatMap((owner) => [owner.label, owner.detail]),
      ].some((value) => value.toLocaleLowerCase('zh-CN').includes(query))
    })
  })
  const visibleListeningPortGroups = computed(() => filteredListeningPortGroups.value.slice(0, listenerVisibleLimit.value))
  const exposedListenerCount = computed(() => listeningPortGroups.value.filter((item) => ['public', 'all-interfaces'].includes(item.scope.value)).length)
  const localListenerCount = computed(() => listeningPortGroups.value.filter((item) => item.scope.value === 'loopback').length)
  const listenerResultLabel = computed(() => filteredListeningPortGroups.value.length === listeningPortGroups.value.length
    ? `共 ${listeningPortGroups.value.length} 个监听端口`
    : `筛选出 ${filteredListeningPortGroups.value.length} / ${listeningPortGroups.value.length} 个端口`)
  const listenerScopeOptions = computed(() => [
    { label: '全部范围', value: 'all' },
    { label: `对外监听 ${exposedListenerCount.value}`, value: 'exposed' },
    { label: `局域网 ${listenerScopeCount('lan')}`, value: 'lan' },
    { label: `仅本机 ${listenerScopeCount('loopback')}`, value: 'loopback' },
    { label: `Tailscale ${listenerScopeCount('overlay')}`, value: 'overlay' },
    { label: `容器网络 ${listenerScopeCount('container')}`, value: 'container' },
  ])

  watch([listenerQuery, listenerProtocol, listenerScope], () => { listenerVisibleLimit.value = 24 })

  function listenerScopeCount(scope: ListenerScope) {
    return listeningPortGroups.value.filter((item) => item.scope.value === scope).length
  }

  function showMoreListeningPorts() {
    listenerVisibleLimit.value += 24
  }

  return {
    listenerQuery,
    listenerProtocol,
    listenerScope,
    listenerVisibleLimit,
    listenerProtocolOptions,
    listenerScopeOptions,
    networkInterfaces,
    primaryInterfaces,
    secondaryInterfaces,
    primaryActiveInterfaceCount,
    listeningPortGroups,
    filteredListeningPortGroups,
    visibleListeningPortGroups,
    exposedListenerCount,
    localListenerCount,
    listenerResultLabel,
    showMoreListeningPorts,
  }
}
