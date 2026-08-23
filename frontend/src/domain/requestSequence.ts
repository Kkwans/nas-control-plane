export interface RequestSequenceGate {
  begin(): number
  invalidate(): number
  isLatest(sequence: number): boolean
}

export function createRequestSequenceGate(): RequestSequenceGate {
  let current = 0

  return {
    begin() {
      current += 1
      return current
    },
    invalidate() {
      current += 1
      return current
    },
    isLatest(sequence) {
      return sequence === current
    },
  }
}
