import { requestJson } from './systemTransport'
import type { FileEntriesPage, FileEntry, FileEntryType } from './system'

export async function requestPathEntries(
  path: string,
  cursor = '',
  limit = 200,
  fetcher: typeof fetch = fetch,
): Promise<FileEntriesPage> {
  const parameters = new URLSearchParams({ path })
  if (cursor) parameters.set('cursor', cursor)
  parameters.set('limit', String(limit))
  return requestJson(
    `/api/v1/files/entries?${parameters}`,
    {},
    isFileEntriesPage,
    fetcher,
    'FILES_RESPONSE_INVALID',
  )
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

function isFileEntry(value: unknown): value is FileEntry {
  return isRecord(value) &&
    typeof value.name === 'string' &&
    typeof value.path === 'string' &&
    isFileEntryType(value.type) &&
    typeof value.readable === 'boolean'
}

function isFileEntryType(value: unknown): value is FileEntryType {
  return value === 'directory' || value === 'file' || value === 'symlink' || value === 'other'
}

function isFileEntriesPage(value: unknown): value is FileEntriesPage {
  return isRecord(value) &&
    typeof value.path === 'string' &&
    typeof value.parent === 'string' &&
    typeof value.collectedAt === 'string' &&
    Array.isArray(value.entries) && value.entries.every(isFileEntry) &&
    (value.nextCursor === undefined || typeof value.nextCursor === 'string')
}
