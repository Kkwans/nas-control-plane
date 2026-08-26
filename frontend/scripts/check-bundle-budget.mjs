import { readdir, stat } from 'node:fs/promises'
import { join } from 'node:path'

const assetsDirectory = new URL('../dist/assets/', import.meta.url)
const budgetBytes = 540 * 1024
const entries = await readdir(assetsDirectory)
const chunks = []

for (const entry of entries) {
  if (!entry.endsWith('.js')) continue
  const size = (await stat(join(assetsDirectory.pathname, entry))).size
  chunks.push({ entry, size })
}

chunks.sort((left, right) => right.size - left.size)
const largest = chunks.slice(0, 5).map(({ entry, size }) => `${entry} ${(size / 1024).toFixed(1)} KiB`)
console.log(`Bundle budget ${budgetBytes / 1024} KiB; largest chunks: ${largest.join(', ')}`)

const oversized = chunks.filter(({ size }) => size > budgetBytes)
if (oversized.length) {
  console.error(`Bundle budget exceeded: ${oversized.map(({ entry, size }) => `${entry} ${(size / 1024).toFixed(1)} KiB`).join(', ')}`)
  process.exitCode = 1
}
