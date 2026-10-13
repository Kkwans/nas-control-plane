// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, type App } from 'vue'
import { useResourceAlerts } from './useResourceAlerts'
import { alertsFixture } from '@/domain/monitoring/alerts.fixture'
import { alertAvailability } from '@/domain/monitoring/alerts'
import { beginSession, invalidateSession } from '@/session/sessionLifecycle'
const request = vi.hoisted(() => vi.fn())
vi.mock('@/api/control', () => ({ requestResourceAlerts: request }))
let app: App | undefined
let host: HTMLDivElement
let model: ReturnType<typeof useResourceAlerts>
function mount() { host = document.createElement('div'); app = createApp({ setup() { model = useResourceAlerts(); return () => h('div') } }); app.mount(host) }
function deferred() { let resolve!: (value: ReturnType<typeof alertsFixture>) => void; let reject!: (error: Error) => void; const promise = new Promise<ReturnType<typeof alertsFixture>>((yes, no) => { resolve = yes; reject = no }); return { promise, resolve, reject } }
async function settle() { for (let i = 0; i < 8; i++) await nextTick() }
function visibility(state: 'visible' | 'hidden') { Object.defineProperty(document, 'visibilityState', { configurable: true, value: state }); document.dispatchEvent(new Event('visibilitychange')) }
beforeEach(() => { vi.useFakeTimers(); vi.setSystemTime('2026-10-13T00:00:00Z'); beginSession(); visibility('visible'); request.mockReset(); request.mockResolvedValue(alertsFixture()) })
afterEach(() => { app?.unmount(); app = undefined; vi.useRealTimers() })
describe('资源提示请求生命周期', () => {
  it('polls five seconds after completion, never overlaps slow requests or retry clicks', async () => {
    const slow = deferred(); request.mockReturnValueOnce(slow.promise); mount()
    await vi.advanceTimersByTimeAsync(20000); void model.refresh(); expect(request).toHaveBeenCalledTimes(1)
    slow.resolve(alertsFixture()); await settle()
    await vi.advanceTimersByTimeAsync(4999); expect(request).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1); expect(request).toHaveBeenCalledTimes(2)
  })
  it('marks unchanged samples stale locally while a poll hangs', async () => {
    mount(); await settle(); request.mockReturnValueOnce(deferred().promise)
    await vi.advanceTimersByTimeAsync(16000)
    expect(alertAvailability(model.snapshot.value!.rules.cpu, model.now.value)).toBe('stale')
    expect(request).toHaveBeenCalledTimes(2)
  })
  it('never revives stale data or freezes age when the local wall clock rolls back', async () => {
    mount(); await settle(); request.mockReturnValueOnce(deferred().promise)
    await vi.advanceTimersByTimeAsync(16000)
    const staleAt = model.now.value
    expect(alertAvailability(model.snapshot.value!.rules.cpu, staleAt)).toBe('stale')
    vi.setSystemTime('2026-10-12T23:59:00Z')
    await vi.advanceTimersByTimeAsync(1000)
    expect(model.now.value).toBeGreaterThanOrEqual(staleAt + 1000)
    expect(alertAvailability(model.snapshot.value!.rules.cpu, model.now.value)).toBe('stale')
  })
  it('keeps prior data on refresh error then independently retries', async () => {
    mount(); await settle(); request.mockRejectedValueOnce(new Error('offline'))
    await vi.advanceTimersByTimeAsync(5000); expect(model.snapshot.value).not.toBeNull(); expect(model.errorCode.value).toBe('MONITORING_ALERTS_READ_FAILED')
    await vi.advanceTimersByTimeAsync(5000); expect(model.errorCode.value).toBe('')
  })
  it('hides, aborts, suppresses late result, and resumes only after old request settles', async () => {
    const slow = deferred(); request.mockReturnValueOnce(slow.promise); mount()
    const signal = request.mock.calls[0]![0] as AbortSignal
    visibility('hidden'); expect(signal.aborted).toBe(true)
    await vi.advanceTimersByTimeAsync(20000); visibility('visible'); expect(request).toHaveBeenCalledTimes(1)
    const old = alertsFixture(); old.rules.cpu.value = 99; slow.resolve(old); await settle()
    expect(request).toHaveBeenCalledTimes(2); expect(model.snapshot.value?.rules.cpu.value).toBe(0)
  })
  it('refreshes immediately on visible return and updates the local age', async () => {
    mount(); await settle(); visibility('hidden'); await vi.advanceTimersByTimeAsync(20000)
    expect(request).toHaveBeenCalledTimes(1); visibility('visible'); await settle()
    expect(request).toHaveBeenCalledTimes(2); expect(alertAvailability(model.snapshot.value!.rules.cpu, model.now.value)).toBe('stale')
  })
  it.each(['logout', 'new-session', 'unmount'])('discards delayed response and tears down timers on %s', async (action) => {
    const slow = deferred(); request.mockReturnValueOnce(slow.promise); mount()
    const signal = request.mock.calls[0]![0] as AbortSignal
    if (action === 'unmount') { app!.unmount(); app = undefined } else if (action === 'logout') invalidateSession(); else beginSession()
    expect(signal.aborted).toBe(true); slow.resolve(alertsFixture()); await settle(); await vi.advanceTimersByTimeAsync(30000)
    expect(model.snapshot.value).toBeNull(); expect(request).toHaveBeenCalledTimes(1); expect(vi.getTimerCount()).toBe(0)
  })
})
