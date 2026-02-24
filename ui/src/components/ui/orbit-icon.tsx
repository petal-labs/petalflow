import type { SVGProps } from 'react'
import { cn } from '@/lib/utils'

interface OrbitIconProps extends SVGProps<SVGSVGElement> {
  size?: number
}

export function OrbitIcon({ size = 16, className, ...props }: OrbitIconProps) {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={2}
      strokeLinecap="round"
      strokeLinejoin="round"
      className={cn('orbit-icon', className)}
      {...props}
    >
      <path d="M20.341 6.484A10 10 0 0 1 10.266 21.85" />
      <path d="M3.659 17.516A10 10 0 0 1 13.74 2.152" />
      <circle cx="12" cy="12" r="3" />
      <circle cx="19" cy="5" r="2" />
      <circle cx="5" cy="19" r="2" />
    </svg>
  )
}
