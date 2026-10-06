import { useMemo } from 'react'
import { encode } from 'uqr'
import { cn } from '@/lib/utils'

/** A QR code as one SVG path, in the current text color. */
export function QrCode({ value, className, label }: { value: string; className?: string; label: string }) {
  const { size, d } = useMemo(() => {
    const qr = encode(value, { ecc: 'M', border: 1 })
    let path = ''
    qr.data.forEach((row, y) =>
      row.forEach((on, x) => {
        if (on) path += `M${x} ${y}h1v1h-1z`
      }),
    )
    return { size: qr.size, d: path }
  }, [value])
  return (
    <svg viewBox={`0 0 ${size} ${size}`} role="img" aria-label={label} shapeRendering="crispEdges" className={cn('block', className)}>
      <path d={d} fill="currentColor" />
    </svg>
  )
}
