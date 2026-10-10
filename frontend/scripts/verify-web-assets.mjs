import { createHash } from 'node:crypto'
import { readFile, readdir } from 'node:fs/promises'
import { relative, resolve } from 'node:path'
import { pathToFileURL } from 'node:url'

async function listFiles(directory) {
  const files = []
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const path = resolve(directory, entry.name)
    if (entry.isSymbolicLink()) throw new Error('WEB_ASSET_SYMLINK_REJECTED')
    if (entry.isDirectory()) files.push(...await listFiles(path))
    else if (entry.isFile()) files.push(path)
  }
  return files
}

const digest = (bytes) => createHash('sha256').update(bytes).digest('hex')

export async function verifyWebAssets(directory, baseURL, fetcher = fetch) {
  const root = resolve(directory)
  const files = await listFiles(root)
  if (!files.includes(resolve(root, 'index.html'))) throw new Error('WEB_INDEX_MISSING')
  const base = new URL(baseURL)
  if (!['http:', 'https:'].includes(base.protocol) || base.username || base.password || base.search || base.hash) {
    throw new Error('WEB_BASE_URL_INVALID')
  }
  base.pathname = base.pathname.replace(/\/?$/, '/')
  const failed = []
  let cursor = 0
  async function worker() {
    while (cursor < files.length) {
      const file = files[cursor++]
      const name = relative(root, file).split('\\').join('/')
      const url = new URL(name.split('/').map(encodeURIComponent).join('/'), base)
      try {
        const response = await fetcher(url, { cache: 'no-store', signal: AbortSignal.timeout(10000) })
        if (response.status !== 200) throw new Error(`HTTP_${response.status}`)
        const type = response.headers.get('content-type') ?? ''
        if (name.endsWith('.js') && !/(?:java|ecma)script/i.test(type)) throw new Error('JS_CONTENT_TYPE_INVALID')
        if (name.endsWith('.css') && !/text\/css/i.test(type)) throw new Error('CSS_CONTENT_TYPE_INVALID')
        const [expected, received] = await Promise.all([readFile(file), response.arrayBuffer()])
        if (digest(expected) !== digest(new Uint8Array(received))) throw new Error('CONTENT_HASH_MISMATCH')
      } catch (error) {
        failed.push({ asset: name, error: error instanceof Error ? error.message : 'ASSET_CHECK_FAILED' })
      }
    }
  }
  await Promise.all(Array.from({ length: Math.min(6, files.length) }, () => worker()))
  return { ok: failed.length === 0, checked: files.length, failed }
}

if (process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === import.meta.url) {
  if (process.argv.length !== 4) throw new Error('Usage: verify-web-assets.mjs <built-dist> <base-url>')
  const result = await verifyWebAssets(process.argv[2], process.argv[3])
  console.log(JSON.stringify(result, null, 2))
  if (!result.ok) process.exitCode = 1
}
