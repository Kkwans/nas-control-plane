import { describe, expect, it } from 'vitest'

import type { DatabaseColumn } from '@/api/database'

import { DatabaseValueError, databaseEditorKind, resolveDatabaseValue } from './valueConversion'

function column(dataType: string, nullable = true): DatabaseColumn {
  return { name: 'value', dataType, nullable, primaryKey: false, position: 1 }
}

describe('resolveDatabaseValue', () => {
  it('writes an empty string when NULL is not explicitly selected', () => {
    expect(resolveDatabaseValue('', column('varchar(255)'), false)).toBe('')
  })

  it('writes NULL only when the explicit NULL state is selected', () => {
    expect(resolveDatabaseValue('', column('varchar(255)'), true)).toBeNull()
  })

  it('preserves bigint and decimal literals without JavaScript Number conversion', () => {
    expect(resolveDatabaseValue('9007199254740993', column('bigint'), false)).toBe('9007199254740993')
    expect(resolveDatabaseValue('12345678901234567890.123456789', column('decimal(30,9)'), false))
      .toBe('12345678901234567890.123456789')
  })

  it('rejects an empty non-null numeric value instead of guessing NULL', () => {
    expect(() => resolveDatabaseValue('', column('integer', false), false)).toThrow(DatabaseValueError)
  })

  it('validates JSON without converting the submitted string', () => {
    expect(resolveDatabaseValue('{"id":9007199254740993}', column('jsonb'), false)).toBe('{"id":9007199254740993}')
    expect(() => resolveDatabaseValue('{oops}', column('json'), false)).toThrow(DatabaseValueError)
  })
})

describe('databaseEditorKind', () => {
  it.each([
    ['varchar(255)', 'text'],
    ['bigint', 'integer'],
    ['decimal(30,9)', 'decimal'],
    ['boolean', 'boolean'],
    ['date', 'date'],
    ['timestamp with time zone', 'datetime'],
    ['time', 'time'],
    ['jsonb', 'json'],
    ['bytea', 'blob'],
  ] as const)('maps %s to %s', (dataType, kind) => {
    expect(databaseEditorKind(dataType)).toBe(kind)
  })
})
