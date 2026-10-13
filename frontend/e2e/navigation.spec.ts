import { expect, test } from '@playwright/test'
import { installMockApi } from './mockApi'

for (const width of [390, 768]) {
  test(`${width}px 移动导航真实键盘隔离、循环、退出和路由焦点`, async ({ page }) => {
    await installMockApi(page)
    await page.setViewportSize({ width, height: 844 })
    await page.goto('/')
    await expect(page.getByRole('heading', { name: '系统总览', exact: true })).toBeVisible()
    const sidebar = page.locator('.app-sidebar')
    const menu = page.getByRole('button', { name: '打开主菜单', exact: true })
    await expect(sidebar).toHaveAttribute('inert', '')
    // 真正从页面外部 Tab，不用 locator.focus() 代替关闭态键盘可达性验证。
    await page.evaluate(() => { (document.activeElement as HTMLElement)?.blur() })
    for (let index = 0; index < 20; index++) {
      await page.keyboard.press('Tab')
      expect(await sidebar.evaluate((node) => node.contains(document.activeElement))).toBe(false)
    }
    await menu.focus(); await page.keyboard.press('Enter')
    const dialog = page.getByRole('dialog', { name: '主导航' })
    const close = dialog.getByRole('button', { name: '关闭主菜单', exact: true })
    const brand = dialog.getByRole('link', { name: 'NAS 管理面板首页' })
    const logout = dialog.getByRole('button', { name: '退出登录', exact: true })
    await expect(close).toBeFocused()
    await expect(page.locator('.app-stage')).toHaveAttribute('inert', '')
    await page.keyboard.press('Shift+Tab'); await expect(brand).toBeFocused()
    await page.keyboard.press('Shift+Tab'); await expect(logout).toBeFocused()
    await page.keyboard.press('Tab'); await expect(brand).toBeFocused()
    for (let index = 0; index < 25; index++) {
      await page.keyboard.press('Tab')
      expect(await sidebar.evaluate((node) => node.contains(document.activeElement))).toBe(true)
    }
    await page.keyboard.press('Escape'); await expect(menu).toBeFocused()
    await expect(page.locator('.app-stage')).not.toHaveAttribute('inert')
    await page.keyboard.press('Enter')
    await page.locator('.navigation-scrim').click({ position: { x: width - 10, y: 100 } })
    await expect(menu).toBeFocused()
    await page.keyboard.press('Enter')
    await dialog.getByRole('link', { name: '设置', exact: true }).focus()
    await page.keyboard.press('Enter')
    await expect(page).toHaveURL(/\/settings$/)
    await expect(page.locator('#app-main')).toBeFocused()
    await expect(sidebar).toHaveAttribute('inert', '')
    // 重选当前页也必须关闭，而非等待不会触发的路由变更。
    await menu.focus(); await page.keyboard.press('Enter')
    await dialog.getByRole('link', { name: '设置', exact: true }).focus()
    await page.keyboard.press('Enter')
    await expect(page.locator('#app-main')).toBeFocused()
    await expect(sidebar).toHaveAttribute('inert', '')
  })
}

test('窄高菜单可滚动，跨断点不遗留 inert、焦点或滚动锁', async ({ page }) => {
  await installMockApi(page)
  await page.setViewportSize({ width: 390, height: 420 })
  await page.goto('/')
  const menu = page.getByRole('button', { name: '打开主菜单', exact: true })
  await menu.click()
  const sidebar = page.locator('.app-sidebar')
  expect(await sidebar.evaluate((node) => node.scrollHeight > node.clientHeight)).toBe(true)
  await sidebar.getByRole('button', { name: '退出登录', exact: true }).focus()
  await expect(sidebar.getByRole('button', { name: '退出登录', exact: true })).toBeInViewport()
  await page.keyboard.press('Escape'); await page.keyboard.press('Enter')
  await page.setViewportSize({ width: 769, height: 900 })
  await expect(sidebar).not.toHaveAttribute('inert')
  await expect(sidebar).not.toHaveAttribute('aria-modal')
  await expect(page.locator('.app-stage')).not.toHaveAttribute('inert')
  await expect(page.locator('body')).not.toHaveClass(/mobile-navigation-open/)
  await expect(page.locator('#app-main')).toBeFocused()
  await sidebar.getByRole('link', { name: '数据库', exact: true }).focus()
  await page.setViewportSize({ width: 768, height: 900 })
  await expect(menu).toBeFocused()
  await expect(sidebar).toHaveAttribute('inert', '')
})

for (const path of ['/databases/sqlite-main', '/databases/sqlite-main/tables/settings']) {
  test(`${path} 保持数据库模块活跃语义`, async ({ page }) => {
    await installMockApi(page)
    await page.setViewportSize({ width: 1440, height: 900 })
    await page.goto(path)
    const link = page.locator('.navigation').getByRole('link', { name: '数据库', exact: true })
    await expect(link).toHaveAttribute('aria-current', 'location')
    await expect(link).toHaveClass(/navigation__item--active/)
  })
}
