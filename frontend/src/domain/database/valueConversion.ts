import type { DatabaseColumn, DatabaseValue } from '@/api/database'

const integerTypePattern = /\b(?:tinyint|smallint|mediumint|int|integer|bigint|serial|bigserial)\b/i
const decimalTypePattern = /\b(?:decimal|numeric|real|double|float)\b/i
const booleanTypePattern = /\b(?:bool|boolean)\b/i
const jsonTypePattern = /\bjson(?:b)?\b/i
const blobTypePattern = /\b(?:blob|binary|varbinary|bytea|longblob|mediumblob|tinyblob)\b/i
const integerLiteralPattern = /^[+-]?\d+$/
const decimalLiteralPattern = /^[+-]?(?:(?:\d+(?:\.\d*)?)|(?:\.\d+))(?:e[+-]?\d+)?$/i

export type DatabaseEditorKind = 'text' | 'integer' | 'decimal' | 'boolean' | 'date' | 'datetime' | 'time' | 'json' | 'blob'

export function databaseEditorKind(dataType: string): DatabaseEditorKind {
  const type = dataType.toLowerCase()
  if (blobTypePattern.test(type)) return 'blob'
  if (jsonTypePattern.test(type)) return 'json'
  if (booleanTypePattern.test(type)) return 'boolean'
  if (integerTypePattern.test(type)) return 'integer'
  if (decimalTypePattern.test(type)) return 'decimal'
  if (/\bdatetime\b|\btimestamp\b/.test(type)) return 'datetime'
  if (/\bdate\b/.test(type)) return 'date'
  if (/\btime\b/.test(type)) return 'time'
  return 'text'
}

function isTimezoneAware(dataType: string) {
  return /timestamptz|timestamp\s+with\s+time\s+zone|datetimeoffset|with\s+time\s+zone/i.test(dataType)
}

function pad(value: number) {
  return String(value).padStart(2, '0')
}

/** Convert a wire timestamp into the browser's datetime-local wall clock. */
export function databaseEditorValue(value: DatabaseValue | undefined, column: DatabaseColumn): string {
  if (value === null || value === undefined) return ''
  const text = String(value)
  if (databaseEditorKind(column.dataType) !== 'datetime') return text
  if (!isTimezoneAware(column.dataType)) return text.replace(' ', 'T').replace(/Z$/i, '')
  const date = new Date(text)
  if (!Number.isFinite(date.valueOf())) return text.replace(' ', 'T')
  const milliseconds = date.getMilliseconds()
  const fraction = milliseconds ? `.${String(milliseconds).padStart(3, '0')}` : ''
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}${fraction}`
}

/** Convert datetime-local input back to an instant or a timezone-naive value. */
export function databaseWireValue(value: string, column: DatabaseColumn): string {
  if (databaseEditorKind(column.dataType) !== 'datetime' || !value.trim()) return value
  if (!isTimezoneAware(column.dataType)) return value.replace('T', ' ')
  const date = new Date(value)
  return Number.isFinite(date.valueOf()) ? date.toISOString() : value
}

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

  if (databaseEditorKind(column.dataType) === 'datetime') {
    return databaseWireValue(value, column)
  }

  if (jsonTypePattern.test(column.dataType)) {
    try {
      JSON.parse(value)
    } catch {
      throw new DatabaseValueError(`字段「${column.name}」需要填写有效 JSON。`)
    }
  }

  return value
}
