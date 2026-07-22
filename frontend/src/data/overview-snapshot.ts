interface PreviewSnapshot {
  hostname: string
  updatedAt: string
  cpu: { usage: number; trend: number[] }
  memory: { usedGiB: number; totalGiB: number }
  storage: { usedTiB: number; totalTiB: number }
  network: { downMbps: number; upMbps: number }
  docker: { available: boolean; activeContainers: number }
}

export const previewSnapshot: PreviewSnapshot = {
  hostname: 'DH4300-PLUS',
  updatedAt: '2026-07-19T09:30:00+08:00',
  cpu: {
    usage: 36.8,
    trend: [23.4, 27.8, 25.1, 34.6, 31.2, 36.8, 33.7, 38.5, 36.8],
  },
  memory: {
    usedGiB: 8.7,
    totalGiB: 15.6,
  },
  storage: {
    usedTiB: 11.4,
    totalTiB: 24,
  },
  network: {
    downMbps: 13.8,
    upMbps: 4.2,
  },
  docker: {
    available: true,
    activeContainers: 14,
  },
}

export const previewServices = [
  {
    name: 'Docker Engine',
    detail: '容器编排与运行时可观测性',
    state: 'healthy' as const,
    value: '14 个运行中容器',
  },
  {
    name: 'NCP Agent Socket',
    detail: '受控主机能力通道',
    state: 'healthy' as const,
    value: 'P0-03 已完成',
  },
  {
    name: '系统日志',
    detail: 'journald 查询与实时订阅',
    state: 'pending' as const,
    value: '等待 P0-05 验证',
  },
]
