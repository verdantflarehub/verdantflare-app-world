import { createApp, h } from 'vue'
import { createMemoryHistory, createRouter, RouterView } from 'vue-router'
import Browser from './pages/WorldBrowser.vue'
import { worldDomains } from './platform/world-catalog'
import CollectionsPage from './pages/CollectionsPage.vue'
import LearnPage from './pages/LearnPage.vue'
import PublicationPage from './pages/PublicationPage.vue'
import { connectHost, disconnectHost } from './platform/host'
import { canonicalRoute } from './platform/route-links'
import './base.css'

const root = document.querySelector<HTMLElement>('#app')!

async function mount() {
  const { appSDK: sdk, dashboardHost: host } = connectHost('world')
  const router = createRouter({ history: createMemoryHistory(), routes: [
    { path: '/', component: Browser },
    ...worldDomains.map(d=>({path:'/'+d.key,component:Browser})),
    { path: '/publish', component: PublicationPage },
    { path: '/collections', component: CollectionsPage },
    { path: '/collections/:draftId', component: CollectionsPage },
    { path: '/learn', component: LearnPage },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ] })
  const app = createApp({ render: () => h(RouterView) }).use(router)
  document.body.dataset.theme = sdk.theme.get()
  await router.replace(canonicalRoute('world', sdk.navigation.get().path))
  await router.isReady()
  app.mount(root)
  let disposed = false, revision = sdk.navigation.get().revision
  const hostPaths = new Set<string>()
  const stopTheme = sdk.theme.subscribe(theme => { if (!disposed) document.body.dataset.theme = theme })
  const stopRoute = sdk.navigation.subscribe(route => {
    if (disposed || route.revision <= revision) return
    revision = route.revision
    const path = canonicalRoute('world', route.path)
    if (path === router.currentRoute.value.fullPath) return
    hostPaths.add(path)
    void router.replace(path).finally(() => hostPaths.delete(path))
  })
  const stopAfter = router.afterEach((to, _from, failure) => {
    if (failure || disposed || hostPaths.has(to.fullPath) || to.fullPath === sdk.navigation.get().path) return
    void sdk.navigation.request({ target: 'app', path: to.fullPath }).catch(() => {
      if (disposed) return
      const path = sdk.navigation.get().path
      hostPaths.add(path)
      void router.replace(path).finally(() => hostPaths.delete(path))
    })
  })
  const resize = new ResizeObserver(() => { if (!disposed) host.resize(Math.ceil(root.getBoundingClientRect().height)) })
  resize.observe(root)
  const stopLifecycle = sdk.lifecycle.subscribe(event => {
    if (event !== 'dispose' || disposed) return
    disposed = true; resize.disconnect(); stopTheme(); stopRoute(); stopAfter(); stopLifecycle(); app.unmount(); disconnectHost()
  })
  host.ready()
  host.setDirty(false)
  host.resize(Math.ceil(root.getBoundingClientRect().height))
}
void mount().catch(error => { disconnectHost(); root.textContent = error instanceof Error ? error.message : '应用暂时无法打开。' })
