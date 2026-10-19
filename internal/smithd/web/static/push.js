// Opt-in to push notifications. Browsers (iOS in particular) only allow
// asking for permission from a tap, so this is driven by a button.
(function () {
  const supported = 'serviceWorker' in navigator && 'PushManager' in window && 'Notification' in window

  function urlBase64ToUint8Array(base64) {
    const padded = (base64 + '='.repeat((4 - (base64.length % 4)) % 4)).replace(/-/g, '+').replace(/_/g, '/')
    return Uint8Array.from(atob(padded), (c) => c.charCodeAt(0))
  }

  async function registration() {
    return navigator.serviceWorker.register('/ui/sw.js', { scope: '/ui/' }).then(() => navigator.serviceWorker.ready)
  }

  async function post(path, body) {
    const res = await fetch('/ui/push/' + path, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    })
    if (!res.ok) throw new Error(await res.text())
  }

  function label(button, text, disabled) {
    button.textContent = text
    button.disabled = !!disabled
  }

  async function refresh() {
    const button = document.getElementById('push-toggle')
    if (!button) return
    if (!button.dataset.key) {
      button.hidden = true
      return
    }
    button.hidden = false
    if (!supported) {
      // iOS only offers push to apps added to the home screen
      label(button, 'Add to Home Screen for notifications', true)
      return
    }
    if (Notification.permission === 'denied') {
      label(button, 'Notifications blocked', true)
      return
    }
    const reg = await registration()
    const sub = await reg.pushManager.getSubscription()
    button.dataset.subscribed = sub ? 'true' : 'false'
    label(button, sub ? 'Notifications on' : 'Enable notifications')
  }

  async function toggle(button) {
    label(button, 'Working…', true)
    try {
      const reg = await registration()
      const existing = await reg.pushManager.getSubscription()
      if (existing) {
        await post('unsubscribe', { endpoint: existing.endpoint })
        await existing.unsubscribe()
      } else {
        if ((await Notification.requestPermission()) !== 'granted') return
        const sub = await reg.pushManager.subscribe({
          userVisibleOnly: true,
          applicationServerKey: urlBase64ToUint8Array(button.dataset.key),
        })
        await post('subscribe', sub.toJSON())
      }
    } catch (e) {
      console.error('push toggle failed', e)
      alert('Could not change notifications: ' + e.message)
    } finally {
      refresh()
    }
  }

  document.addEventListener('click', (event) => {
    const button = event.target.closest('#push-toggle')
    if (button) toggle(button)
  })
  document.addEventListener('DOMContentLoaded', refresh)
  document.addEventListener('htmx:afterSettle', refresh)
})()
