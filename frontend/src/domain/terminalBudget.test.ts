import { describe, expect, it } from 'vitest'
import { boundedUtf8Bytes, createTerminalOutputBudget, TERMINAL_OUTPUT_FRAME_BYTES, TERMINAL_PENDING_OUTPUT_BYTES } from './terminalBudget'

describe('终端 pending 二进制字节预算', () => {
  it('精确 256 KiB，超一字节拒绝；乱序及重复回调仅释放一次', () => {
    const budget = createTerminalOutputBudget()
    const callbacks = Array.from({ length: 16 }, () => budget.reserve(TERMINAL_OUTPUT_FRAME_BYTES)!)
    expect(budget.pendingBytes).toBe(TERMINAL_PENDING_OUTPUT_BYTES)
    expect(budget.reserve(1)).toBeNull()
    callbacks[7]!(); callbacks[7]!(); callbacks[0]!()
    expect(budget.pendingBytes).toBe(14 * TERMINAL_OUTPUT_FRAME_BYTES)
    expect(budget.reserve(TERMINAL_OUTPUT_FRAME_BYTES)).not.toBeNull()
  })
  it('拒绝异常大帧且不改变预算', () => {
    const budget = createTerminalOutputBudget()
    for (const size of [TERMINAL_OUTPUT_FRAME_BYTES + 1, -1, NaN, Infinity, 1.5]) expect(budget.reserve(size)).toBeNull()
    expect(budget.pendingBytes).toBe(0)
  })
  it('dispose 后旧回调不能计数为负或改动新 owner', () => {
    const old = createTerminalOutputBudget()
    const release = old.reserve(10)!
    old.dispose(); old.dispose()
    const next = createTerminalOutputBudget()
    next.reserve(20)
    release(); release()
    expect(old.pendingBytes).toBe(0)
    expect(old.reserve(1)).toBeNull()
    expect(next.pendingBytes).toBe(20)
  })
  it.each(['ASCII', '中文', '😀', '\ud800', '\udc00', '\ud800x', '\u0080\u0800'])('UTF-8 计数与编码器一致：%s', text => {
    const bytes = new TextEncoder().encode(text).byteLength
    expect(boundedUtf8Bytes(text, bytes)).toBe(bytes)
    expect(boundedUtf8Bytes(text, bytes - 1)).toBe(bytes)
    expect(boundedUtf8Bytes(text, 0)).toBe(1)
  })
})
