import { describe, expect, it } from 'vitest'

import { createRequestSequenceGate } from './requestSequence'

describe('request sequence gate', () => {
  it('marks an earlier refresh response stale when a newer one starts', () => {
    const gate = createRequestSequenceGate()
    const first = gate.begin()
    const second = gate.begin()

    expect(gate.isLatest(first)).toBe(false)
    expect(gate.isLatest(second)).toBe(true)
  })

  it('invalidates an in-flight inspection before resetting its state', () => {
    const gate = createRequestSequenceGate()
    const inspection = gate.begin()

    gate.invalidate()

    expect(gate.isLatest(inspection)).toBe(false)
  })
})
