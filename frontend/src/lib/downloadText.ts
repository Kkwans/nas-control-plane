export interface DownloadTextFile { text: string; filename: string }

// 一次点击同步交接浏览器；锁一直保留到下一任务释放 URL，不持久保存内容。
export function createTextDownloader(onBusy: (busy: boolean) => void = () => {}) {
  let busy = false
  let disposed = false
  let objectUrl: string | undefined
  let timer: ReturnType<typeof setTimeout> | undefined
  let anchor: HTMLAnchorElement | undefined

  function release() {
    if (timer !== undefined) clearTimeout(timer)
    timer = undefined
    anchor?.remove()
    anchor = undefined
    if (objectUrl !== undefined) URL.revokeObjectURL(objectUrl)
    objectUrl = undefined
    busy = false
    onBusy(false)
  }

  function download(build: () => DownloadTextFile, isCurrent: () => boolean): boolean {
    if (disposed || busy || !isCurrent()) return false
    busy = true
    onBusy(true)
    try {
      const file = build()
      if (disposed || !isCurrent()) { release(); return false }
      objectUrl = URL.createObjectURL(new Blob([file.text], { type: 'text/plain;charset=utf-8' }))
      anchor = document.createElement('a')
      anchor.href = objectUrl
      anchor.download = file.filename
      anchor.hidden = true
      document.body.append(anchor)
      if (disposed || !isCurrent()) { release(); return false }
      anchor.click()
      // click 可同步触发导航/会话清理；不要再次访问已释放的 anchor 或安排迟到清理。
      if (disposed || !isCurrent()) { release(); return false }
      anchor?.remove()
      anchor = undefined
      timer = setTimeout(release, 0)
      return true
    } catch (error) {
      release()
      throw error
    }
  }

  function dispose() { disposed = true; release() }
  return { download, dispose }
}
