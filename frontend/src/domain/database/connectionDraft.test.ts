import { describe, expect, it } from 'vitest'
import type { DatabaseCredentials, DatabaseSource } from '@/api/database'
import { buildDatabaseConnectionDraft, cloneDatabaseCredentials, databaseCAPEMError, databaseSourceBinding, emptyDatabaseTLSDraft, validDatabaseServerName } from './connectionDraft'

const source: DatabaseSource = { id: 'fixture', name: 'Fixture', driver: 'mysql', category: 'project', project: '', module: '', location: 'fixture:3306', defaultDatabase: 'fixture', requiresLogin: true, status: '', tags: [] }
// 仅文本结构夹具，非有效 DER；浏览器不得把预检通过称为证书有效。
const pem = '-----BEGIN CERTIFICATE-----\nZmFrZQ==\n-----END CERTIFICATE-----\n'

describe('数据库 TLS 草稿与快照', () => {
  it('默认完全省略 TLS；明确选择根来源，不回填 CA 或服务器名', () => {
    expect(buildDatabaseConnectionDraft({}, emptyDatabaseTLSDraft(), source)).toEqual({ credentials: {}, errors: {} })
    const draft = { active: true, trust: '', caPEM: '', serverName: '' }
    expect(buildDatabaseConnectionDraft({ username: 'alice' }, draft, source).errors).toEqual({ trust: '请选择信任来源' })
    draft.trust = 'system'; draft.caPEM = pem
    expect(buildDatabaseConnectionDraft({ username: 'alice', password: '' }, draft, source)).toEqual({ credentials: { username: 'alice', password: '', tls: { mode: 'verify-full' } }, errors: {} })
  })
  it('TLS-only 必须提供用户名和有效数据库，空密码可以提交', () => {
    const draft = { active: true, trust: 'system', caPEM: '', serverName: '' }
    expect(buildDatabaseConnectionDraft({}, draft, { ...source, defaultDatabase: '' }).errors).toEqual({ username: '请输入本次连接的用户名', database: '请输入数据库名' })
    expect(buildDatabaseConnectionDraft({ username: 'alice', database: 'fixture', password: '' }, draft, source).errors).toEqual({})
    expect(buildDatabaseConnectionDraft({ username: 'alice' }, draft, { ...source, driver: 'sqlite' }).errors.trust).toBeDefined()
  })
  it('自定义 CA 与非空服务器名保持原文；空自定义不变成系统根', () => {
    const draft = { active: true, trust: 'custom', caPEM: pem, serverName: 'DB.Example.' }
    expect(buildDatabaseConnectionDraft({ username: 'alice' }, draft, source).credentials.tls).toEqual({ mode: 'verify-full', caPEM: pem, serverName: 'DB.Example.' })
    draft.caPEM = ' '
    expect(buildDatabaseConnectionDraft({ username: 'alice' }, draft, source).errors.caPEM).toBe('请输入自定义 CA 证书')
  })
  it('深复制四字段与嵌套策略，忽略未知字段', () => {
    const input = { username: 'alice', password: '', token: 'synthetic', database: 'fixture', tls: { mode: 'verify-full' as const, caPEM: pem, serverName: 'fixture' }, unknown: 'discard' }
    const copy = cloneDatabaseCredentials(input)
    input.tls.caPEM = 'changed'
    expect(copy.tls?.caPEM).toBe(pem)
    copy.tls!.serverName = 'other'
    expect(input.tls.serverName).toBe('fixture')
    expect(copy).not.toHaveProperty('unknown')
    expect(cloneDatabaseCredentials({})).toEqual({})
    expect(cloneDatabaseCredentials({ tls: { mode: 'verify-full' } } as DatabaseCredentials)).toEqual({ tls: { mode: 'verify-full' } })
  })
  it.each(['driver', 'host', 'port', 'location', 'path', 'defaultDatabase'] as const)('相同 ID 的 %s 变化产生不同所有权绑定', (key) => {
    expect(databaseSourceBinding({ ...source, [key]: key === 'port' ? 999 : 'changed' } as DatabaseSource)).not.toBe(databaseSourceBinding(source))
  })
  it('来源显示名称与状态变化不改变目标绑定', () => {
    expect(databaseSourceBinding({ ...source, name: 'new', status: 'available' })).toBe(databaseSourceBinding(source))
  })
})

describe('CA 只做安全文本预检', () => {
  it('精确接收 64 KiB 和 32 块，拒绝 +1 字节和 33 块', () => {
    const exact = pem + ' '.repeat(65536 - new TextEncoder().encode(pem).length)
    expect(databaseCAPEMError(exact)).toBeUndefined()
    expect(databaseCAPEMError(exact + ' ')).toBe('CA 内容超过 64 KiB')
    expect(databaseCAPEMError(pem.repeat(32))).toBeUndefined()
    expect(databaseCAPEMError(pem.repeat(33))).toBe('最多支持 32 张 CA 证书')
    expect(databaseCAPEMError('界'.repeat(21846))).toBe('CA 内容超过 64 KiB')
  })
  it.each(['', ' ', '/tmp/ca.pem', 'https://ca.example', `garbage${pem}`, `${pem}garbage`, pem.replace('ZmFrZQ==', 'Proc-Type: 4,ENCRYPTED\nZmFrZQ=='), pem.replaceAll('CERTIFICATE', 'PRIVATE KEY'), pem.replace('ZmFrZQ==', `-----BEGIN PRIVATE KEY-----\nZmFrZQ==`), pem + '\u00a0'])('拒绝空白、私钥、路径、头部与块外内容 %#', (value) => {
    expect(databaseCAPEMError(value)).toBeDefined()
  })
})

describe('服务器名与 T0 文本合同对齐', () => {
  it.each(['db', 'DB.Example.', 'xn--bcher-kva.example', '127.0.0.1', '::1', '2001:db8::1', '::ffff:192.0.2.1', 'a'.repeat(63), `${'a'.repeat(63)}.${'b'.repeat(63)}.${'c'.repeat(63)}.${'d'.repeat(61)}`])('允许 ASCII DNS 或 raw IP %s', value => {
    expect(validDatabaseServerName(value)).toBe(true)
  })
  it.each(['', ' db.example', 'db.example ', 'db\n.example', 'https://db.example', 'db.example:5432', 'db/path', '*.db', 'db_name', 'db..example', 'db..', '-db', 'db-', '[::1]', 'fe80::1%eth0', '1', '1.2.3', '256.1.2.3', '01.2.3.4', '127.0.0.1.', '1:2:3', '1::2::3', ':::1', '1:2:3:4:5:6:7:8:9', '::ffff:192.0.2.999', 'a'.repeat(64), `${'a'.repeat(63)}.${'b'.repeat(63)}.${'c'.repeat(63)}.${'d'.repeat(61)}.`, '数据库.example'])('拒绝非法非空输入，不 trim 修复 %s', value => {
    expect(validDatabaseServerName(value)).toBe(false)
  })
})
