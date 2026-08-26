export const monitoringChartTokens = {
  cpu: '#2468d8',
  load: '#d28a1b',
  memory: '#16866a',
  storage: '#7a5bd0',
  receive: '#16866a',
  transmit: '#2468d8',
  temperature: ['#d28a1b', '#c64a59', '#7a5bd0', '#16866a'],
  axis: '#7b8aa1',
  grid: '#e7edf4',
  line: '#dce4ee',
  tooltip: '#14213a',
  tooltipText: '#ffffff',
  legendText: '#53627a',
} as const

export type MonitoringChartToken = keyof typeof monitoringChartTokens
