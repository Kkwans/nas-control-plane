import type { SystemDetails } from './system'
import { NcpApiError } from './systemTransport'

type Decoder<T> = (value: unknown) => T

function invalid(): never {
  throw new NcpApiError('SYSTEM_DETAILS_RESPONSE_INVALID', 'NCP 服务返回了无法识别的数据。')
}

const string: Decoder<string> = (value) => typeof value === 'string' ? value : invalid()
const boolean: Decoder<boolean> = (value) => typeof value === 'boolean' ? value : invalid()
const number: Decoder<number> = (value) => typeof value === 'number' && Number.isFinite(value) ? value : invalid()
const integer: Decoder<number> = (value) => Number.isInteger(number(value)) ? value as number : invalid()
const nonnegative: Decoder<number> = (value) => number(value) >= 0 ? value as number : invalid()
const unsigned: Decoder<number> = (value) => integer(value) >= 0 ? value as number : invalid()

function object<S extends Record<string, Decoder<unknown>>>(shape: S): Decoder<{ [K in keyof S]: ReturnType<S[K]> }> {
  return (value) => {
    if (value === null || typeof value !== 'object' || Array.isArray(value)) invalid()
    const input = value as Record<string, unknown>
    const result: Record<string, unknown> = { ...input }
    for (const key of Object.keys(shape)) result[key] = shape[key]!(input[key])
    return result as { [K in keyof S]: ReturnType<S[K]> }
  }
}

function array<T>(decode: Decoder<T>, nullable = false): Decoder<T[]> {
  return (value) => {
    // 仅兼容 Go 未初始化的嵌套切片；缺字段和错误元素不能伪装成空结果。
    if (nullable && value === null) return []
    if (!Array.isArray(value)) invalid()
    return value.map(decode)
  }
}

function optional<T>(decode: Decoder<T>): Decoder<T | undefined> {
  return (value) => value === undefined ? undefined : decode(value)
}

function enumeration<const T extends readonly string[]>(values: T): Decoder<T[number]> {
  return (value) => typeof value === 'string' && values.includes(value) ? value : invalid()
}

const strings = array(string)
const nestedStrings = array(string, true)
const warning = object({ code: string, source: string })
const evidence = object({ source: string, status: string, detail: string })
const mihomo = object({
  detected: boolean, state: string, processName: string, executable: string, version: string,
  controller: object({
    detected: boolean, endpoint: string, reachable: boolean, authRequired: boolean,
    tokenConfigured: boolean, operations: nestedStrings, detectionSource: string,
  }),
  evidence: array(evidence, true), warnings: array(warning, true),
})
const dns = object({
  backend: string, detected: boolean, state: string, readOnly: boolean,
  canRead: boolean, canPreview: boolean, canConfirm: boolean, canRollback: boolean,
  nameservers: nestedStrings, configuredNameservers: optional(nestedStrings),
  detectionSource: string, errorCode: string,
})

const details = object({
  collectedAt: string,
  warnings: strings,
  device: object({
    hostname: string, model: string, operatingSystem: string, kernelVersion: string,
    architecture: string, uptimeSeconds: unsigned, processCount: unsigned, cgroupVersion: string,
  }),
  hardware: object({
    cpu: object({ model: string, physicalCores: unsigned, logicalCores: unsigned, frequencyMHz: nonnegative, temperatureCelsius: number }),
    memory: object({ totalBytes: unsigned, availableBytes: unsigned }),
    sensors: array(object({ name: string, temperatureCelsius: number })),
  }),
  network: object({
    interfaces: array(object({
      name: string, hardwareAddress: string, mtu: unsigned, state: string,
      lowerUp: boolean, lowerUpKnown: boolean,
      // Linux 用 -1 表示未知速率，不能按 OpenAPI 旧的 minimum: 0 拒绝。
      speedMbps: (value: unknown) => integer(value) >= -1 ? value as number : invalid(), duplex: string,
      addresses: array(object({ address: string, prefixLength: unsigned, family: string }), true),
    })),
    gateway: string,
    routes: array(object({ destination: string, gateway: string, interface: string, metric: integer })),
    dnsServers: strings,
    listeningPorts: array(object({
      protocol: string, address: string,
      port: (value: unknown) => unsigned(value) <= 65535 ? value as number : invalid(),
      pid: integer,
      processName: optional(string), executable: optional(string), systemdUnit: optional(string),
      containerId: optional(string), containerName: optional(string), service: optional(string),
      detectionSource: optional(string), detectionSources: optional(nestedStrings),
      detectionStatus: optional(string), detectionErrorCode: optional(string),
    })),
  }),
  storage: object({
    mounts: array(object({ path: string, device: string, filesystem: string, totalBytes: unsigned, usedBytes: unsigned, availableBytes: unsigned, usedPercent: nonnegative })),
    disks: array(object({
      name: string, model: string, sizeBytes: unsigned, rotational: boolean,
      kind: enumeration(['', 'physical', 'emmc', 'emmc-boot', 'compressed-memory', 'virtual']),
      role: enumeration(['', 'data', 'system', 'boot', 'swap', 'virtual']),
      transport: string, description: string, health: string, temperatureCelsius: number,
    })),
    raid: array(object({ name: string, level: string, state: string, devices: nestedStrings })),
  }),
  proxy: object({
    mihomo: object({ detected: boolean, state: string, detail: string, controllerEndpoint: optional(string), authRequired: optional(boolean), operations: optional(nestedStrings) }),
    mihomoCapability: optional(mihomo),
    system: array(object({ source: string, method: string, evidence: string, address: string, detail: string })),
    associations: array(object({ subject: string, kind: string, method: string, evidence: string, endpoint: string, detail: string })),
  }),
  tailscale: optional(object({
    detected: boolean, state: string, backendState: string, version: string, interface: string,
    overlayIps: nestedStrings, online: boolean, linkState: string, heartbeatState: string,
    reachable: boolean, evidence: array(evidence, true), warnings: array(warning, true),
  })),
  dns: optional(dns),
  publicEgress: optional(object({ configured: boolean, status: string, endpoint: string, requiresUserAction: boolean, detectionSource: string, errorCode: string })),
  control: object({ nodes: array(object({ id: string, name: string, detail: string, status: string, version: string, lastSeen: string })) }),
}) satisfies Decoder<SystemDetails>

export function parseSystemDetailsResponse(value: unknown): SystemDetails {
  return details(value)
}
