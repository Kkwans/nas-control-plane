// 与 Agent 的二进制输出块及 Server/Manager 的输入上限保持一致。
export const TERMINAL_OUTPUT_FRAME_BYTES = 16 * 1024
export const TERMINAL_PENDING_OUTPUT_BYTES = 256 * 1024
export const TERMINAL_INPUT_BYTES = 32 * 1024
// 浏览器侧发送策略，不代表服务端容量或总内存上限。
export const TERMINAL_SOCKET_BUFFER_BYTES = 64 * 1024

// 不先分配完整 UTF-8 副本；超限后只返回 limit + 1。
export function boundedUtf8Bytes(text: string, limit: number): number {
  let bytes = 0
  for (let index = 0; index < text.length; index++) {
    const code = text.charCodeAt(index)
    if (code < 0x80) bytes++
    else if (code < 0x800) bytes += 2
    else if (code >= 0xd800 && code <= 0xdbff && index + 1 < text.length &&
      text.charCodeAt(index + 1) >= 0xdc00 && text.charCodeAt(index + 1) <= 0xdfff) {
      bytes += 4
      index++
    } else bytes += 3
    if (bytes > limit) return limit + 1
  }
  return bytes
}

// 每个 attempt 单独持有；只统计交给 xterm、尚未完成 write 回调的二进制字节。
export function createTerminalOutputBudget() {
  let pending = 0
  let active = true
  return {
    get pendingBytes() { return pending },
    reserve(bytes: number): (() => void) | null {
      if (!active || !Number.isSafeInteger(bytes) || bytes < 0 || bytes > TERMINAL_OUTPUT_FRAME_BYTES ||
        bytes > TERMINAL_PENDING_OUTPUT_BYTES - pending) return null
      pending += bytes
      let released = false
      return () => {
        if (!active || released) return
        released = true
        pending -= bytes
      }
    },
    dispose() { active = false; pending = 0 },
  }
}
