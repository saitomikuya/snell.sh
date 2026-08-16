export interface CopyEnvironment {
  isSecureContext: boolean
  clipboard?: { writeText(value: string): Promise<void> }
  document?: Document
}

function browserEnvironment(): CopyEnvironment {
  return {
    isSecureContext: typeof window !== 'undefined' && window.isSecureContext,
    clipboard: typeof navigator !== 'undefined' ? navigator.clipboard : undefined,
    document: typeof document !== 'undefined' ? document : undefined,
  }
}

export async function copyToClipboard(value: string, environment: CopyEnvironment = browserEnvironment()): Promise<boolean> {
  if (environment.isSecureContext && environment.clipboard) {
    try {
      await environment.clipboard.writeText(value)
      return true
    } catch {
      // Safari and permission policies can still reject the modern API.
    }
  }

  const doc = environment.document
  if (!doc?.body || typeof doc.execCommand !== 'function') return false
  const textarea = doc.createElement('textarea')
  textarea.value = value
  textarea.setAttribute('readonly', '')
  textarea.setAttribute('aria-hidden', 'true')
  textarea.style.position = 'fixed'
  textarea.style.left = '-9999px'
  textarea.style.top = '0'
  textarea.style.opacity = '0'
  doc.body.appendChild(textarea)
  textarea.focus()
  textarea.select()
  textarea.setSelectionRange(0, value.length)
  try {
    return doc.execCommand('copy')
  } catch {
    return false
  } finally {
    textarea.remove()
  }
}
