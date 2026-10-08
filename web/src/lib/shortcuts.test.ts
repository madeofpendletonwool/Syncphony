import { describe, expect, it } from 'vitest'
import { cycle, isTyping, shortcutOf, tvKeyOf } from './shortcuts'

const press = (key: string, more: Partial<Parameters<typeof shortcutOf>[0]> = {}) =>
  shortcutOf({ key, metaKey: false, ctrlKey: false, altKey: false, repeat: false, defaultPrevented: false, target: document.body, ...more })

const el = (html: string) => {
  const host = document.createElement('div')
  host.innerHTML = html
  return host.firstElementChild as HTMLElement
}

describe('shortcutOf', () => {
  it('maps the keys', () => {
    expect(press(' ')).toBe('playPause')
    expect(press('n')).toBe('skip')
    expect(press('N')).toBe('skip')
    expect(press('/')).toBe('search')
    expect(press('k')).toBe('playPause')
    expect(press('K')).toBe('playPause')
    expect(press('k', { metaKey: true })).toBe('search')
    expect(press('k', { ctrlKey: true })).toBe('search')
    expect(press('l')).toBe('lyrics')
    expect(press('?')).toBe('help')
    expect(press('x')).toBeUndefined()
  })

  it('never fires while typing', () => {
    for (const html of ['<input>', '<input type="search">', '<textarea></textarea>', '<div contenteditable="true"></div>']) {
      const target = el(html)
      expect(press(' ', { target })).toBeUndefined()
      expect(press('n', { target })).toBeUndefined()
      expect(press('k', { target, metaKey: true })).toBeUndefined()
    }
  })

  it('leaves space to the button it would press', () => {
    expect(press(' ', { target: el('<button>Play</button>') })).toBeUndefined()
    expect(press(' ', { target: el('<div role="switch"></div>') })).toBeUndefined()
    expect(press('n', { target: el('<button>Play</button>') })).toBe('skip')
    expect(press('k', { target: el('<button>Play</button>') })).toBe('playPause')
  })

  it('stays out of the browser’s own combos and held keys', () => {
    expect(press('n', { metaKey: true })).toBeUndefined()
    expect(press('l', { ctrlKey: true })).toBeUndefined()
    expect(press('k', { metaKey: true, altKey: true })).toBeUndefined()
    expect(press(' ', { repeat: true })).toBeUndefined()
    expect(press('n', { repeat: true })).toBeUndefined()
    expect(press(' ', { defaultPrevented: true })).toBeUndefined()
  })
})

describe('isTyping', () => {
  it('counts text fields, not checkboxes or sliders', () => {
    expect(isTyping(el('<input type="text">'))).toBe(true)
    expect(isTyping(el('<input type="checkbox">'))).toBe(false)
    expect(isTyping(el('<input type="range">'))).toBe(false)
    expect(isTyping(document.body)).toBe(false)
    expect(isTyping(null)).toBe(false)
  })
})

describe('tvKeyOf', () => {
  const tv = (key: string, more: Partial<Parameters<typeof tvKeyOf>[0]> = {}) =>
    tvKeyOf({ key, metaKey: false, ctrlKey: false, altKey: false, repeat: false, defaultPrevented: false, target: document.body, ...more })

  it('maps the keys', () => {
    expect(tv(' ')).toBe('playPause')
    expect(tv('ArrowRight')).toBe('skip')
    expect(tv('ArrowUp')).toBe('previousLook')
    expect(tv('ArrowDown')).toBe('nextLook')
    expect(tv('ArrowLeft')).toBeUndefined()
  })

  it('leaves space and → to a focused button, for the remote', () => {
    const target = el('<button>Stop</button>')
    expect(tv(' ', { target })).toBeUndefined()
    expect(tv('ArrowRight', { target })).toBeUndefined()
    expect(tv('ArrowDown', { target })).toBe('nextLook')
    expect(tv('k', { target })).toBe('playPause')
  })

  it('stays out of combos, held skips, and typing', () => {
    expect(tv('ArrowRight', { repeat: true })).toBeUndefined()
    expect(tv('ArrowDown', { repeat: true })).toBe('nextLook')
    expect(tv('ArrowRight', { altKey: true })).toBeUndefined()
    expect(tv('ArrowDown', { target: el('<input>') })).toBeUndefined()
    expect(tv('k', { target: el('<input>') })).toBeUndefined()
    expect(tv('k', { repeat: true })).toBeUndefined()
  })
})

describe('cycle', () => {
  it('wraps both ways', () => {
    expect(cycle(['a', 'b', 'c'], 'c', 1)).toBe('a')
    expect(cycle(['a', 'b', 'c'], 'a', -1)).toBe('c')
    expect(cycle(['a', 'b', 'c'], 'x', 1)).toBe('b')
  })
})
