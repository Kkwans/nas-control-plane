import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { chmod, mkdtemp, mkdir, readFile, rm, stat, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'
import { verifyWebAssets } from './verify-web-assets.mjs'

const scripts = dirname(fileURLToPath(import.meta.url))
async function fixture(t) {
  const root = await mkdtemp(join(tmpdir(), 'ncp-web-'))
  t.after(() => rm(root, { recursive: true, force: true }))
  await mkdir(join(root, 'assets'))
  await writeFile(join(root, 'index.html'), '<html>entry</html>')
  await writeFile(join(root, 'assets', 'lazy-page.js'), 'export const loaded = true')
  return root
}

test('rejects a lazy page returning 403 even when the entrypoint works', async (t) => {
  const root = await fixture(t)
  const result = await verifyWebAssets(root, 'http://example.test/', async (url) => {
    if (url.pathname.endsWith('.js')) return new Response('forbidden', { status: 403 })
    return new Response(await readFile(join(root, 'index.html')))
  })
  assert.equal(result.ok, false)
  assert.equal(result.failed[0].error, 'HTTP_403')
})

test('rejects HTML fallback and stale JavaScript bytes', async (t) => {
  const root = await fixture(t)
  const result = await verifyWebAssets(root, 'http://example.test/', async (url) => {
    if (url.pathname.endsWith('.js')) return new Response('<html>fallback</html>', { headers: { 'Content-Type': 'text/html' } })
    return new Response(await readFile(join(root, 'index.html')))
  })
  assert.equal(result.failed[0].error, 'JS_CONTENT_TYPE_INVALID')
  const stale = await verifyWebAssets(root, 'http://example.test/', async () => new Response('old', { headers: { 'Content-Type': 'text/javascript' } }))
  assert.equal(stale.ok, false)
  assert.ok(stale.failed.some((failure) => failure.error === 'CONTENT_HASH_MISMATCH'))
})

test('normalizes transferred private file modes and accepts matching served bytes', async (t) => {
  const root = await fixture(t)
  await chmod(join(root, 'assets', 'lazy-page.js'), 0o600)
  execFileSync(process.execPath, [join(scripts, 'normalize-dist-permissions.mjs'), root])
  assert.equal((await stat(join(root, 'assets', 'lazy-page.js'))).mode & 0o777, 0o644)
  const result = await verifyWebAssets(root, 'http://example.test/', async (url) => new Response(await readFile(join(root, url.pathname)), {
    headers: { 'Content-Type': url.pathname.endsWith('.js') ? 'text/javascript' : 'text/html' },
  }))
  assert.deepEqual(result, { ok: true, checked: 2, failed: [] })
})
