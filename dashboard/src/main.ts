import { createApp, h } from 'vue'
import { createMemoryHistory, createRouter, RouterView } from 'vue-router'
import Browser from './pages/WorldBrowser.vue'
import ShotDetailPage from './pages/ShotDetailPage.vue'
import SetReviewPage from './pages/SetReviewPage.vue'
import { worldDomains } from './platform/world-catalog'
import CollectionsPage from './pages/CollectionsPage.vue'
import LearnPage from './pages/LearnPage.vue'
import PublicationPage from './pages/PublicationPage.vue'
import ReferenceGallery from './pages/ReferenceGallery.vue'
import ReferenceDetailPage from './pages/ReferenceDetailPage.vue'
import { connectHost, disconnectHost } from './platform/host'
import { canonicalRoute, resultRouteBase } from './platform/route-links'
import { createNavigationQueue } from './platform/navigation-queue'
import './base.css'

const root = document.querySelector<HTMLElement>('#app')!

async function mount() {
  const { appSDK: sdk, dashboardHost: host } = connectHost('world')
  const router = createRouter({ history: createMemoryHistory(), routes: [
    { path: '/', component: Browser },
    { path: '/scene/:assetId/versions/:assetVersionId/review', component: SetReviewPage },
    { path: '/item/:resultKey', component: Browser },
    { path: '/shot/asset/:assetId', component: ShotDetailPage },
    ...(['pose', 'costume'] as const).flatMap(kind => [
      { path: '/' + kind, component: ReferenceGallery, props: { kind } },
      { path: '/' + kind + '/asset/:assetId', component: ReferenceDetailPage, props: { kind } },
    ]),
    ...worldDomains.filter(d=>d.key!=='pose'&&d.key!=='costume').map(d=>({path:'/'+d.key,component:Browser})),
    ...worldDomains.filter(d=>d.key!=='pose'&&d.key!=='costume').map(d=>({path:'/'+d.key+'/item/:resultKey',component:Browser})),
    { path: '/publish', component: PublicationPage },
    { path: '/collections', component: CollectionsPage },
    { path: '/collections/:draftId', component: CollectionsPage },
    { path: '/learn', component: LearnPage },
    { path: '/learn/item/:resultKey', component: LearnPage },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ] })
  const app = createApp({ render: () => h(RouterView) }).use(router)
  document.body.dataset.theme = sdk.theme.get()
  await router.replace(canonicalRoute('world', sdk.navigation.get().path))
  await router.isReady()
  app.mount(root)
  let disposed = false, revision = sdk.navigation.get().revision
  const hostPaths = new Set<string>()
  const navigation = createNavigationQueue(() => canonicalRoute('world', sdk.navigation.get().path),
    path => sdk.navigation.request({ target: 'app', path }), path => {
      if (disposed) return
      hostPaths.add(path)
      void router.replace(path).finally(() => hostPaths.delete(path))
    })
  const stopTheme = sdk.theme.subscribe(theme => { if (!disposed) document.body.dataset.theme = theme })
  const stopRoute = sdk.navigation.subscribe(route => {
    if (disposed || route.revision <= revision) return
    revision = route.revision
    const path = canonicalRoute('world', route.path)
    if (navigation.staleEcho(path) || path === router.currentRoute.value.fullPath) return
    hostPaths.add(path)
    void router.replace(path).finally(() => hostPaths.delete(path))
  })
  const stopAfter = router.afterEach((to, _from, failure) => {
    if (failure || disposed || hostPaths.has(to.fullPath)) return
    navigation.request(to.fullPath)
  })
  // Do not expose a detail page before its browser-history entry exists.
  // Otherwise an immediate Back operates on the previous list/filter entry.
  const stopBefore = router.beforeEach((to, from) => {
    const shotDetail = to.path.startsWith('/shot/asset/')
    if (disposed || hostPaths.has(to.fullPath) || (!shotDetail && !to.path.includes('/item/'))) return
    if (canonicalRoute('world', sdk.navigation.get().path) === to.fullPath) return
    // Preserve the latest list filters even if another filter request is in
    // flight. Coalescing directly to the detail would make Back restore the
    // previous search instead. Adjacent details need no extra list entry.
    const list = shotDetail
      ? (!from.path.startsWith('/shot/asset/') ? from.fullPath : undefined)
      : !from.path.includes('/item/') ? router.resolve({ path: resultRouteBase(to.path), query: to.query }).fullPath : undefined
    navigation.request(to.fullPath, list)
    return false
  })
  const resize = new ResizeObserver(() => { if (!disposed) host.resize(Math.ceil(root.getBoundingClientRect().height)) })
  resize.observe(root)
  const stopLifecycle = sdk.lifecycle.subscribe(event => {
    if (event !== 'dispose' || disposed) return
    disposed = true; navigation.dispose(); resize.disconnect(); stopTheme(); stopRoute(); stopAfter(); stopBefore(); stopLifecycle(); app.unmount(); disconnectHost()
  })
  host.ready()
  host.setDirty(false)
  host.resize(Math.ceil(root.getBoundingClientRect().height))
}
void mount().catch(error => { disconnectHost(); root.textContent = error instanceof Error ? error.message : '应用暂时无法打开。' })
