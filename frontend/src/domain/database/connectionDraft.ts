import type { DatabaseCredentials, DatabaseSource, DatabaseTLSPolicy } from '@/api/database'

// 仅复制凭据合同；请求、缓存与可编辑草稿不共享嵌套策略对象。
export function cloneDatabaseCredentials(input: DatabaseCredentials): DatabaseCredentials {
  const copy: DatabaseCredentials = {}
  for (const key of ['username', 'password', 'token', 'database'] as const) {
    if (input[key] !== undefined) copy[key] = input[key]
  }
  if (input.tls !== undefined) {
    copy.tls = { mode: input.tls.mode }
    if (input.tls.caPEM !== undefined) copy.tls.caPEM = input.tls.caPEM
    if (input.tls.serverName !== undefined) copy.tls.serverName = input.tls.serverName
  }
  return copy
}

export function databaseSourceBinding(source: DatabaseSource | null | undefined): string {
  return source ? JSON.stringify([source.id, source.driver, source.host, source.port, source.location, source.path, source.defaultDatabase]) : ''
}

export interface DatabaseTLSDraft {
  active: boolean
  trust: string
  caPEM: string
  serverName: string
}
export type DatabaseConnectionField = 'username' | 'database' | 'trust' | 'caPEM' | 'serverName'
export type DatabaseConnectionErrors = Partial<Record<DatabaseConnectionField, string>>
export function emptyDatabaseTLSDraft(): DatabaseTLSDraft {
  return { active: false, trust: '', caPEM: '', serverName: '' }
}

// 只预检文本结构；DER、CA用途和真实服务器身份由 Root Agent 校验。
export function databaseCAPEMError(value: string): string | undefined {
  if (!value.trim()) return '请输入自定义 CA 证书'
  if (new TextEncoder().encode(value).length > 65536) return 'CA 内容超过 64 KiB'
  const blocks = value.match(/-----BEGIN CERTIFICATE-----/g) ?? []
  if (blocks.length > 32) return '最多支持 32 张 CA 证书'
  const pattern = /-----BEGIN CERTIFICATE-----[ \t]*\r?\n[A-Za-z0-9+/=\r\n \t]+\r?\n-----END CERTIFICATE-----[ \t]*(?:\r?\n|$)/g
  let count = 0
  const remainder = value.replace(pattern, () => { count += 1; return '' })
  if (!count || count !== blocks.length || /[^ \t\r\n]/.test(remainder)) return '请输入完整的 PEM CA 证书，不接受路径、网址、私钥或其他内容'
  return undefined
}

function ipv4(value: string): boolean {
  const parts = value.split('.')
  return parts.length === 4 && parts.every(part => /^(0|[1-9][0-9]{0,2})$/.test(part) && Number(part) <= 255)
}
function ipv6(value: string): boolean {
  if (!/^[0-9a-fA-F:.]+$/.test(value) || !value.includes(':')) return false
  const halves = value.split('::')
  if (halves.length > 2) return false
  const parts = halves.flatMap(half => half ? half.split(':') : [])
  let groups = 0
  for (const [index, part] of parts.entries()) {
    if (part.includes('.')) {
      if (index !== parts.length - 1 || !value.endsWith(part) || !ipv4(part)) return false
      groups += 2
    } else {
      if (!/^[0-9a-fA-F]{1,4}$/.test(part)) return false
      groups += 1
    }
  }
  return halves.length === 2 ? groups < 8 : groups === 8
}

export function validDatabaseServerName(value: string): boolean {
  if (!value || value.length > 253) return false
  if (ipv4(value) || ipv6(value)) return true
  const name = value.endsWith('.') ? value.slice(0, -1) : value
  if (/^[0-9.]+$/.test(name)) return false
  return name.split('.').every(label => /^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$/.test(label))
}

export function buildDatabaseConnectionDraft(credentials: DatabaseCredentials, tls: DatabaseTLSDraft, source: DatabaseSource): {
  credentials: DatabaseCredentials
  errors: DatabaseConnectionErrors
} {
  const snapshot = cloneDatabaseCredentials(credentials)
  delete snapshot.tls
  const errors: DatabaseConnectionErrors = {}
  if (!tls.active) return { credentials: snapshot, errors }
  if (!snapshot.username?.trim()) errors.username = '请输入本次连接的用户名'
  if (!snapshot.database?.trim() && !source.defaultDatabase?.trim()) errors.database = '请输入数据库名'
  if (tls.trust !== 'system' && tls.trust !== 'custom') errors.trust = '请选择信任来源'
  if (tls.trust === 'custom') {
    const message = databaseCAPEMError(tls.caPEM)
    if (message) errors.caPEM = message
  }
  if (tls.serverName !== '' && !validDatabaseServerName(tls.serverName)) errors.serverName = '服务器名格式无效'
  if (source.driver === 'sqlite') errors.trust = 'SQLite 不支持此 TLS 设置'
  const policy: DatabaseTLSPolicy = { mode: 'verify-full' }
  if (tls.trust === 'custom') policy.caPEM = tls.caPEM
  if (tls.serverName !== '') policy.serverName = tls.serverName
  snapshot.tls = policy
  return { credentials: snapshot, errors }
}
