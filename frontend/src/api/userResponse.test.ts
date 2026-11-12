import { describe, expect, it } from 'vitest'
import { managedUsernameKey, normalizeManagedUsername, parsePasswordPolicyResponse, parseUserListResponse } from './userResponse'

const user = {
  id: 1, username: 'fixture-root', role: 'root', disabled: false,
  createdAt: '2026-10-13T12:00:00Z', updatedAt: '2026-10-13T12:00:00Z',
}
const policy = { minLength: 6, requireUppercase: false, requireLowercase: false, requireDigit: false, requireSpecial: false }
const invalid = { code: 'CONTROL_RESPONSE_INVALID' }

describe('用户列表读取契约', () => {
  it.each([null, undefined, true, 'users', {}, { users: [] }])('拒绝非数组响应：%j', (value) => {
    expect(() => parseUserListResponse(value)).toThrow(expect.objectContaining(invalid))
  })

  it.each([
    null, [], {}, { ...user, id: 0 }, { ...user, id: -1 }, { ...user, id: 1.5 },
    { ...user, id: '1' }, { ...user, id: Number.MAX_SAFE_INTEGER + 1 }, { ...user, id: NaN },
    { ...user, id: Infinity }, { ...user, username: null }, { ...user, username: 1 },
    { ...user, role: 'admin' }, { ...user, role: null }, { ...user, disabled: 0 },
    { ...user, disabled: 'false' }, { ...user, createdAt: null }, { ...user, updatedAt: '' },
    { ...user, createdAt: 'yesterday' }, { ...user, updatedAt: '2026-10-13' },
    { ...user, createdAt: '2026-13-01T00:00:00Z' }, { ...user, updatedAt: '2026-10-13T25:00:00Z' },
    { ...user, createdAt: '2026-02-30T00:00:00Z' }, { ...user, createdAt: '2026-02-29T00:00:00Z' },
    { ...user, updatedAt: '2026-10-13T24:00:00Z' },
    { ...user, lastLoginAt: 'bad' }, { ...user, lastLoginAt: '' }, { ...user, lastLoginAt: 0 },
    { ...user, lastLoginAt: undefined },
  ])('任何坏行使整份列表拒绝，不静默过滤：%j', (value) => {
    expect(() => parseUserListResponse([user, value])).toThrow(expect.objectContaining(invalid))
  })

  it.each(['id', 'username', 'role', 'disabled', 'createdAt', 'updatedAt'])('必填字段 %s 不能缺失', (field) => {
    const value: Record<string, unknown> = { ...user }
    delete value[field]
    expect(() => parseUserListResponse([value])).toThrow(expect.objectContaining(invalid))
  })

  it('保留空列表、未知字段及 schema 允许的空用户名，不修改输入', () => {
    const value = Object.freeze({ ...user, username: '', id: Number.MAX_SAFE_INTEGER, future: { revision: 2 } })
    expect(parseUserListResponse([])).toEqual([])
    expect(parseUserListResponse([value])).toEqual([value])
    expect(parseUserListResponse([value])[0]).not.toBe(value)
  })

  it('允许未登录时间省略、null、带偏移和纳秒的 Go 日期', () => {
    const values = [
      user,
      { ...user, lastLoginAt: null },
      { ...user, createdAt: '2026-10-13T20:00:00.123456789+08:00', lastLoginAt: '2026-10-13T12:00:00Z' },
      { ...user, createdAt: '2024-02-29T00:00:00Z' },
    ]
    expect(parseUserListResponse(values)).toEqual(values)
    expect(parseUserListResponse([user])[0]).not.toHaveProperty('lastLoginAt')
  })
})

describe('密码规则读取契约', () => {
  it.each([null, undefined, [], true, 'policy', {}])('拒绝非规则对象，不创建默认规则：%j', (value) => {
    expect(() => parsePasswordPolicyResponse(value)).toThrow(expect.objectContaining(invalid))
  })

  it.each([0, -1, 129, 1.5, '6', null, undefined, NaN, Infinity])('拒绝非法最短长度：%j', (minLength) => {
    expect(() => parsePasswordPolicyResponse({ ...policy, minLength })).toThrow(expect.objectContaining(invalid))
  })

  it.each(['minLength', 'requireUppercase', 'requireLowercase', 'requireDigit', 'requireSpecial'])('不能省略 %s', (field) => {
    const value: Record<string, unknown> = { ...policy }
    delete value[field]
    expect(() => parsePasswordPolicyResponse(value)).toThrow(expect.objectContaining(invalid))
  })

  it.each(['requireUppercase', 'requireLowercase', 'requireDigit', 'requireSpecial'])('%s 必须为布尔值', (field) => {
    for (const value of [0, 1, 'false', null, undefined, {}]) {
      expect(() => parsePasswordPolicyResponse({ ...policy, [field]: value })).toThrow(expect.objectContaining(invalid))
    }
    expect(parsePasswordPolicyResponse({ ...policy, [field]: true })).toEqual({ ...policy, [field]: true })
  })

  it.each([1, 128])('保留合法边界 %s 和未知字段，不修改输入', (minLength) => {
    const value = Object.freeze({ ...policy, minLength, future: { revision: 2 } })
    expect(parsePasswordPolicyResponse(value)).toEqual(value)
    expect(parsePasswordPolicyResponse(value)).not.toBe(value)
  })
})

describe('账号名归一化与 Go 一致', () => {
  it('去掉 NEL Unicode 空白，保留 BOM 并保留大小写', () => {
    expect(normalizeManagedUsername('\u0085 Root\u0085')).toBe('Root')
    expect(normalizeManagedUsername('\uFEFFRoot\uFEFF')).toBe('\uFEFFRoot\uFEFF')
  })
})

describe('账号创建未知锁的 SQLite NOCASE 身份', () => {
  it('仅折叠 ASCII，保留非 ASCII 区分并复用 Go 空白规则', () => {
    expect(managedUsernameKey('\u0085 Root\u0085')).toBe('root')
    expect(managedUsernameKey('ÄBC')).toBe('Äbc')
    expect(managedUsernameKey('äBC')).toBe('äbc')
    expect(managedUsernameKey('İI')).toBe('İi')
    expect(managedUsernameKey('\uFEFFRoot')).toBe('\uFEFFroot')
  })
})
