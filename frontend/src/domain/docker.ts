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
  if (/^about (?:a|an) (second|minute|hour|day|week|month|year)$/i.test(normalized)) {
    const unit = normalized.match(/(second|minute|hour|day|week|month|year)$/i)?.[1]?.toLowerCase() ?? ''
    label = `约 1 ${durationUnits[unit] ?? unit}`
  }
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

export function dockerContainerStateLabel(state: string): string {
  const labels: Record<string, string> = {
    running: '运行中', exited: '已停止', created: '已创建', paused: '已暂停',
    restarting: '正在重启', removing: '正在删除', dead: '异常终止',
  }
  return labels[state.trim().toLowerCase()] ?? '状态未知'
}

export function dockerContainerStateDetail(container: {
  state: string
  health?: string
  exitCode?: number
  restartCount?: number
}): string {
  if (container.state === 'running') {
    if (container.health === 'healthy') return '健康'
    if (container.health === 'unhealthy') return '健康检查异常'
    if (container.health === 'starting') return '健康检查中'
    return '运行正常'
  }
  if (container.state === 'exited' || container.state === 'dead') return `退出码 ${container.exitCode ?? '未知'}`
  if (container.state === 'restarting') return (container.restartCount ?? 0) > 0 ? `已重启 ${container.restartCount} 次` : '正在恢复服务'
  if (container.state === 'created') return '等待首次启动'
  if (container.state === 'paused') return '进程已暂停'
  return '等待状态更新'
}

function formatShortDateTime(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.valueOf())) return '时间未知'
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false,
  }).format(date)
}

function formatElapsed(milliseconds: number): string {
  const seconds = Math.max(0, Math.floor(milliseconds / 1000))
  if (seconds < 60) return `${Math.max(1, seconds)} 秒`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes} 分钟`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours} 小时 ${minutes % 60} 分钟`
  const days = Math.floor(hours / 24)
  return `${days} 天 ${hours % 24} 小时`
}

export function dockerContainerTimingLabel(container: {
  state: string
  createdAt: string
  startedAt?: string
  finishedAt?: string
}, now = Date.now()): string {
  const created = `创建 ${formatShortDateTime(container.createdAt)}`
  if (container.state === 'running' && container.startedAt) {
    const startedAt = new Date(container.startedAt).valueOf()
    if (Number.isFinite(startedAt)) return `${created} · 已运行 ${formatElapsed(now - startedAt)}`
  }
  if (container.finishedAt) return `${created} · 停止 ${formatShortDateTime(container.finishedAt)}`
  return created
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
