import type { ManagedUser, PasswordPolicy } from './control'
import { NcpApiError } from './system'

function invalid(): never {
  throw new NcpApiError('CONTROL_RESPONSE_INVALID', '用户接口返回了无法识别的数据。')
}

function record(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) invalid()
  return value as Record<string, unknown>
}

function text(value: unknown): string {
  if (typeof value !== 'string') invalid()
  return value
}

function boolean(value: unknown): boolean {
  if (typeof value !== 'boolean') invalid()
  return value
}

function integer(value: unknown, min: number, max: number): number {
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value < min || value > max) invalid()
  return value
}

function timestamp(value: unknown): string {
  const result = text(value)
  const parts = /^(\d{4})-(\d{2})-(\d{2})T([01]\d|2[0-3]):[0-5]\d:[0-5]\d(?:\.\d+)?(?:Z|[+-](?:[01]\d|2[0-3]):[0-5]\d)$/i.exec(result)
  if (!parts || !Number.isFinite(Date.parse(result))) invalid()
  const year = Number(parts[1])
  const month = Number(parts[2])
  const day = Number(parts[3])
  const leap = year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0)
  const daysInMonth = [31, leap ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31][month - 1]
  // Date.parse 会把部分越界日历日期进位，不能将其当成有效服务端时间。
  if (!daysInMonth || day < 1 || day > daysInMonth) invalid()
  return result
}

export function parseManagedUserResponse(value: unknown): ManagedUser {
  const item = record(value)
  if (item.role !== 'root') invalid()
  const result: ManagedUser = {
    ...item,
    id: integer(item.id, 1, Number.MAX_SAFE_INTEGER),
    username: text(item.username),
    role: item.role,
    disabled: boolean(item.disabled),
    createdAt: timestamp(item.createdAt),
    updatedAt: timestamp(item.updatedAt),
  }
  // Go 省略尚未登录的时间；OpenAPI 也允许显式 null，不伪造日期。
  if ('lastLoginAt' in item) result.lastLoginAt = item.lastLoginAt === null ? null : timestamp(item.lastLoginAt)
  return result
}

export function parseUserListResponse(value: unknown): ManagedUser[] {
  if (!Array.isArray(value)) invalid()
  return value.map(parseManagedUserResponse)
}

// 仅接入读取响应；写入结果未知的恢复语义由独立切片处理。
export function parsePasswordPolicyResponse(value: unknown): PasswordPolicy {
  const item = record(value)
  return {
    ...item,
    minLength: integer(item.minLength, 1, 128),
    requireUppercase: boolean(item.requireUppercase),
    requireLowercase: boolean(item.requireLowercase),
    requireDigit: boolean(item.requireDigit),
    requireSpecial: boolean(item.requireSpecial),
  }
}

// 与 Go strings.TrimSpace 相同；JS trim 对 NEL/BOM 的处理不同。
export function normalizeManagedUsername(value: string): string {
  return value.replace(/^\p{White_Space}+|\p{White_Space}+$/gu, '')
}

// SQLite COLLATE NOCASE 只折叠 ASCII 字母，不使用 Unicode/区域大小写转换。
export function managedUsernameKey(value: string): string {
  return normalizeManagedUsername(value).replace(/[A-Z]/g, (character) => character.toLowerCase())
}
