// Service worker: shows push notifications and opens the app when tapped
self.addEventListener('install', () => self.skipWaiting())
self.addEventListener('activate', (event) => event.waitUntil(self.clients.claim()))

self.addEventListener('push', (event) => {
  let msg = { title: 'deploysmith', body: '' }
  try {
    msg = { ...msg, ...event.data.json() }
  } catch (e) {
    // keep the defaults for a payload that is not JSON
  }
  event.waitUntil(
    self.registration.showNotification(msg.title, {
      body: msg.body,
      tag: msg.tag || undefined,
      icon: '/ui/static/icon-192.png',
      badge: '/ui/static/icon-192.png',
      data: { url: msg.url || '/ui/' },
    })
  )
})

self.addEventListener('notificationclick', (event) => {
  event.notification.close()
  const url = event.notification.data.url
  event.waitUntil(
    self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then((windows) => {
      for (const client of windows) {
        if ('focus' in client && 'navigate' in client) {
          return client.focus().then(() => client.navigate(url))
        }
      }
      return self.clients.openWindow(url)
    })
  )
})
