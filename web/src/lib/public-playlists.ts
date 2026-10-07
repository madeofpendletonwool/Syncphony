import { createStore, useStore } from './store'

// Whether search looks through public playlists too. Like recent searches,
// it's this device's choice, and storage is best effort.
const KEY = 'syncphony:public-playlists'

function load(): boolean {
  try {
    return localStorage.getItem(KEY) === 'true'
  } catch {
    return false
  }
}

const publicPlaylists = createStore<boolean>(load())

export function setPublicPlaylists(on: boolean) {
  publicPlaylists.set(on)
  try {
    localStorage.setItem(KEY, String(on))
  } catch {
    // Kept for this visit only.
  }
}

export function usePublicPlaylists() {
  return useStore(publicPlaylists)
}
