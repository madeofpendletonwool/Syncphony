import { describe, expect, it } from 'vitest'
import { appTitle, formatBytes } from './server'

describe('formatBytes', () => {
  it('picks a unit', () => {
    expect(formatBytes(0)).toBe('0 bytes')
    expect(formatBytes(1023)).toBe('1023 bytes')
    expect(formatBytes(1536)).toBe('1.5 KB')
    expect(formatBytes(812 * 1024)).toBe('812 KB')
    expect(formatBytes(1.4 * 1024 ** 3)).toBe('1.4 GB')
  })
})

describe('appTitle', () => {
  it('adds the server name', () => {
    expect(appTitle('')).toBe('Syncphony')
    expect(appTitle('The Den')).toBe('The Den · Syncphony')
  })
})
