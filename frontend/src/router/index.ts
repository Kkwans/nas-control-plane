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
      path: '/services',
      name: 'services',
      component: () => import('@/views/ServicesView.vue'),
      meta: { title: '服务入口' },
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
