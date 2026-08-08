// @vitest-environment jsdom

import { createApp, h } from 'vue'
import { describe, expect, it } from 'vitest'

import GroupedDirectory, { type GroupedDirectoryGroup } from './GroupedDirectory.vue'

describe('GroupedDirectory', () => {
  it('renders shared group headings, counts, actions and rows', () => {
    const host = document.createElement('div')
    const groups: GroupedDirectoryGroup[] = [{ key: 'media', title: '影音服务', count: 2 }]
    const app = createApp({
      render: () => h(GroupedDirectory, { groups, label: '站点目录' }, {
        actions: ({ group }: { group: GroupedDirectoryGroup }) => h('button', { type: 'button' }, `管理 ${group.title}`),
        items: ({ group }: { group: GroupedDirectoryGroup }) => h('a', { href: `/groups/${group.key}` }, '影视森林'),
      }),
    })

    app.mount(host)

    expect(host.querySelector('[aria-label="站点目录"]')).not.toBeNull()
    expect(host.querySelector('h3')?.textContent).toBe('影音服务')
    expect(host.querySelector('[aria-label="2 项"]')?.textContent).toBe('2')
    expect(host.querySelector('button')?.textContent).toBe('管理 影音服务')
    expect(host.querySelector('a')?.getAttribute('href')).toBe('/groups/media')

    app.unmount()
  })
})
