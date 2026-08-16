export type Theme = 'dark' | 'light'

export const THEME_STORAGE_KEY = 'proxy-panel-theme'

type ReadableStorage = Pick<Storage, 'getItem'>
type WritableStorage = Pick<Storage, 'setItem'>

function browserStorage(): Storage | undefined {
  if (typeof window === 'undefined') return undefined
  try {
    return window.localStorage
  } catch {
    return undefined
  }
}

export function getStoredTheme(storage: ReadableStorage | undefined = browserStorage()): Theme {
  try {
    return storage?.getItem(THEME_STORAGE_KEY) === 'light' ? 'light' : 'dark'
  } catch {
    return 'dark'
  }
}

export function applyTheme(
  theme: Theme,
  options: { root?: HTMLElement; storage?: WritableStorage; persist?: boolean } = {},
): void {
  const root = options.root ?? (typeof document === 'undefined' ? undefined : document.documentElement)
  if (root) {
    root.dataset.theme = theme
    root.style.colorScheme = theme
  }
  if (options.persist === false) return
  const storage = options.storage ?? browserStorage()
  try {
    storage?.setItem(THEME_STORAGE_KEY, theme)
  } catch {
    // localStorage 不可用时仍保持当前页面主题，不影响面板操作。
  }
}

export function nextTheme(theme: Theme): Theme {
  return theme === 'dark' ? 'light' : 'dark'
}
