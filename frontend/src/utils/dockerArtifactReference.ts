// 仅用于当前清单引用存在性，不得用于拉取回执归属或历史镜像身份判定。
// 对照 distribution/reference v0.6.0 normalize.go、regexp.go；未知输入不比较。
const component = '[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*'
const pathPattern = new RegExp(`^${component}(?:/${component})*$`)
const domainComponent = '(?:[a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9-]*[a-zA-Z0-9])'
const domainPattern = new RegExp(`^(?:${domainComponent}(?:\\.${domainComponent})*|\\[[a-fA-F0-9:]+\\])(?::[0-9]+)?$`)
const digestPattern = /^(?:sha256:[a-f0-9]{64}|sha384:[a-f0-9]{96}|sha512:[a-f0-9]{128})$/

export function dockerArtifactReferenceKey(value: string | undefined): string | undefined {
  if (!value || /\s/.test(value)) return undefined
  const parts = value.split('@')
  if (parts.length > 2) return undefined
  let name = parts[0]!
  const digest = parts[1]
  if (digest !== undefined && !digestPattern.test(digest)) return undefined
  let tag: string | undefined
  const colon = name.lastIndexOf(':')
  if (colon > name.lastIndexOf('/')) {
    tag = name.slice(colon + 1)
    name = name.slice(0, colon)
    if (!/^[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,127}$/.test(tag)) return undefined
  }
  if (tag === undefined && digest === undefined) return undefined
  let domain = 'docker.io'
  let path = name
  const slash = name.indexOf('/')
  if (slash >= 0) {
    const first = name.slice(0, slash)
    if (first === 'localhost' || /[.:]/.test(first) || first.toLowerCase() !== first) {
      domain = first === 'index.docker.io' ? 'docker.io' : first
      path = name.slice(slash + 1)
    }
  }
  if (domain === 'docker.io' && !path.includes('/')) path = `library/${path}`
  if (!domainPattern.test(domain) || !pathPattern.test(path) || path.length > 255) return undefined
  return `${domain}/${path}${tag === undefined ? '' : `:${tag}`}${digest === undefined ? '' : `@${digest}`}`
}
