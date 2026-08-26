// @vitest-environment jsdom

import { createApp, h } from 'vue'
import { describe, expect, it } from 'vitest'

import type { DatabaseColumn } from '@/api/database'
import DatabaseCellEditor from './DatabaseCellEditor.vue'

function column(dataType: string, nullable = true): DatabaseColumn {
  return { name: 'value', dataType, nullable, primaryKey: false, position: 1 }
}

describe('DatabaseCellEditor', () => {
  it('uses a precision-preserving numeric input', () => {
    const host = document.createElement('div')
    const app = createApp({
      render: () => h(DatabaseCellEditor, { modelValue: '9007199254740993', column: column('bigint') }),
    })
    app.mount(host)

    expect(host.querySelector('[data-field-kind="integer"]')).not.toBeNull()
    expect(host.querySelector('input')?.getAttribute('inputmode')).toBe('numeric')
    expect(host.querySelector('input')?.value).toBe('9007199254740993')
    app.unmount()
  })

  it('renders boolean columns as a three-state selector when nullable', () => {
    const host = document.createElement('div')
    const app = createApp({
      render: () => h(DatabaseCellEditor, { modelValue: '', column: column('boolean') }),
    })
    app.mount(host)

    expect(host.querySelector('[data-field-kind="boolean"]')).not.toBeNull()
    expect(host.querySelector('.ncp-select')).not.toBeNull()
    app.unmount()
  })

  it('keeps Blob values read-only', () => {
    const host = document.createElement('div')
    const app = createApp({
      render: () => h(DatabaseCellEditor, { modelValue: 'base64:AA==', column: column('bytea') }),
    })
    app.mount(host)

    expect(host.querySelector('[data-field-kind="blob"]')).not.toBeNull()
    expect(host.querySelector('textarea')?.readOnly).toBe(true)
    app.unmount()
  })
})
