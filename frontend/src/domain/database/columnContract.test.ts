import { describe, expect, it } from 'vitest'
import type { DatabaseColumn } from '@/api/database'
import { databaseColumnContract } from './columnContract'

const column: DatabaseColumn = { name: 'value', dataType: 'TEXT', nullable: false, primaryKey: false, position: 1 }

describe('数据库写入列合同归一', () => {
  it('缺失、null、空列合同一致，缺省 writeMode 与 required 一致', () => {
    expect(databaseColumnContract(undefined)).toBe(databaseColumnContract([]))
    expect(databaseColumnContract(null)).toBe(databaseColumnContract([]))
    expect(databaseColumnContract([column])).toBe(databaseColumnContract([{ ...column, default: null, writeMode: 'required' }]))
  })
  it('空 SQLite 声明类型合法，字段顺序与位置不影响合同', () => {
    const emptyType = { ...column, name: 'empty', dataType: '' }
    expect(databaseColumnContract([emptyType])).not.toBeNull()
    expect(databaseColumnContract([column, emptyType])).toBe(databaseColumnContract([{ ...emptyType, position: 12 }, { ...column, position: 40, dataType: ' text ' }]))
  })
  it.each(['', false, 0, 'false', '0', '9223372036854775807'])('默认值 %s 与未提供不同且不做数值转换', (value) => {
    expect(databaseColumnContract([{ ...column, default: value }])).not.toBe(databaseColumnContract([column]))
    expect(JSON.parse(databaseColumnContract([{ ...column, default: value }])!)[0].default).toBe(value)
  })
  it('optional-default 与 server-generated 保留不同写入含义', () => {
    const contracts = ['required', 'optional-default', 'server-generated'].map((writeMode) => databaseColumnContract([{ ...column, writeMode: writeMode as DatabaseColumn['writeMode'] }]))
    expect(new Set(contracts).size).toBe(3)
  })
  it('重复字段或不可比较的列元数据不能提供可信合同', () => {
    expect(databaseColumnContract([column, column])).toBeNull()
    expect(databaseColumnContract([{ ...column, writeMode: 'unexpected' as DatabaseColumn['writeMode'] }])).toBeNull()
    expect(databaseColumnContract([{ ...column, dataType: null } as unknown as DatabaseColumn])).toBeNull()
  })
})
