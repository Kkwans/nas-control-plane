import { chmod, readdir } from 'node:fs/promises'
import { resolve } from 'node:path'

const distDirectory = resolve(process.cwd(), 'dist')

async function normalizeDirectory(directory) {
  await chmod(directory, 0o755)
  const entries = await readdir(directory, { withFileTypes: true })
  await Promise.all(entries.map(async (entry) => {
    const path = resolve(directory, entry.name)
    if (entry.isDirectory()) {
      await normalizeDirectory(path)
      return
    }
    if (entry.isFile()) {
      await chmod(path, 0o644)
    }
  }))
}

await normalizeDirectory(distDirectory)
