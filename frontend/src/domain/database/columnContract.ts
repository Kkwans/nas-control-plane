import type { DatabaseColumn } from '@/api/database'
import { databaseWriteMode } from './valueConversion'

/** 只比较会影响行写入的元数据，不把统计、字段顺序或对象展示类型当作结构变化。 */
export function databaseColumnContract(columns: readonly DatabaseColumn[] | null | undefined): string | null {
  if (columns == null) return '[]'
  if (!Array.isArray(columns)) return null
  const names = new Set<string>()
  const normalized = []
  for (const column of columns) {
    if (!column || typeof column.name !== 'string' || !column.name || names.has(column.name)
      || typeof column.dataType !== 'string' || typeof column.nullable !== 'boolean'
      || typeof column.primaryKey !== 'boolean') return null
    const writeMode = databaseWriteMode(column)
    if (!['required', 'optional-default', 'server-generated'].includes(writeMode)) return null
    names.add(column.name)
    normalized.push({
      name: column.name,
      dataType: column.dataType.trim().replace(/\s+/g, ' ').toLowerCase(),
      nullable: column.nullable,
      primaryKey: column.primaryKey,
      // Go 的 omitempty 与显式 null 都表示未提供默认值；空字符串、0、false 保留各自语义。
      default: column.default ?? null,
      writeMode,
    })
  }
  return JSON.stringify(normalized.sort((left, right) => left.name < right.name ? -1 : left.name > right.name ? 1 : 0))
}
