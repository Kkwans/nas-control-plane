import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createTextDownloader } from './downloadText'

const file = { text: '中文 😀\n', filename: 'ncp-logs-system-page-1-20261013T010807006Z.txt' }

function setup() {
  const anchor = { href: '', download: '', hidden: false, click: vi.fn(), remove: vi.fn() }
  const createElement = vi.fn(() => anchor)
  const append = vi.fn()
  vi.stubGlobal('document', { createElement, body: { append } })
  let count = 0
  const createObjectURL = vi.spyOn(URL, 'createObjectURL').mockImplementation(() => `blob:测试-${++count}`)
  const revokeObjectURL = vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})
  const onBusy = vi.fn()
  const downloader = createTextDownloader(onBusy)
  const build = vi.fn(() => ({ ...file }))
  const isCurrent = vi.fn(() => true)
  return { anchor, createElement, append, createObjectURL, revokeObjectURL, onBusy, downloader, build, isCurrent }
}

beforeEach(() => { vi.useFakeTimers() })
afterEach(() => {
  vi.clearAllTimers()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

describe('文本下载交接及资源生命周期', () => {
  it('点击栈内使用 UTF-8 纯文本 Blob 和临时隐藏 anchor，下一任务才回收 URL', async () => {
    const h = setup()
    h.anchor.click.mockImplementation(() => {
      expect(h.revokeObjectURL).not.toHaveBeenCalled()
      expect(h.anchor.remove).not.toHaveBeenCalled()
      expect(h.append).toHaveBeenCalledWith(h.anchor)
    })
    expect(h.downloader.download(h.build, h.isCurrent)).toBe(true)
    expect(h.build).toHaveBeenCalledOnce()
    expect(h.createObjectURL).toHaveBeenCalledOnce()
    const blob = h.createObjectURL.mock.calls[0]![0] as Blob
    expect(blob).toBeInstanceOf(Blob)
    expect(blob.type).toBe('text/plain;charset=utf-8')
    expect(await blob.text()).toBe(file.text)
    expect(blob.size).toBe(new TextEncoder().encode(file.text).byteLength)
    expect(h.createElement).toHaveBeenCalledWith('a')
    expect(h.anchor).toMatchObject({ href: 'blob:测试-1', download: file.filename, hidden: true })
    expect(h.anchor.click).toHaveBeenCalledOnce()
    expect(h.anchor.remove).toHaveBeenCalledOnce()
    expect(h.revokeObjectURL).not.toHaveBeenCalled()
    expect(h.onBusy.mock.calls).toEqual([[true]])
    vi.runAllTimers()
    expect(h.revokeObjectURL.mock.calls).toEqual([['blob:测试-1']])
    expect(h.anchor.remove).toHaveBeenCalledOnce()
    expect(h.onBusy.mock.calls).toEqual([[true], [false]])
    expect(vi.getTimerCount()).toBe(0)
  })

  it('锁覆盖构建、同步 click 重入和延迟清理，释放后再次点击可重新下载', () => {
    const h = setup()
    const nestedBuild = vi.fn(() => file)
    h.build.mockImplementation(() => {
      expect(h.downloader.download(nestedBuild, h.isCurrent)).toBe(false)
      return file
    })
    h.anchor.click.mockImplementation(() => {
      expect(h.downloader.download(nestedBuild, h.isCurrent)).toBe(false)
    })
    expect(h.downloader.download(h.build, h.isCurrent)).toBe(true)
    expect(h.downloader.download(nestedBuild, h.isCurrent)).toBe(false)
    expect(nestedBuild).not.toHaveBeenCalled()
    expect(h.createObjectURL).toHaveBeenCalledOnce()
    vi.runAllTimers()
    expect(h.downloader.download(h.build, h.isCurrent)).toBe(true)
    expect(h.createObjectURL).toHaveBeenCalledTimes(2)
    expect(h.anchor.click).toHaveBeenCalledTimes(2)
    vi.runAllTimers()
    expect(h.revokeObjectURL.mock.calls).toEqual([['blob:测试-1'], ['blob:测试-2']])
  })

  it.each(['build', 'Blob', 'createObjectURL', 'createElement', 'append', 'click'] as const)('%s 抛错不留下资源或锁，可以明确重试', (stage) => {
    const h = setup()
    const failure = new Error(`${stage} 失败`)
    if (stage === 'build') h.build.mockImplementationOnce(() => { throw failure })
    else if (stage === 'Blob') vi.stubGlobal('Blob', vi.fn(function () { throw failure }))
    else if (stage === 'createObjectURL') h.createObjectURL.mockImplementationOnce(() => { throw failure })
    else if (stage === 'createElement') h.createElement.mockImplementationOnce(() => { throw failure })
    else if (stage === 'append') h.append.mockImplementationOnce(() => { throw failure })
    else h.anchor.click.mockImplementationOnce(() => { throw failure })
    expect(() => h.downloader.download(h.build, h.isCurrent)).toThrow(failure)
    const acquiredUrl = ['createElement', 'append', 'click'].includes(stage)
    expect(h.revokeObjectURL).toHaveBeenCalledTimes(acquiredUrl ? 1 : 0)
    expect(h.anchor.remove).toHaveBeenCalledTimes(['append', 'click'].includes(stage) ? 1 : 0)
    expect(h.onBusy.mock.calls).toEqual([[true], [false]])
    expect(vi.getTimerCount()).toBe(0)
    if (stage === 'Blob') {
      vi.unstubAllGlobals()
      vi.stubGlobal('document', { createElement: h.createElement, body: { append: h.append } })
    }
    expect(h.downloader.download(h.build, h.isCurrent)).toBe(true)
    vi.runAllTimers()
    expect(h.revokeObjectURL).toHaveBeenCalledTimes(acquiredUrl ? 2 : 1)
    expect(h.onBusy.mock.calls).toEqual([[true], [false], [true], [false]])
  })

  it('无效原始会话从入口阻止构建和下载', () => {
    const h = setup()
    h.isCurrent.mockReturnValue(false)
    expect(h.downloader.download(h.build, h.isCurrent)).toBe(false)
    expect(h.build).not.toHaveBeenCalled()
    expect(h.createObjectURL).not.toHaveBeenCalled()
    expect(h.anchor.click).not.toHaveBeenCalled()
    expect(h.onBusy).not.toHaveBeenCalled()
  })

  it('构建期间失效的会话不能获得对象 URL 或产生迟到点击', () => {
    const h = setup()
    h.isCurrent.mockReturnValueOnce(true).mockReturnValue(false)
    expect(h.downloader.download(h.build, h.isCurrent)).toBe(false)
    expect(h.build).toHaveBeenCalledOnce()
    expect(h.createObjectURL).not.toHaveBeenCalled()
    expect(h.anchor.click).not.toHaveBeenCalled()
    expect(h.onBusy.mock.calls).toEqual([[true], [false]])
    h.isCurrent.mockReturnValue(true)
    expect(h.downloader.download(h.build, h.isCurrent)).toBe(true)
    vi.runAllTimers()
  })

  it('anchor 创建后失效会回收对象 URL 和 anchor，不调用 click', () => {
    const h = setup()
    h.isCurrent.mockReturnValueOnce(true).mockReturnValueOnce(true).mockReturnValue(false)
    expect(h.downloader.download(h.build, h.isCurrent)).toBe(false)
    expect(h.createObjectURL).toHaveBeenCalledOnce()
    expect(h.anchor.click).not.toHaveBeenCalled()
    expect(h.anchor.remove).toHaveBeenCalledOnce()
    expect(h.revokeObjectURL.mock.calls).toEqual([['blob:测试-1']])
    expect(vi.getTimerCount()).toBe(0)
    expect(h.onBusy.mock.calls).toEqual([[true], [false]])
  })

  it('构建期间 dispose 永久阻止当前和之后的下载', () => {
    const h = setup()
    h.build.mockImplementation(() => { h.downloader.dispose(); return file })
    expect(h.downloader.download(h.build, h.isCurrent)).toBe(false)
    expect(h.createObjectURL).not.toHaveBeenCalled()
    expect(h.anchor.click).not.toHaveBeenCalled()
    expect(h.downloader.download(h.build, h.isCurrent)).toBe(false)
    expect(h.build).toHaveBeenCalledOnce()
  })

  it('组件卸载或会话终止立即回收资源、取消定时器，重复清理无额外回收', () => {
    const h = setup()
    expect(h.downloader.download(h.build, h.isCurrent)).toBe(true)
    h.isCurrent.mockReturnValue(false)
    h.downloader.dispose()
    expect(h.revokeObjectURL.mock.calls).toEqual([['blob:测试-1']])
    expect(h.anchor.remove).toHaveBeenCalledOnce()
    expect(vi.getTimerCount()).toBe(0)
    h.downloader.dispose()
    vi.runAllTimers()
    expect(h.revokeObjectURL).toHaveBeenCalledOnce()
    h.isCurrent.mockReturnValue(true)
    expect(h.downloader.download(h.build, h.isCurrent)).toBe(false)
    expect(h.build).toHaveBeenCalledOnce()
  })

  it('click 同步触发失活清理时不抛错、不重复回收或留下定时器', () => {
    const h = setup()
    h.anchor.click.mockImplementation(() => { h.downloader.dispose() })
    expect(() => h.downloader.download(h.build, h.isCurrent)).not.toThrow()
    expect(h.anchor.click).toHaveBeenCalledOnce()
    expect(h.anchor.remove).toHaveBeenCalledOnce()
    expect(h.revokeObjectURL).toHaveBeenCalledOnce()
    expect(vi.getTimerCount()).toBe(0)
    expect(h.downloader.download(h.build, h.isCurrent)).toBe(false)
  })
})
