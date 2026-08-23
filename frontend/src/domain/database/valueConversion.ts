import type { DatabaseColumn, DatabaseValue } from '@/api/database'

const integerTypePattern = /\b(?:tinyint|smallint|mediumint|int|integer|bigint|serial|bigserial)\b/i
const decimalTypePattern = /\b(?:decimal|numeric|real|double|float)\b/i
const booleanTypePattern = /\b(?:bool|boolean)\b/i
const integerLiteralPattern = /^[+-]?\d+$/
const decimalLiteralPattern = /^[+-]?(?:(?:\d+(?:\.\d*)?)|(?:\.\d+))(?:e[+-]?\d+)?$/i

export class DatabaseValueError extends Error {
  readonly code = 'DATABASE_VALUE_INVALID'

  constructor(message: string) {
    super(message)
    this.name = 'DatabaseValueError'
  }
}

export function resolveDatabaseValue(value: string, column: DatabaseColumn, nullSelected: boolean): DatabaseValue {
  if (nullSelected) {
    if (!column.nullable) throw new DatabaseValueError(`字段「${column.name}」不允许写入 NULL。`)
    return null
  }

  if (integerTypePattern.test(column.dataType)) {
    const normalized = value.trim()
    if (!integerLiteralPattern.test(normalized)) {
      throw new DatabaseValueError(`字段「${column.name}」需要填写整数，空值不能代替 NULL。`)
    }
    return normalized
  }

  if (decimalTypePattern.test(column.dataType)) {
    const normalized = value.trim()
    if (!decimalLiteralPattern.test(normalized)) {
      throw new DatabaseValueError(`字段「${column.name}」需要填写有效数字，空值不能代替 NULL。`)
    }
    return normalized
  }

  if (booleanTypePattern.test(column.dataType)) {
    const normalized = value.trim().toLowerCase()
    if (normalized === 'true' || normalized === '1') return true
    if (normalized === 'false' || normalized === '0') return false
    throw new DatabaseValueError(`字段「${column.name}」需要填写 true/false 或 1/0。`)
  }

  return value
}
