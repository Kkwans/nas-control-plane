import { execFileSync } from 'node:child_process'
import { mkdir, writeFile } from 'node:fs/promises'
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Locator, type Page, type TestInfo } from '@playwright/test'
import { installMockApi } from './mockApi'
import { waitForPageReady } from './pageReadiness'

const createdAt = '2026-08-19T03:25:00Z'
const lastLoginAt = '2026-10-12T14:37:00Z'
const accounts = [
  { id: 42, username: 'abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789ab', lastLoginAt: null },
  { id: 43, username: '账号管理测试'.repeat(10) + '中文长名', lastLoginAt },
].map((user) => ({ ...user, role: 'root', disabled: false, createdAt, updatedAt: createdAt }))
const policy = { minLength: 8, requireUppercase: false, requireLowercase: false, requireDigit: false, requireSpecial: false }

async function installAccounts(page: Page) {
  // 共用 fixture 接管登录、偏好与实时事件；不访问真实账号或凭据。
  await installMockApi(page, { clock: false, manualRealtime: true })
  const requests: Array<{ method: string; path: string }> = []
  await page.route('**/api/**', async (route) => {
    const method = route.request().method()
    const path = new URL(route.request().url()).pathname
    requests.push({ method, path })
    if (method !== 'GET') return route.fulfill({ status: 503, json: { code: 'FIXTURE_WRITE_BLOCKED' } })
    if (path === '/api/v1/users') return route.fulfill({ json: accounts })
    if (path === '/api/v1/users/password-policy') return route.fulfill({ json: policy })
    if (path.startsWith('/api/v1/')) return route.fallback()
    return route.fulfill({ status: 404, json: { code: 'FIXTURE_ROUTE_MISSING' } })
  })
  return requests
}

// 记录真实文字行框、各轴裁切祖先和滚动尺寸，防止 hidden/ellipsis 假通过。
async function measure(locator: Locator) {
  return locator.evaluate((element) => {
    const bounds = element.getBoundingClientRect()
    const style = getComputedStyle(element)
    const lines: DOMRect[] = []
    const walker = document.createTreeWalker(element, NodeFilter.SHOW_TEXT)
    for (let node = walker.nextNode(); node; node = walker.nextNode()) {
      if (!node.textContent?.trim()) continue
      const range = document.createRange()
      range.selectNodeContents(node)
      lines.push(...[...range.getClientRects()].filter((line) => line.width > 0 && line.height > 0))
    }
    const clips: Array<{ x: boolean; y: boolean; bounds: { left: number; right: number; top: number; bottom: number } }> = []
    let visible = bounds.width > 0 && bounds.height > 0
    for (let ancestor: Element | null = element; ancestor; ancestor = ancestor.parentElement) {
      const computed = getComputedStyle(ancestor)
      visible &&= computed.display !== 'none' && computed.visibility !== 'hidden' && Number(computed.opacity) > 0
      if (ancestor === document.documentElement || ancestor === document.body) continue
      const x = /hidden|clip|auto|scroll/.test(computed.overflowX)
      const y = /hidden|clip|auto|scroll/.test(computed.overflowY)
      if (x || y) clips.push({ x, y, bounds: ancestor.getBoundingClientRect().toJSON() })
    }
    const hit = document.elementFromPoint(bounds.left + bounds.width / 2, bounds.top + bounds.height / 2)
    return {
      text: element.textContent?.trim(), bounds: bounds.toJSON(), lines: lines.map((line) => line.toJSON()), clips,
      scrollWidth: element.scrollWidth, clientWidth: element.clientWidth,
      scrollHeight: element.scrollHeight, clientHeight: element.clientHeight,
      display: style.display, overflowX: style.overflowX, overflowY: style.overflowY, textOverflow: style.textOverflow,
      lineClamp: style.webkitLineClamp, whiteSpace: style.whiteSpace,
      visible, scrollX, scrollY, disabled: element instanceof HTMLButtonElement ? element.disabled : null,
      boxContained: clips.every((clip) => (!clip.x || (bounds.left >= clip.bounds.left - 1 && bounds.right <= clip.bounds.right + 1)) &&
        (!clip.y || (bounds.top >= clip.bounds.top - 1 && bounds.bottom <= clip.bounds.bottom + 1))),
      inViewport: bounds.left >= -1 && bounds.top >= -1 && bounds.right <= innerWidth + 1 && bounds.bottom <= innerHeight + 1,
      centerHit: hit === element || (hit !== null && element.contains(hit)),
      contained: lines.length > 0 && lines.every((line) =>
        line.left >= bounds.left - 1 && line.right <= bounds.right + 1 && line.top >= bounds.top - 1 && line.bottom <= bounds.bottom + 1 &&
        clips.every((clip) => (!clip.x || (line.left >= clip.bounds.left - 1 && line.right <= clip.bounds.right + 1)) &&
          (!clip.y || (line.top >= clip.bounds.top - 1 && line.bottom <= clip.bounds.bottom + 1)))),
    }
  })
}

