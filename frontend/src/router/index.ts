import { createRouter, createWebHistory } from 'vue-router'

/**
 * 路由结构对应旧 Next.js 项目的 App Router 目录（见 docs/UI-还原文档.md §1）：
 *   app/page.tsx                    → /
 *   app/workspace/page.tsx          → /workspace
 *   app/classroom/[id]/page.tsx     → /classroom/:id
 *   app/generation-preview/page.tsx → /generation-preview
 *   app/workbench/new/page.tsx      → /workbench/new
 *
 * 页面组件一律懒加载，避免首屏把 4 个大页面全部打进主 chunk。
 */
const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes: [
    {
      path: '/',
      name: 'home',
      component: () => import('@/views/HomeView.vue'),
      meta: { title: 'Narra' },
    },
    {
      path: '/workspace',
      name: 'workspace',
      component: () => import('@/views/WorkspaceView.vue'),
      meta: { title: 'Workspace' },
    },
    {
      path: '/classroom/:id',
      name: 'classroom',
      component: () => import('@/views/ClassroomView.vue'),
      props: true,
      meta: { title: 'Classroom' },
    },
    {
      path: '/generation-preview',
      name: 'generation-preview',
      component: () => import('@/views/GenerationPreviewView.vue'),
      meta: { title: 'Generation Preview' },
    },
    {
      path: '/:pathMatch(.*)*',
      name: 'not-found',
      component: () => import('@/views/NotFoundView.vue'),
      meta: { title: 'Not Found' },
    },
  ],
  scrollBehavior(_to, _from, savedPosition) {
    return savedPosition ?? { top: 0 }
  },
})

router.afterEach((to) => {
  const title = to.meta.title as string | undefined
  if (title) document.title = title === 'Narra' ? 'Narra' : `${title} · Narra`
})

export default router
