export type LogTokenTone =
  | 'method'
  | 'success'
  | 'danger'
  | 'warning'
  | 'string'
  | 'path'
  | 'field'
  | 'punctuation'

export interface LogToken {
  text: string
  tone?: LogTokenTone
}

const tokenPattern = /(HTTP\/\d(?:\.\d)?\s+[1-5]\d{2}|\b(?:GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)\b|\b(?:error|warn(?:ing)?|fatal|panic|exception|timeout|success|ready)\b|"(?:\\.|[^"])*"|'(?:\\.|[^'])*'|(?:\/[\w.%~+-]+)+|[A-Za-z_][\w.-]*(?=\s*[:=])|[{}[\],:])/gi

function tokenTone(text: string): LogTokenTone | undefined {
  const normalized = text.toLowerCase()
  if (/^(get|post|put|patch|delete|head|options)$/.test(normalized)) return 'method'
  if (/^http\/\d(?:\.\d)?\s+[1-5]\d{2}$/i.test(text)) return /\s[23]\d{2}$/.test(text) ? 'success' : 'danger'
  if (/^(error|warning|warn|fatal|panic|exception)$/.test(normalized)) return 'danger'
  if (/^(success|ready)$/.test(normalized)) return 'success'
  if (/^timeout$/.test(normalized)) return 'warning'
  if (/^["']/.test(text)) return 'string'
  if (/^\//.test(text)) return 'path'
  if (/^[A-Za-z_][\w.-]*$/.test(text)) return 'field'
  if (/^[{}[\],:]$/.test(text)) return 'punctuation'
  return undefined
}

/** Tokenize a log message for the Logs Center and Docker log reader. */
export function logTokens(message: string): LogToken[] {
  return message.split(tokenPattern).filter(Boolean).map((text) => ({ text, tone: tokenTone(text) }))
}
