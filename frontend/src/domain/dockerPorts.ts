export interface DockerPortMapping {
  hostIp: string
  privatePort: number
  publicPort: number
  protocol: string
}

export interface PortPresentation {
  key: string
  hostIp: string
  hostPort: number
  containerPort: number
  protocol: string
  label: string
  webUrl?: string
}

export function portPresentationKey(port: DockerPortMapping) {
  return `${port.hostIp || '0.0.0.0'}:${port.publicPort}/${(port.protocol || 'tcp').toLowerCase()}->${port.privatePort}`
}

export function hostPortBindingKey(hostIp: string | undefined, hostPort: number | undefined, protocol: string | undefined) {
  return `${hostIp?.trim() || '0.0.0.0'}:${hostPort || 0}/${(protocol || 'tcp').toLowerCase()}`
}

export function presentDockerPorts(
  ports: DockerPortMapping[],
  options: { webUrls?: Record<string, string> } = {},
): PortPresentation[] {
  const result: PortPresentation[] = []
  const seen = new Set<string>()
  for (const port of ports) {
    if (!Number.isInteger(port.publicPort) || port.publicPort <= 0) continue
    const protocol = (port.protocol || 'tcp').toLowerCase()
    const hostIp = port.hostIp || '0.0.0.0'
    const key = portPresentationKey(port)
    if (seen.has(key)) continue
    seen.add(key)
    const hostPort = Number(port.publicPort)
    const webUrl = options.webUrls?.[key]
    const presentation: PortPresentation = {
      key,
      hostIp,
      hostPort,
      containerPort: Number(port.privatePort),
      protocol,
      label: `${hostIp}:${hostPort} → ${port.privatePort}/${protocol}`,
    }
    if (webUrl) presentation.webUrl = webUrl
    result.push(presentation)
  }
  return result
}