async function screenshot(page: Page, info: TestInfo, name: string, fullPage: boolean) {
  const path = info.outputPath('ui-captures', `${name}.png`)
  await page.screenshot({ path, fullPage, animations: 'disabled' })
  await info.attach(name, { path, contentType: 'image/png' })
}

for (const width of [320, 390, 768, 1440]) {
  for (const scale of [1, 2]) {
    test(`账号日期与 64 字符用户名 ${width}px / root-font ${scale * 100}% 完整可读`, async ({ page }, info) => {
      await page.setViewportSize({ width, height: 1000 })
      const requests = await installAccounts(page)
      await page.goto('/users', { waitUntil: 'domcontentloaded' })
      await page.waitForFunction(() => document.querySelectorAll('.user-row').length === 2 &&
        [...document.querySelectorAll<HTMLButtonElement>('.users-panel__toolbar button')].some((button) => !button.disabled))
      await page.evaluate(() => document.fonts.ready)
      const base = await page.locator('html').evaluate((element) => Number.parseFloat(getComputedStyle(element).fontSize))
      // 这是根字号放大，不是浏览器 zoom；CSS 视口宽度保持不变。
      await page.evaluate((size) => document.documentElement.style.setProperty('font-size', `${size}px`, 'important'), base * scale)
      await waitForPageReady(page)
      const formatted = await page.evaluate(({ createdAt, lastLoginAt }) => {
        const formatter = new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'short' })
        return { created: formatter.format(new Date(createdAt)), login: formatter.format(new Date(lastLoginAt)), timeZone: formatter.resolvedOptions().timeZone }
      }, { createdAt, lastLoginAt })
      const name = `user-mobile-layout-${width}-${scale * 100}`
      await mkdir(info.outputPath('ui-captures'), { recursive: true })
      await page.evaluate(() => window.scrollTo({ top: 0, left: 0, behavior: 'instant' }))
      await screenshot(page, info, `${name}-fullpage`, true)
      await screenshot(page, info, `${name}-header-viewport`, false)
      const workspaceHeader = []
      for (const selector of ['.workspace-header__copy h1', '.workspace-header__copy p', '.workspace-stat dt', '.workspace-stat dd']) {
        for (const item of await page.locator(`.users-page ${selector}`).all()) workspaceHeader.push({ selector, ...await measure(item) })
      }
      const pageControls = []
      for (const button of await page.locator('.password-policy > header button, .users-panel__toolbar button').all()) {
        await button.evaluate((element) => element.scrollIntoView({ block: 'center', inline: 'nearest', behavior: 'instant' }))
        pageControls.push(await measure(button))
        await screenshot(page, info, `${name}-page-control-${pageControls.length}-viewport`, false)
      }
      const rows = page.locator('.user-row')
      const observations = []
      for (let index = 0; index < accounts.length; index += 1) {
        const row = rows.nth(index)
        const text = []
        for (const selector of ['.user-identity strong', '.user-identity small', '.account-status', '.user-date__value', ...(width <= 1050 ? ['.user-date__label'] : [])]) {
          for (const item of await row.locator(selector).all()) text.push({ selector, ...await measure(item) })
        }
        const controls = []
        for (const button of await row.locator('.user-actions button').all()) {
          // 测实际按钮，不合成点击、不隐藏遮挡；每个控件滚动后测中心命中。
          await button.evaluate((element) => element.scrollIntoView({ block: 'center', inline: 'nearest', behavior: 'instant' }))
          controls.push(await measure(button))
        }
        await screenshot(page, info, `${name}-account-${index + 1}-controls-viewport`, false)
        observations.push({ text, controls, dates: await row.locator('.user-date__value').allTextContents(), labels: await row.locator('.user-date__label').allTextContents() })
      }
      const headerLabels = []
      if (width > 1050) {
        for (const item of await page.locator('.user-table__head span').all()) headerLabels.push(await measure(item))
      }
      const results = await new AxeBuilder({ page }).include('.user-row').analyze()
      const state = {
        sourceSha: execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).trim(),
        fixtureOnly: true, fixtureSource: 'frontend/e2e/user-mobile-layout.spec.ts + frontend/e2e/mockApi.ts',
        magnification: 'root-font-size (not browser zoom)', width, scale, base, formatted, observations, headerLabels, workspaceHeader, pageControls,
        layout: await page.evaluate(() => ({
          rootFontSize: getComputedStyle(document.documentElement).fontSize, innerWidth,
          documentScrollWidth: document.documentElement.scrollWidth,
          workspaceScrollWidth: document.querySelector('.workspace-header')!.scrollWidth,
          workspaceClientWidth: document.querySelector('.workspace-header')!.clientWidth,
          panelScrollWidth: document.querySelector('.users-panel')!.scrollWidth,
          panelClientWidth: document.querySelector('.users-panel')!.clientWidth,
          tableScrollWidth: document.querySelector('.user-table')!.scrollWidth,
          tableClientWidth: document.querySelector('.user-table')!.clientWidth,
        })),
        blockingAxeViolations: results.violations.filter(({ impact }) => impact === 'serious' || impact === 'critical'), requests,
      }
      // 截图与全部测量落盘后才断言，失败时仍保留未改动的原状证据。
      const json = JSON.stringify(state, null, 2)
      await writeFile(info.outputPath('ui-captures', `${name}-capture-state.json`), json)
      await info.attach(`${name}-capture-state`, { body: json, contentType: 'application/json' })
      expect(state.layout.rootFontSize).toBe(`${base * scale}px`)
      expect(state.layout.innerWidth).toBe(width)
      expect(state.layout.documentScrollWidth).toBeLessThanOrEqual(width + 1)
      expect(state.layout.workspaceScrollWidth).toBeLessThanOrEqual(state.layout.workspaceClientWidth + 1)
      expect(state.layout.panelScrollWidth).toBeLessThanOrEqual(state.layout.panelClientWidth + 1)
      expect(state.layout.tableScrollWidth).toBeLessThanOrEqual(state.layout.tableClientWidth + 1)
      expect(workspaceHeader.filter(({ selector }) => selector === '.workspace-header__copy h1').map(({ text }) => text)).toEqual(['用户管理'])
      expect(workspaceHeader.filter(({ selector }) => selector === '.workspace-header__copy p').map(({ text }) => text)).toEqual(['管理可登录 NCP 的等权 Root 账号与会话安全'])
      expect(workspaceHeader.filter(({ selector }) => selector === '.workspace-stat dt').map(({ text }) => text)).toEqual(['全部账号', '可用账号', '权限模型'])
      expect(workspaceHeader.filter(({ selector }) => selector === '.workspace-stat dd').map(({ text }) => text)).toEqual(['2', '2', '等权 Root'])
      for (const item of workspaceHeader) {
        expect(item.visible, JSON.stringify(item)).toBe(true)
        expect(item.contained, JSON.stringify(item)).toBe(true)
        expect(item.textOverflow, JSON.stringify(item)).not.toBe('ellipsis')
        expect(item.lineClamp, JSON.stringify(item)).toBe('none')
        expect(item.scrollWidth, JSON.stringify(item)).toBeLessThanOrEqual(item.clientWidth + 1)
        expect(item.scrollHeight, JSON.stringify(item)).toBeLessThanOrEqual(item.clientHeight + 1)
      }
      expect(pageControls).toHaveLength(2)
      for (const control of pageControls) {
        expect(control.visible, JSON.stringify(control)).toBe(true)
        expect(control.contained, JSON.stringify(control)).toBe(true)
        expect(control.disabled, JSON.stringify(control)).toBe(false)
        expect(control.boxContained, JSON.stringify(control)).toBe(true)
        expect(control.inViewport, JSON.stringify(control)).toBe(true)
        expect(control.centerHit, JSON.stringify(control)).toBe(true)
        expect(control.scrollWidth, JSON.stringify(control)).toBeLessThanOrEqual(control.clientWidth + 1)
        expect(control.scrollHeight, JSON.stringify(control)).toBeLessThanOrEqual(control.clientHeight + 1)
      }
      for (const [index, observed] of observations.entries()) {
        expect(accounts[index]!.username).toHaveLength(64)
        expect(observed.text.find(({ selector }) => selector === '.user-identity strong')?.text).toBe(accounts[index]!.username)
        expect(observed.dates.map((value) => value.trim())).toEqual([index === 0 ? '从未登录' : formatted.login, formatted.created])
        expect(observed.labels.map((value) => value.trim())).toEqual(['最近登录', '创建时间'])
        expect(observed.text.filter(({ selector }) => selector === '.user-date__value')).toHaveLength(2)
        expect(observed.controls).toHaveLength(2)
        for (const item of [...observed.text, ...observed.controls]) {
          expect(item.visible, JSON.stringify(item)).toBe(true)
          // 图标按钮没有文字行框，其可见性和命中单独验证。
          if (item.text) expect(item.contained, JSON.stringify(item)).toBe(true)
          if (item.clientWidth > 0) expect(item.scrollWidth, JSON.stringify(item)).toBeLessThanOrEqual(item.clientWidth + 1)
          if (item.clientHeight > 0) expect(item.scrollHeight, JSON.stringify(item)).toBeLessThanOrEqual(item.clientHeight + 1)
        }
        for (const control of observed.controls) {
          expect(control.disabled, JSON.stringify(control)).toBe(false)
          expect(control.boxContained, JSON.stringify(control)).toBe(true)
          expect(control.inViewport, JSON.stringify(control)).toBe(true)
          expect(control.centerHit, JSON.stringify(control)).toBe(true)
        }
      }
      if (width > 1050) {
        expect(headerLabels.map(({ text }) => text)).toContain('最近登录')
        expect(headerLabels.map(({ text }) => text)).toContain('创建时间')
        for (const label of headerLabels) {
          expect(label.visible, JSON.stringify(label)).toBe(true)
          expect(label.contained, JSON.stringify(label)).toBe(true)
        }
      }
      expect(state.blockingAxeViolations, JSON.stringify(state.blockingAxeViolations, null, 2)).toEqual([])
      expect(requests.filter(({ method }) => method !== 'GET')).toEqual([])
    })
  }
}
