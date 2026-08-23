export type SqlRiskKind = 'schema-change' | 'delete-without-where' | 'update-without-where'

export interface SqlRisk {
  kind: SqlRiskKind
  title: string
  hint: string
  confirmation: string
}

export function classifySqlRisk(sql: string): SqlRisk | null {
  const normalized = sql
    .replace(/--[^\n]*/g, ' ')
    .replace(/\/\*[\s\S]*?\*\//g, ' ')
    .trim()
  if (!normalized) return null

  if (/\b(drop|truncate|alter)\b/i.test(normalized)) {
    return {
      kind: 'schema-change',
      title: '确认高影响 SQL',
      hint: '此语句可能删除数据或改变表结构。',
      confirmation: '该 SQL 包含 DROP、TRUNCATE 或 ALTER，可能删除数据、改变表结构或影响现有应用。NCP 只提示风险，不限制 Root 执行能力；请确认目标数据库和语句范围。',
    }
  }

  if (/\bdelete\b/i.test(normalized) && !/\bwhere\b/i.test(normalized)) {
    return {
      kind: 'delete-without-where',
      title: '确认无范围 DELETE',
      hint: 'DELETE 未包含 WHERE，可能影响整张表。',
      confirmation: '该 DELETE 未包含 WHERE 条件，可能删除整张表的数据。NCP 只提示风险，不限制 Root 执行能力；请确认这是预期操作。',
    }
  }

  if (/\bupdate\b/i.test(normalized) && !/\bwhere\b/i.test(normalized)) {
    return {
      kind: 'update-without-where',
      title: '确认无范围 UPDATE',
      hint: 'UPDATE 未包含 WHERE，可能修改整张表。',
      confirmation: '该 UPDATE 未包含 WHERE 条件，可能修改整张表的数据。NCP 只提示风险，不限制 Root 执行能力；请确认这是预期操作。',
    }
  }

  return null
}
