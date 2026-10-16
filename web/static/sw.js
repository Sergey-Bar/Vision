// VISION app-shell service worker. Caches the chrome; falls through to network
// for data so the console never answers a live question from a stale record.
const CACHE = 'vision-shell-v1';
const SHELL = ['/', '/ask', '/chief', '/static/app.css', '/static/showcase.css', '/static/ask.js', '/static/chief.js', '/static/manifest.json'];

self.addEventListener('install', (e) => {
  e.waitUntil(caches.open(CACHE).then((c) => c.addAll(SHELL)).catch(() => {}));
  self.skipWaiting();
});
self.addEventListener('activate', (e) => {
  e.waitUntil(caches.keys().then((keys) => Promise.all(keys.filter((k) => k !== CACHE).map((k) => caches.delete(k)))));
  self.clients.claim();
});
self.addEventListener('fetch', (e) => {
  const url = new URL(e.request.url);
  if (e.request.method !== 'GET') return;
  if (url.pathname.startsWith('/api/')) return; // live data always networked
  if (url.pathname.startsWith('/static/icon')) return;
  e.respondWith(caches.match(e.request).then((cached) => {
    if (cached) return cached;
    return fetch(e.request).then((res) => {
      if (res.ok && (url.pathname === '/' || url.pathname.endsWith('.html') || url.pathname.endsWith('.css') || url.pathname.endsWith('.js'))) {
        const clone = res.clone();
        caches.open(CACHE).then((c) => c.put(e.request, clone));
      }
      return res;
    }).catch(() => cached || Response.error());
  }));
});