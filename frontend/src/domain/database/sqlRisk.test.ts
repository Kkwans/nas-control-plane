import { describe, expect, it } from 'vitest'

import { classifySqlRisk } from './sqlRisk'

describe('classifySqlRisk', () => {
  it('warns before schema-changing statements', () => {
    expect(classifySqlRisk('ALTER TABLE users ADD COLUMN note TEXT')).toMatchObject({ kind: 'schema-change' })
    expect(classifySqlRisk('TRUNCATE TABLE users')).toMatchObject({ kind: 'schema-change' })
  })

  it('warns before UPDATE or DELETE without a WHERE clause', () => {
    expect(classifySqlRisk('DELETE FROM users')).toMatchObject({ kind: 'delete-without-where' })
    expect(classifySqlRisk('UPDATE users SET disabled = 1')).toMatchObject({ kind: 'update-without-where' })
  })

  it('does not warn for scoped mutations or read-only queries', () => {
    expect(classifySqlRisk('DELETE FROM users WHERE id = 1')).toBeNull()
    expect(classifySqlRisk('UPDATE users SET disabled = 1 WHERE id = 1')).toBeNull()
    expect(classifySqlRisk('SELECT * FROM users')).toBeNull()
  })

  it('ignores commented-out SQL keywords', () => {
    expect(classifySqlRisk('-- DROP TABLE users\nSELECT 1')).toBeNull()
    expect(classifySqlRisk('/* DELETE FROM users */ SELECT 1')).toBeNull()
  })
})
