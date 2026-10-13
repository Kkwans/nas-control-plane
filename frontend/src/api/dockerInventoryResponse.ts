import type { DockerInventory } from './system'
import { NcpApiError } from './systemTransport'

export const DOCKER_INVENTORY_RESPONSE_INVALID = 'DOCKER_INVENTORY_RESPONSE_INVALID'

type Decoder<T> = (value: unknown) => T

function invalid(): never {
  throw new NcpApiError(DOCKER_INVENTORY_RESPONSE_INVALID, 'NCP 服务返回了无法识别的数据。')
}

const string: Decoder<string> = (value) => typeof value === 'string' ? value : invalid()
const integer: Decoder<number> = (value) => typeof value === 'number' && Number.isFinite(value) && Number.isInteger(value) ? value : invalid()
const unsigned: Decoder<number> = (value) => integer(value) >= 0 ? value as number : invalid()
const port: Decoder<number> = (value) => unsigned(value) <= 65535 ? value as number : invalid()

function object<S extends Record<string, Decoder<unknown>>>(shape: S): Decoder<{ [K in keyof S]: ReturnType<S[K]> }> {
  return (value) => {
    if (value === null || typeof value !== 'object' || Array.isArray(value)) invalid()
    const input = value as Record<string, unknown>
    const result: Record<string, unknown> = {}
    for (const key of Object.keys(shape)) {
      const decoded = shape[key]!(input[key])
      if (decoded !== undefined) result[key] = decoded
    }
    return result as { [K in keyof S]: ReturnType<S[K]> }
  }
}

function collection<T>(decode: Decoder<T>): Decoder<T[]> {
  return (value) => {
    // 仅兼容明确为 null 的 Go 切片；缺字段和坏行不能伪装成空库存。
    if (value === null) return []
    if (!Array.isArray(value)) invalid()
    return Array.from(value, decode)
  }
}

function optional<T>(decode: Decoder<T>): Decoder<T | undefined> {
  return (value) => value === undefined ? undefined : decode(value)
}

function enumeration<const T extends readonly string[]>(values: T): Decoder<T[number]> {
  return (value) => typeof value === 'string' && values.includes(value) ? value : invalid()
}

const inventory = object({
  collectedAt: string,
  engine: object({
    serverVersion: string,
    operatingSystem: string,
    architecture: string,
    containers: unsigned,
    containersRunning: unsigned,
    containersStopped: unsigned,
    images: unsigned,
  }),
  containers: collection(object({
    id: string,
    name: string,
    image: string,
    state: string,
    status: string,
    createdAt: string,
    startedAt: optional(string),
    finishedAt: optional(string),
    exitCode: optional(integer),
    restartCount: optional(unsigned),
    health: optional(enumeration(['none', 'starting', 'healthy', 'unhealthy'])),
    ports: collection(object({ hostIp: string, privatePort: port, publicPort: port, protocol: string })),
    projectId: string,
    projectName: string,
    serviceName: string,
  })),
  projects: collection(object({
    id: string,
    name: string,
    kind: enumeration(['compose', 'standalone']),
    state: enumeration(['running', 'stopped', 'degraded']),
    workingDirectory: string,
    configFiles: collection(string),
    containerCount: unsigned,
    runningCount: unsigned,
  })),
}) satisfies Decoder<DockerInventory>

export function parseDockerInventoryResponse(value: unknown): DockerInventory {
  return inventory(value)
}
