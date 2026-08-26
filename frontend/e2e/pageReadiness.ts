import type { Page } from '@playwright/test'

/**
 * Wait for route data to render and for Element Plus enter/leave transitions
 * to finish before taking measurements or running Axe.
 *
 * A heading becoming visible can precede a data-backed tag/card being mounted;
 * sampling during that transition makes color-contrast results nondeterministic.
 */
export async function waitForPageReady(page: Page) {
  await page.waitForTimeout(350)
  await page.waitForFunction(
    () => {
      const hasActiveTransition = [...document.querySelectorAll<HTMLElement>('*')].some((element) =>
        [...element.classList].some(
          (className) => className.endsWith('enter-active') || className.endsWith('leave-active'),
        ),
      )
      return !hasActiveTransition
    },
    undefined,
    { timeout: 5_000 },
  )
}
