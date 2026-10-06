import { useSyncExternalStore } from 'react'
import { createStore, useStore } from './store'
import { toast } from './toast'

/**
 * Installing the app, the service worker that keeps its shell offline, and
 * whether we're online. See docs/adr/0007-pwa-and-native-audio.md.
 */

/** Registers the service worker (production builds only) and offers to reload when a new version is ready. */
export function registerServiceWorker() {
  if (!import.meta.env.PROD || !('serviceWorker' in navigator)) return
  // A page that was already controlled is being updated: reload into the
  // new version once it takes over. A first install needs no reload.
  const updating = !!navigator.serviceWorker.controller
  let reloaded = false
  navigator.serviceWorker.addEventListener('controllerchange', () => {
    if (!updating || reloaded) return
    reloaded = true
    location.reload()
  })
  void navigator.serviceWorker.register('/sw.js').then((reg) => {
    const offer = (worker: ServiceWorker) =>
      toast(
        {
          message: 'A new version of Syncphony is ready',
          action: { label: 'Reload', onClick: () => worker.postMessage('skip-waiting') },
        },
        60_000,
      )
    if (reg.waiting && navigator.serviceWorker.controller) offer(reg.waiting)
    reg.addEventListener('updatefound', () => {
      const worker = reg.installing
      worker?.addEventListener('statechange', () => {
        if (worker.state === 'installed' && navigator.serviceWorker.controller) offer(worker)
      })
    })
    // Long-lived tabs (the speaker) should still notice deploys.
    setInterval(() => void reg.update().catch(() => {}), 60 * 60_000)
  })
}

/** Chromium's install prompt event, which TypeScript's DOM types lack. */
type InstallPromptEvent = Event & { prompt(): Promise<void>; userChoice: Promise<{ outcome: 'accepted' | 'dismissed' }> }

const installPrompt = createStore<InstallPromptEvent | undefined>(undefined)
const installedNow = createStore(false)

if (typeof window !== 'undefined') {
  window.addEventListener('beforeinstallprompt', (e) => {
    // Keep it for our own Install button instead of the browser's mini-bar.
    e.preventDefault()
    installPrompt.set(e as InstallPromptEvent)
  })
  window.addEventListener('appinstalled', () => {
    installPrompt.set(undefined)
    installedNow.set(true)
  })
}

/** Whether this is an iPhone or iPad, where installing is manual (Share, Add to Home Screen). */
export function isIOS(ua = navigator.userAgent, touchPoints = navigator.maxTouchPoints) {
  // iPadOS reports itself as a Mac, but Macs don't have touch screens.
  return /iPhone|iPad|iPod/.test(ua) || (/Macintosh/.test(ua) && touchPoints > 1)
}

/** Whether we're running as an installed app, not in a browser tab. */
export function isStandalone() {
  return (
    window.matchMedia?.('(display-mode: standalone)').matches ||
    (navigator as Navigator & { standalone?: boolean }).standalone === true
  )
}

export type InstallState =
  | { kind: 'installed' }
  /** The browser can install it: call install(). */
  | { kind: 'prompt'; install: () => Promise<void> }
  /** iOS: Share, then Add to Home Screen. */
  | { kind: 'ios' }
  /** Nothing we can offer (a browser without install support, or it was dismissed). */
  | { kind: 'unavailable' }

/** How this device can install the app, if it can. */
export function useInstall(): InstallState {
  const prompt = useStore(installPrompt)
  const justInstalled = useStore(installedNow)
  if (justInstalled || isStandalone()) return { kind: 'installed' }
  if (prompt) {
    return {
      kind: 'prompt',
      install: async () => {
        await prompt.prompt()
        // A prompt can only be used once.
        installPrompt.set(undefined)
      },
    }
  }
  return isIOS() ? { kind: 'ios' } : { kind: 'unavailable' }
}

function subscribeOnline(cb: () => void) {
  window.addEventListener('online', cb)
  window.addEventListener('offline', cb)
  return () => {
    window.removeEventListener('online', cb)
    window.removeEventListener('offline', cb)
  }
}

/** Whether the browser thinks it's online. */
export function useOnline() {
  return useSyncExternalStore(subscribeOnline, () => navigator.onLine, () => true)
}
