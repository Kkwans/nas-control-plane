const durationUnits: Record<string, string> = {
  second: '秒',
  minute: '分钟',
  hour: '小时',
  day: '天',
  week: '周',
  month: '个月',
  year: '年',
}

function formatDuration(value: string, ago: boolean) {
  const normalized = value.trim()
  let label = normalized
  if (/^about an hour$/i.test(normalized)) label = '约 1 小时'
  else if (/^less than a second$/i.test(normalized)) label = '不足 1 秒'
  else if (/^(?:a|an) (second|minute|hour|day|week|month|year)$/i.test(normalized)) {
    const unit = normalized.match(/(second|minute|hour|day|week|month|year)$/i)?.[1]?.toLowerCase() ?? ''
    label = `1 ${durationUnits[unit] ?? unit}`
  } else {
    const match = normalized.match(/^(\d+)\s+(seconds?|minutes?|hours?|days?|weeks?|months?|years?)$/i)
    if (match) {
      const unit = (match[2] ?? '').toLowerCase().replace(/s$/, '')
      label = `${match[1]} ${durationUnits[unit] ?? unit}`
    }
  }
  return ago ? `${label}前` : label
}

function splitHealth(raw: string) {
  const healthMatch = raw.match(/\s+\((healthy|unhealthy|health:\s*starting)\)\s*$/i)
  if (!healthMatch) return { status: raw.trim(), health: '' }
  const healthValue = healthMatch[1] ?? ''
  const health = healthValue.toLowerCase() === 'healthy'
    ? '健康'
    : healthValue.toLowerCase() === 'unhealthy'
      ? '异常'
      : '健康检查中'
  return { status: raw.slice(0, healthMatch.index).trim(), health: `（${health}）` }
}

export function formatDockerContainerStatus(rawStatus: string): string {
  const raw = rawStatus.trim()
  if (!raw) return '状态未知'

  const { status, health } = splitHealth(raw)
  const up = status.match(/^Up\s+(.+)$/i)
  if (up?.[1]) return `已运行 ${formatDuration(up[1], false)}${health}`

  const exited = status.match(/^Exited\s+\((-?\d+)\)(?:\s+(.+?)\s+ago)?$/i)
  if (exited) {
    const elapsed = exited[2] ? ` · ${formatDuration(exited[2], true)}` : ''
    return `已退出（代码 ${exited[1] ?? '未知'}）${elapsed}`
  }

  const restarting = status.match(/^Restarting\s+\((-?\d+)\)(?:\s+(.+?)\s+ago)?$/i)
  if (restarting) {
    const elapsed = restarting[2] ? ` · ${formatDuration(restarting[2], true)}` : ''
    return `正在重启（代码 ${restarting[1] ?? '未知'}）${elapsed}`
  }

  const fixed: Record<string, string> = {
    created: '已创建',
    paused: '已暂停',
    dead: '异常终止',
    'removal in progress': '正在删除',
  }
  return fixed[status.toLowerCase()] ?? raw
}
