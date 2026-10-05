let label: HTMLLabelElement | undefined

/**
 * A light tap on phones that support it. Android has the Vibration API.
 * iOS Safari (18+) has no API, but toggling a native `switch` checkbox
 * plays its haptic, so we keep a hidden one around and click its label.
 */
export function tap() {
  if (typeof navigator === 'undefined') return
  if (window.matchMedia?.('(prefers-reduced-motion: reduce)').matches) return
  if ('vibrate' in navigator && navigator.vibrate(10)) return
  if (!/iPhone|iPad/.test(navigator.userAgent)) return
  if (!label) {
    label = document.createElement('label')
    label.ariaHidden = 'true'
    label.style.display = 'none'
    const input = document.createElement('input')
    input.type = 'checkbox'
    input.setAttribute('switch', '')
    input.tabIndex = -1
    label.append(input)
    document.body.append(label)
  }
  label.click()
}
