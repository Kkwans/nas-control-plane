import type { TerminalLease, TerminalPageOwner } from '@/domain/terminalPageOwner'
import { computed, nextTick, onBeforeUnmount, ref, shallowRef, watch, type Ref } from 'vue'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'

import {
  createTerminalPastePayload,
  describeTerminalCapability,
  formatTerminalEnhancement,
  formatTerminalShell,
  getTerminalCapabilityState,
  isMultilineTerminalPaste,
  isTerminalLiteralNextShortcut,
  isTerminalPasteShortcut,
  normalizeTerminalCapabilities,
  normalizeTerminalPaste,
  supportsSafeMultilinePaste,
  type TerminalCapabilities,
  type TerminalCapabilityState,
} from '@/domain/terminal'
import { boundedUtf8Bytes, createTerminalOutputBudget, TERMINAL_INPUT_BYTES, TERMINAL_SOCKET_BUFFER_BYTES } from '@/domain/terminalBudget'
import { getTerminalTheme } from '@/domain/terminalTheme'
import { captureSession, isCurrentSession, type SessionContext } from '@/session/sessionLifecycle'

type Target = 'host' | 'container'
const terminalErrorMessages: Record<string, string> = {
  TERMINAL_SESSION_LIMIT_REACHED: '终端会话名额暂不可用，服务端可能仍在收尾，请稍后手动重试。',
  TERMINAL_UNAVAILABLE: '终端服务暂不可用，请确认 Root Agent 正常运行。',
  TERMINAL_INITIALIZATION_FAILED: '终端会话初始化失败，请检查 Agent 终端能力。',
  TERMINAL_INITIALIZATION_TIMEOUT: '终端初始化超时，请稍后重试。',
  TERMINAL_TARGET_UNAVAILABLE: '当前终端目标不可用，请确认主机或容器状态。',
  TERMINAL_INPUT_INVALID: '终端输入无效或超出允许范围。',
  TERMINAL_SESSION_FAILED: '终端会话异常结束，请重新连接。',
}
type PanelOptions = {
  tabId: string
  page: TerminalPageOwner
  containers: Ref<{ id: string; name: string; image: string; state: string }[]>
  reducedMotion: Ref<boolean>
}
export function useTerminalPanel({ tabId, page, containers, reducedMotion }: PanelOptions) {
  type PendingPaste = { owner: Attempt; text: string; lineCount: number; safe: boolean }
  let attached = true
  let focusEpoch = 0
  function focusIntent() {
    const origin = document.activeElement
    const epoch = focusEpoch
    const selection = page.selectionEpoch
    return () => attached && page.selected(tabId) && epoch === focusEpoch && selection === page.selectionEpoch &&
      (document.activeElement === origin || (origin instanceof HTMLElement && !origin.isConnected && document.activeElement === document.body))
  }
  const cancelFocus = () => { focusEpoch++ }
  document.addEventListener('focusin', cancelFocus)
  const target = ref<Target>('host')
  const containerId = ref('')
  const state = ref<'idle' | 'connecting' | 'connected' | 'closing' | 'closed' | 'error'>('idle')
  const shellName = ref('')
  const shellEnhancement = ref('')
  const shellReason = ref('')
  const capabilities = ref<TerminalCapabilities | null>(null)
  const statusMessage = ref('')
  const shortcutDialogOpen = ref(false)
  const pendingPaste = shallowRef<PendingPaste | null>(null)
  const pasteConfirmRef = ref<HTMLButtonElement | null>(null)
  const terminalElement = ref<HTMLElement | null>(null)
  const pasteTitleId = `terminal-paste-title-${tabId}`
  const pasteDescriptionId = `terminal-paste-description-${tabId}`
  // 每次尝试独占资源；退役后仅其 close 事件能完成本地收尾。
  type Attempt = {
    generation: number
    lease: TerminalLease
    focus: () => boolean
    session: SessionContext
    target: Target
    containerId: string
    title: string
    active: boolean
    ready: boolean
    cleaned: boolean
    outputBudget: ReturnType<typeof createTerminalOutputBudget>
    socket: WebSocket | null
    terminal: Terminal | null
    fit: FitAddon | null
    timers: { handshake?: number; resize?: number }
    releases: (() => void)[]
    outcome: 'closed' | 'error'
  }
  let current: Attempt | null = null
  let attemptGeneration = 0
  const targetLocked = computed(() => ['connecting', 'connected', 'closing'].includes(state.value))
  const terminalTitle = ref('')
  const toolbarActions = ref<HTMLElement | null>(null)

  function pageCurrent() {
    return attached && page.current()
  }
  function owns(owner: Attempt, socket = owner.socket) {
    return pageCurrent() && current === owner && owner.active && owner.socket === socket &&
      !owner.session.signal.aborted && isCurrentSession(owner.session.generation)
  }
  function ready(owner: Attempt) {
    return page.selected(tabId) && owns(owner) && owner.ready && owner.socket?.readyState === WebSocket.OPEN
  }
  function safely(action: () => void) {
    try { action() } catch { /* 单项清理失败不能阻断其余资源收尾。 */ }
  }

  const containerOptions = computed(() => containers.value.map((item) => ({ label: `${item.name} · ${item.image}`, value: item.id })))
  const canConnect = computed(() => !targetLocked.value && page.occupiedCount < page.limit && (target.value === 'host' || Boolean(containerId.value)))
  const canClearTerminal = computed(() => state.value === 'connecting' || state.value === 'connected' || state.value === 'error')
  const stateLabel = computed(() => ({
    idle: '未连接',
    connecting: '连接中',
    connected: '已连接',
    closing: '关闭中',
    closed: '已断开',
    error: '连接失败',
  }[state.value]))
  const shellLabel = computed(() => formatTerminalShell(shellName.value))
  const enhancementLabel = computed(() => formatTerminalEnhancement(shellEnhancement.value))
  const editorCapabilityItems = computed(() => {
    const readlineState = getTerminalCapabilityState(capabilities.value, 'readline')
    let highlightingState: TerminalCapabilityState = 'unknown'
    if (shellEnhancement.value === 'blesh' && readlineState !== 'unsupported') highlightingState = 'supported'
    else if (shellEnhancement.value === 'native' || shellEnhancement.value === 'readline' || shellEnhancement.value === 'unsupported') highlightingState = 'unsupported'
    return [
      { key: 'completion', label: '命令补全', state: readlineState },
      { key: 'highlighting', label: '输入高亮', state: highlightingState },
    ]
  })
  const capabilityItems = computed(() => [
    { key: 'resize' as const, label: '窗口调整', value: describeTerminalCapability(capabilities.value?.resize) },
    { key: 'readline' as const, label: 'readline', value: describeTerminalCapability(capabilities.value?.readline) },
    { key: 'bracketedPaste' as const, label: 'bracketed paste', value: describeTerminalCapability(capabilities.value?.bracketedPaste) },
    { key: 'multilinePaste' as const, label: '多行粘贴', value: describeTerminalCapability(capabilities.value?.multilinePaste) },
    { key: 'ansiColors' as const, label: 'ANSI 配色', value: describeTerminalCapability(capabilities.value?.ansiColors) },
  ])
  const capabilityWarning = computed(() => {
    if (state.value !== 'connected') return ''
    const unknown = capabilityItems.value.filter((item) => getTerminalCapabilityState(capabilities.value, item.key) === 'unknown')
    if (unknown.length === capabilityItems.value.length) return '服务端未报告终端能力，已使用兼容模式；多行粘贴会先确认并按行发送。'
    if (unknown.length > 0) return `服务端未完整报告终端能力（${unknown.map((item) => item.label).join('、')}），未报告项按不支持处理。`
    return ''
  })
  const pendingPasteDescription = computed(() => {
    if (!pendingPaste.value) return ''
    if (pendingPaste.value.safe) return `将把 ${pendingPaste.value.lineCount} 行内容交给当前 Shell 的 bracketed paste 处理。`
    return '当前会话未同时报告 multiline paste 与 bracketed paste；确认后将逐行发送，每行以 Enter 提交。'
  })
  const pendingPasteActionLabel = computed(() => pendingPaste.value?.safe ? '粘贴到终端' : '按行发送')
  watch(reducedMotion, (reduce) => {
    const owner = current
    if (owner && owns(owner) && owner.terminal) owner.terminal.options.cursorBlink = !reduce
  })

  function clearSessionDetails() {
    pendingPaste.value = null
    shellName.value = ''
    shellEnhancement.value = ''
    shellReason.value = ''
    capabilities.value = null
  }

  function cleanup(owner: Attempt) {
    if (owner.cleaned) return
    owner.cleaned = true
    owner.outputBudget.dispose()
    // 仅保留 close 回调以核实本地关闭；不让退役 socket 持有输出对象。
    if (owner.socket) { owner.socket.onmessage = null; owner.socket.onerror = null }
    owner.containerId = ''
    owner.title = ''
    window.clearTimeout(owner.timers.handshake)
    window.clearTimeout(owner.timers.resize)
    owner.timers = {}
    for (const release of owner.releases.splice(0)) safely(release)
    const terminal = owner.terminal
    owner.terminal = null
    owner.fit = null
    if (terminal) safely(() => terminal.dispose())
  }

  function retire(owner: Attempt, message: string, outcome: 'closed' | 'error' = 'closed') {
    if (current !== owner || !owner.active) return
    // 先撤销输入及所有回调资格，再调用可能同步回调或抛错的第三方方法。
    owner.active = false
    owner.ready = false
    owner.outcome = outcome
    clearSessionDetails()
    statusMessage.value = message
    const socket = owner.socket
    state.value = socket ? 'closing' : outcome
    cleanup(owner)
    if (!socket) {
      page.release(owner.lease)
      current = null
      terminalTitle.value = ''
      return
    }
    if (socket.readyState === WebSocket.OPEN) safely(() => socket.send(JSON.stringify({ type: 'close' })))
    safely(() => socket.close())
  }

  function finishClose(owner: Attempt, socket: WebSocket) {
    if (current !== owner || owner.socket !== socket) return
    if (owner.active) {
      const connecting = !owner.ready
      owner.active = false
      owner.ready = false
      owner.outcome = connecting ? 'error' : 'closed'
      clearSessionDetails()
      statusMessage.value = connecting ? '终端在握手完成前断开。服务端资源回收可能仍在进行。' : '本地连接已关闭，服务端资源回收可能仍在进行。'
      cleanup(owner)
    } else if (attached && owner.outcome === 'closed') {
      statusMessage.value = '本地连接已关闭，服务端资源回收可能仍在进行。'
    }
    page.release(owner.lease, socket)
    owner.socket = null
    socket.onmessage = null
    socket.onerror = null
    socket.onclose = null
    current = null
    if (attached) { terminalTitle.value = ''; state.value = owner.outcome }
  }

  async function connect() {
    if (!pageCurrent() || !canConnect.value || current) return
    const lease = page.reserve(tabId, reason => retire(owner, reason))
    if (!lease) { statusMessage.value = '本页终端连接正在使用或关闭中，请等待本地连接关闭后手动重试。'; return }
    const owner: Attempt = {
      lease, focus: focusIntent(),
      generation: ++attemptGeneration, session: captureSession(), target: target.value, containerId: containerId.value,
      title: target.value === 'host' ? 'root@主机' : containers.value.find(item => item.id === containerId.value)?.name || 'container',
      active: true, ready: false, cleaned: false, socket: null, terminal: null, fit: null,
      timers: {}, releases: [], outcome: 'closed', outputBudget: createTerminalOutputBudget(),
    }
    current = owner
    terminalTitle.value = owner.title
    state.value = 'connecting'
    statusMessage.value = '正在建立终端连接，可取消本次连接。'
    clearSessionDetails()
    await nextTick()
    if (!owns(owner)) return
    try {
      const element = terminalElement.value
      if (!element) throw new Error('terminal element missing')
      const terminal = new Terminal({
        cursorBlink: !reducedMotion.value, cursorStyle: 'bar', cursorWidth: 2, convertEol: true,
        fontFamily: "'JetBrains Mono Variable', 'Cascadia Mono', monospace",
        fontSize: 14, lineHeight: 1.32, scrollback: 5000, theme: getTerminalTheme(),
      })
      owner.terminal = terminal
      const fit = new FitAddon()
      owner.fit = fit
      terminal.loadAddon(fit)
      terminal.open(element)
      if (visible()) fitTerminal(owner)
      terminal.attachCustomKeyEventHandler((event) => {
        if (!page.selected(tabId) || !owns(owner)) return false
        if (isTerminalPasteShortcut(event)) return false
        if (isTerminalLiteralNextShortcut(event)) {
          if (event.type === 'keydown') { event.preventDefault(); sendTerminalInput(owner, '\u0016') }
          return false
        }
        return true
      })
      terminal.writeln('\x1b[38;5;25mNCP 终端\x1b[0m  正在建立会话…')
      const paste = (event: ClipboardEvent) => handlePaste(owner, event)
      element.addEventListener('paste', paste, true)
      owner.releases.push(() => element.removeEventListener('paste', paste, true))
      const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:'
      const params = new URLSearchParams({ target: owner.target, rows: String(Math.max(1, Math.min(200, terminal.rows))), cols: String(Math.max(1, Math.min(400, terminal.cols))) })
      if (owner.target === 'container') params.set('containerId', owner.containerId)
      const socket = new WebSocket(`${protocol}//${location.host}/ws/terminal?${params}`)
      owner.socket = socket
      page.bindSocket(lease, socket)
      socket.binaryType = 'arraybuffer'
      socket.onclose = () => finishClose(owner, socket)
      socket.onerror = () => {
        if (owns(owner, socket)) retire(owner, '终端连接失败，请确认 Agent 终端服务已启用。正在关闭本地连接。', 'error')
      }
      socket.onmessage = (event) => {
        if (!owns(owner, socket)) return
        try {
          if (typeof event.data !== 'string') {
            if (!owner.ready) return
            if (!(event.data instanceof ArrayBuffer)) throw new Error('invalid terminal binary frame')
            if (!event.data.byteLength) return
            const consumed = owner.outputBudget.reserve(event.data.byteLength)
            if (!consumed) {
              retire(owner, '终端输出处理过载，正在关闭本地连接，后续输出未保留。', 'error')
              return
            }
            try { terminal.write(new Uint8Array(event.data), consumed) }
            catch (error) { consumed(); throw error }
            return
          }
          const control = JSON.parse(event.data) as { type?: string; shell?: string; enhancement?: string; reason?: string; code?: string; capabilities?: unknown }
          if (control.type === 'started') {
            if (owner.ready || state.value !== 'connecting') return
            window.clearTimeout(owner.timers.handshake)
            owner.timers.handshake = undefined
            owner.ready = true
            state.value = 'connected'
            shellName.value = control.shell ?? ''
            shellEnhancement.value = control.enhancement ?? ''
            shellReason.value = control.reason ?? ''
            capabilities.value = normalizeTerminalCapabilities(control.capabilities)
            statusMessage.value = capabilityWarning.value
            terminal.clear()
            scheduleResize(owner)
            if (owner.focus()) safely(() => terminal.focus())
          } else if (control.type === 'error') {
            retire(owner, terminalErrorMessages[control.code ?? ''] ?? '终端会话建立失败，请稍后手动重试。', 'error')
          } else if (control.type === 'closed') {
            retire(owner, '服务端已通知会话关闭，正在关闭本地连接；服务端资源回收可能仍在进行。')
          }
        } catch {
          retire(owner, '终端数据处理失败，正在关闭本地连接，后续输出未保留。', 'error')
        }
      }
      owner.timers.handshake = window.setTimeout(() => {
        if (owns(owner, socket) && !owner.ready) retire(owner, '终端握手超时，正在关闭本地连接；服务端资源回收可能仍在进行。', 'error')
      }, 10_000)
      const input = terminal.onData(data => sendTerminalInput(owner, data))
      owner.releases.push(() => input.dispose())
      const dimensions = terminal.onResize(({ rows, cols }) => {
        if (!ready(owner) || capabilities.value?.resize === false) return
        if (!visible() || !Number.isFinite(rows) || !Number.isFinite(cols)) return
        const payload = JSON.stringify({ type: 'resize', rows: Math.max(1, Math.min(200, Math.floor(rows))), cols: Math.max(1, Math.min(400, Math.floor(cols))) })
        if (!checkSocketBudget(owner, payload.length)) return
        try { socket.send(payload) }
        catch { retire(owner, '终端窗口调整发送失败，正在关闭本地连接。', 'error') }
      })
      owner.releases.push(() => dimensions.dispose())
      const resize = () => scheduleResize(owner)
      const observer = new ResizeObserver(resize)
      owner.releases.push(() => observer.disconnect())
      observer.observe(element)
      window.addEventListener('resize', resize)
      owner.releases.push(() => window.removeEventListener('resize', resize))
      const viewport = window.visualViewport
      viewport?.addEventListener('resize', resize)
      owner.releases.push(() => viewport?.removeEventListener('resize', resize))
    } catch {
      if (owns(owner)) retire(owner, '终端初始化失败，已停止本次连接；请稍后手动重试。', 'error')
    }
  }

  function handlePaste(owner: Attempt, event: ClipboardEvent) {
    if (!page.selected(tabId) || !owns(owner)) return
    const text = event.clipboardData?.getData('text/plain') ?? ''
    if (!text) return
    event.preventDefault()
    event.stopImmediatePropagation()
    if (!ready(owner)) { statusMessage.value = '终端尚未就绪，未写入剪贴板内容。'; return }
    // CRLF 归一化最多将字符数减半；先限制原文，再检查包含 bracketed-paste 包装的实际载荷。
    const safe = supportsSafeMultilinePaste(capabilities.value)
    if (text.length > TERMINAL_INPUT_BYTES * 2 ||
      boundedUtf8Bytes(createTerminalPastePayload(text, safe), TERMINAL_INPUT_BYTES) > TERMINAL_INPUT_BYTES) {
      pendingPaste.value = null
      statusMessage.value = '终端输入超过 32 KiB，未发送；请缩短内容后重试。'
      return
    }
    if (isMultilineTerminalPaste(text)) {
      const paste = { owner, text, lineCount: normalizeTerminalPaste(text).split('\n').length, safe }
      pendingPaste.value = paste
      statusMessage.value = pendingPasteDescription.value
      const focus = focusIntent()
      void nextTick(() => { if (focus() && ready(owner) && pendingPaste.value === paste) safely(() => pasteConfirmRef.value?.focus()) })
      return
    }
    sendPastedText(owner, text)
  }

  function sendPastedText(owner: Attempt, text: string, bracketedPaste = supportsSafeMultilinePaste(capabilities.value)) {
    if (sendTerminalInput(owner, createTerminalPastePayload(text, bracketedPaste))) {
      statusMessage.value = bracketedPaste ? '已将内容交给当前 Shell 的 bracketed paste 处理。' : '已将内容写入终端。'
      safely(() => owner.terminal?.focus())
    }
  }
  function confirmPaste() {
    const paste = pendingPaste.value
    pendingPaste.value = null
    if (paste && ready(paste.owner)) sendPastedText(paste.owner, paste.text, paste.safe)
  }
  function cancelPaste() {
    const paste = pendingPaste.value
    if (!paste) return
    pendingPaste.value = null
    if (ready(paste.owner)) { statusMessage.value = '已取消多行粘贴。'; safely(() => paste.owner.terminal?.focus()) }
  }
  function sendTerminalInput(owner: Attempt, data: string | Uint8Array): boolean {
    if (!ready(owner)) return false
    const bytes = typeof data === 'string' ? boundedUtf8Bytes(data, TERMINAL_INPUT_BYTES) : data.byteLength
    if (!bytes) return false
    if (bytes > TERMINAL_INPUT_BYTES) {
      statusMessage.value = '终端输入超过 32 KiB，未发送；请缩短内容后重试。'
      return false
    }
    if (!checkSocketBudget(owner, bytes)) return false
    const payload = typeof data === 'string' ? new TextEncoder().encode(data) : data
    try { owner.socket!.send(payload); return true }
    catch { retire(owner, '终端输入发送失败，正在关闭本地连接。', 'error'); return false }
  }
  function checkSocketBudget(owner: Attempt, bytes: number): boolean {
    if (!ready(owner)) return false
    if (owner.socket!.bufferedAmount + bytes <= TERMINAL_SOCKET_BUFFER_BYTES) return true
    retire(owner, '终端发送积压，当前输入或窗口调整未发送，正在关闭本地连接；已发送的输入无法撤回。', 'error')
    return false
  }
  function describeTerminalCapabilityState(state: TerminalCapabilityState): string {
    return state === 'supported' ? '支持' : state === 'unsupported' ? '不支持' : '未报告'
  }
  function clearTerminal() {
    const owner = current
    if (!owner || !page.selected(tabId) || !owns(owner)) return
    try { owner.terminal?.clear(); owner.terminal?.scrollToBottom(); owner.terminal?.focus(); statusMessage.value = '终端内容已清空。' }
    catch { retire(owner, '终端显示处理失败，正在关闭本地连接。', 'error') }
  }
  function fitTerminal(owner: Attempt) {
    owner.fit?.fit()
    const terminal = owner.terminal
    if (!terminal) return
    const rows = Math.max(1, Math.min(200, terminal.rows))
    const cols = Math.max(1, Math.min(400, terminal.cols))
    if (terminal.rows !== rows || terminal.cols !== cols) terminal.resize(cols, rows)
  }
  function visible() {
    const element = terminalElement.value
    return page.selected(tabId) && Boolean(element && element.clientWidth > 0 && element.clientHeight > 0)
  }
  function scheduleResize(owner: Attempt) {
    if (!visible() || !owns(owner)) return
    window.clearTimeout(owner.timers.resize)
    const selection = page.selectionEpoch
    owner.timers.resize = window.setTimeout(() => {
      if (!visible() || selection !== page.selectionEpoch || !owns(owner)) return
      try { fitTerminal(owner) }
      catch { retire(owner, '终端窗口调整失败，正在关闭本地连接。', 'error') }
    }, 80)
  }
  function close() {
    const owner = current
    if (!owner) return
    const focus = focusIntent()
    retire(owner, '已请求断开，正在关闭本地连接；服务端可能仍在收尾，不能保证远端任务已结束。')
    void nextTick(() => {
      if (focus() && pageCurrent() && attemptGeneration === owner.generation && !owner.active) safely(() => toolbarActions.value?.querySelector<HTMLButtonElement>('button')?.focus())
    })
  }
  function clearPage() {
    shortcutDialogOpen.value = false
    clearSessionDetails()
    terminalTitle.value = ''
    containerId.value = ''
  }
  const unregister = page.register(tabId, {
    clear: clearPage,
    select() {
      focusEpoch++
      pendingPaste.value = null
      shortcutDialogOpen.value = false
      const owner = current
      if (!owner) return
      window.clearTimeout(owner.timers.resize)
      owner.timers.resize = undefined
      const selection = page.selectionEpoch
      void nextTick(() => {
        if (page.selectionEpoch === selection && owns(owner)) scheduleResize(owner)
      })
    },
  })
  function detach() {
    if (!attached) return
    attached = false
    focusEpoch++
    if (current) retire(current, '已离开终端页面。')
    clearPage()
    unregister()
    document.removeEventListener('focusin', cancelFocus)
  }
  onBeforeUnmount(detach)
  return {
    pasteTitleId, pasteDescriptionId,
    target, containerId, state, shellName, shellEnhancement, shellReason, capabilities, statusMessage,
    shortcutDialogOpen, pendingPaste, pasteConfirmRef, terminalElement, terminalTitle, toolbarActions,
    containerOptions, targetLocked, canConnect, canClearTerminal, stateLabel, shellLabel, enhancementLabel,
    capabilityItems, editorCapabilityItems, pendingPasteDescription, pendingPasteActionLabel,
    connect, close, clearTerminal, confirmPaste, cancelPaste, describeTerminalCapabilityState, detach,
  }
}
