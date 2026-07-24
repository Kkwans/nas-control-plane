import { createRouter, createWebHistory } from 'vue-router'

const router = createRouter({
  history: createWebHistory(),
  scrollBehavior: () => ({ top: 0 }),
  routes: [
    {
      path: '/',
      name: 'overview',
      component: () => import('@/views/OverviewView.vue'),
      meta: { title: '总览' },
    },
    {
      path: '/sites',
      name: 'sites',
      component: () => import('@/views/ServicesView.vue'),
      meta: { title: '站点中心' },
    },
    {
      path: '/services',
      redirect: '/sites',
    },
    {
      path: '/docker',
      name: 'docker',
      component: () => import('@/views/DockerView.vue'),
      meta: { title: 'Docker 管理' },
    },
    {
      path: '/system',
      name: 'system',
      component: () => import('@/views/InfrastructureView.vue'),
      meta: { title: '系统信息' },
    },
    {
      path: '/databases',
      name: 'databases',
      component: () => import('@/views/DatabaseView.vue'),
      meta: { title: '数据库' },
    },
    {
      path: '/databases/:sourceId/tables/:table',
      name: 'database-table',
      component: () => import('@/views/DatabaseTableView.vue'),
      meta: { title: '数据表' },
    },
    {
      path: '/databases/:sourceId',
      name: 'database-detail',
      component: () => import('@/views/DatabaseDetailView.vue'),
      meta: { title: '数据库详情' },
    },
    {
      path: '/settings',
      name: 'settings',
      component: () => import('@/views/SettingsView.vue'),
      meta: { title: '系统设置' },
    },
    {
      path: '/:pathMatch(.*)*',
      redirect: '/',
    },
  ],
})

router.afterEach((to) => {
  document.title = `${String(to.meta.title ?? 'NCP')} · NAS 管理面板`
  requestAnimationFrame(() => document.getElementById('app-main')?.focus({ preventScroll: true }))
})

export default router
