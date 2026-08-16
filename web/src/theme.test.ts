import { describe, expect, it } from 'vitest'
import { applyTheme, getStoredTheme, nextTheme, THEME_STORAGE_KEY } from './theme'

describe('theme', () => {
  it('uses dark as the default and restores light mode', () => {
    expect(getStoredTheme({ getItem: () => null })).toBe('dark')
    expect(getStoredTheme({ getItem: () => 'light' })).toBe('light')
    expect(getStoredTheme({ getItem: () => 'unknown' })).toBe('dark')
  })

  it('toggles between light and dark', () => {
    expect(nextTheme('dark')).toBe('light')
    expect(nextTheme('light')).toBe('dark')
  })

  it('applies and persists the selected theme', () => {
    const root = { dataset: {}, style: { colorScheme: '' } } as unknown as HTMLElement
    const saved: Record<string, string> = {}
    applyTheme('light', { root, storage: { setItem: (key, value) => { saved[key] = value } } })
    expect(root.dataset.theme).toBe('light')
    expect(root.style.colorScheme).toBe('light')
    expect(saved[THEME_STORAGE_KEY]).toBe('light')
  })
})
