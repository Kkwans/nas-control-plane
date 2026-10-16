// @vitest-environment jsdom

import { createApp, h, nextTick, ref } from 'vue'
import { describe, expect, it } from 'vitest'

import GroupedDirectory, { type GroupedDirectoryGroup } from './GroupedDirectory.vue'

describe('GroupedDirectory', () => {
  it('renders shared group headings, counts, actions and rows', () => {
    const host = document.createElement('div')
    const groups: GroupedDirectoryGroup[] = [{ key: 'media', title: '影音服务', count: 2 }]
    const app = createApp({
      render: () => h(GroupedDirectory, { groups, label: '站点目录' }, {
        header: () => h('div', { class: 'test-column-header' }, '站点 状态 入口'),
        actions: ({ group }: { group: GroupedDirectoryGroup }) => h('button', { type: 'button' }, `管理 ${group.title}`),
        items: ({ group }: { group: GroupedDirectoryGroup }) => h('a', { href: `/groups/${group.key}` }, '影视森林'),
      }),
    })

    app.mount(host)

    expect(host.querySelector('[role="group"][aria-label="站点目录"]')).not.toBeNull()
    expect(host.querySelector('.test-column-header')?.textContent).toBe('站点 状态 入口')
    expect(host.querySelector('h3')?.textContent).toBe('影音服务')
    expect(host.querySelector('.grouped-directory__title > span')?.textContent).toBe('2 项')
    expect(host.querySelector('button')?.textContent).toBe('管理 影音服务')
    expect(host.querySelector('a')?.getAttribute('href')).toBe('/groups/media')

    app.unmount()
  })

  it('keeps count text accessible without naming generic elements as groups update', async () => {
    const host = document.createElement('div')
    const groups = ref<GroupedDirectoryGroup[]>([
      { key: 'media', title: '影音服务', count: 2 },
      { key: 'tools', title: '工具', count: 0 },
    ])
    const app = createApp({
      render: () => h(GroupedDirectory, { groups: groups.value, label: '站点目录' }),
    })

    app.mount(host)

    try {
      const expectAccessibleCounts = (counts: number[]) => {
        const badges = [...host.querySelectorAll('.grouped-directory__title > span')]
        expect(badges).toHaveLength(counts.length)
        badges.forEach((badge, index) => {
          expect(badge.firstChild?.textContent).toBe(String(counts[index]))
          expect(badge.textContent).toBe(`${counts[index]} 项`)
          expect(badge.querySelector('.sr-only')?.textContent).toBe(' 项')
          expect(badge.querySelector('[aria-hidden]')).toBeNull()
          expect(badge.hasAttribute('aria-hidden')).toBe(false)
          expect(badge.hasAttribute('aria-label')).toBe(false)
          expect(badge.hasAttribute('role')).toBe(false)
        })
        expect(host.querySelectorAll('[role]')).toHaveLength(1)
        expect(host.querySelectorAll('[aria-label]')).toHaveLength(1)
        expect(host.querySelector('[role="group"][aria-label="站点目录"]')).not.toBeNull()
        expect(host.querySelector('div:not([role])[aria-label], span[aria-label]')).toBeNull()
      }

      expectAccessibleCounts([2, 0])
      groups.value = [{ key: 'media', title: '影音服务', count: 1 }]
      await nextTick()
      expectAccessibleCounts([1])
    } finally {
      app.unmount()
    }
  })
})
