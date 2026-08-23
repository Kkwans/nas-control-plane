import { describe, expect, it } from 'vitest'

import { DEFAULT_NAVIGATION_ORDER, navigationLabels, primaryNavigation } from './navigation'

describe('navigation metadata', () => {
  it('keeps the default order and labels sourced from the same items', () => {
    expect(DEFAULT_NAVIGATION_ORDER).toEqual(primaryNavigation.map((item) => item.id))
    expect(Object.keys(navigationLabels)).toEqual(DEFAULT_NAVIGATION_ORDER)
    expect(new Set(DEFAULT_NAVIGATION_ORDER).size).toBe(DEFAULT_NAVIGATION_ORDER.length)
  })

  it('provides a route for every configurable navigation item', () => {
    expect(primaryNavigation.every((item) => item.to.startsWith('/'))).toBe(true)
    expect(primaryNavigation.every((item) => item.label.trim().length > 0)).toBe(true)
  })
})
