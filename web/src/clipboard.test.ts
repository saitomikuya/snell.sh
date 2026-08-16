import { describe, expect, it, vi } from 'vitest'
import { copyToClipboard, type CopyEnvironment } from './clipboard'

function fallbackEnvironment(result: boolean) {
  const remove = vi.fn()
  const textarea = {
    value: '',
    style: {},
    setAttribute: vi.fn(),
    focus: vi.fn(),
    select: vi.fn(),
    setSelectionRange: vi.fn(),
    remove,
  }
  const document = {
    body: { appendChild: vi.fn() },
    createElement: vi.fn(() => textarea),
    execCommand: vi.fn(() => result),
  } as unknown as Document
  const environment: CopyEnvironment = { isSecureContext: false, document }
  return { environment, document, textarea, remove }
}

describe('copyToClipboard', () => {
  it('uses the Clipboard API in a secure context', async () => {
    const writeText = vi.fn(async () => undefined)
    const copied = await copyToClipboard('配置内容', { isSecureContext: true, clipboard: { writeText } })
    expect(copied).toBe(true)
    expect(writeText).toHaveBeenCalledWith('配置内容')
  })

  it('falls back to execCommand on a plain HTTP page', async () => {
    const { environment, document, textarea, remove } = fallbackEnvironment(true)
    const copied = await copyToClipboard('HTTP 也能复制', environment)
    expect(copied).toBe(true)
    expect(document.execCommand).toHaveBeenCalledWith('copy')
    expect(textarea.value).toBe('HTTP 也能复制')
    expect(remove).toHaveBeenCalled()
  })

  it('falls back after Clipboard API permission rejection', async () => {
    const { environment, document } = fallbackEnvironment(true)
    environment.isSecureContext = true
    environment.clipboard = { writeText: vi.fn(async () => { throw new Error('denied') }) }
    expect(await copyToClipboard('retry', environment)).toBe(true)
    expect(document.execCommand).toHaveBeenCalledWith('copy')
  })
})
