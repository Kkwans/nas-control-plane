export type TimestampFormatOptions = {
  fractional?: boolean
}

function fractionFromInput(value: string) {
  const match = value.match(/T\d{2}:\d{2}:\d{2}(\.\d{1,9})/) ?? value.match(/\s\d{2}:\d{2}:\d{2}(\.\d{1,9})/)
  return match?.[1] ?? ''
}

/** Format server RFC3339 timestamps in the browser's local timezone. */
export function formatLocalTimestamp(value: string, options: TimestampFormatOptions = {}) {
  const timestamp = new Date(value)
  if (Number.isNaN(timestamp.valueOf())) return '时间未知'

  const pad = (part: number) => String(part).padStart(2, '0')
  const base = `${timestamp.getFullYear()}-${pad(timestamp.getMonth() + 1)}-${pad(timestamp.getDate())} ${pad(timestamp.getHours())}:${pad(timestamp.getMinutes())}:${pad(timestamp.getSeconds())}`
  return options.fractional ? `${base}${fractionFromInput(value)}` : base
}
