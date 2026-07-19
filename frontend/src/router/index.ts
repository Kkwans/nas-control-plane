import { createRouter, createWebHistory } from 'vue-router'

const router = createRouter({
  history: createWebHistory(),
  scrollBehavior: () => ({ top: 0 }),
  routes: [
    {
      path: '/',
      name: 'overview',
      component: () => import('@/views/OverviewView.vue'),
      meta: { title: '控制室总览' },
    },
    {
      path: '/services',
      name: 'services',
      component: () => import('@/views/ServicesView.vue'),
      meta: { title: '服务中心' },
    },
    {
      path: '/infrastructure',
      name: 'infrastructure',
      component: () => import('@/views/InfrastructureView.vue'),
      meta: { title: '能力地图' },
    },
    {
      path: '/:pathMatch(.*)*',
      redirect: '/',
    },
  ],
})

router.afterEach((to) => {
  document.title = `${String(to.meta.title ?? 'NCP')} · NAS Control Plane`
  requestAnimationFrame(() => document.getElementById('app-main')?.focus({ preventScroll: true }))
})

export default router
