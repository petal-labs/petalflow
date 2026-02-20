import { cn } from '@/lib/utils'

export type IconName =
  | 'workflows'
  | 'designer'
  | 'runs'
  | 'tools'
  | 'providers'
  | 'settings'
  | 'play'
  | 'check'
  | 'x'
  | 'clock'
  | 'zap'
  | 'sun'
  | 'moon'
  | 'chevron'
  | 'chevron-right'
  | 'plus'
  | 'cpu'
  | 'git'
  | 'file'
  | 'user'
  | 'search'
  | 'bot'
  | 'list'
  | 'arrow'
  | 'arrow-left'
  | 'export'
  | 'eject'
  | 'trash'
  | 'edit'
  | 'copy'
  | 'code'
  | 'eye'
  | 'eye-off'
  | 'logout'

interface IconProps {
  name: IconName
  size?: number
  className?: string
}

const icons: Record<IconName, React.ReactNode> = {
  workflows: <path d="M4 6h16M4 12h16M4 18h7" strokeWidth="2" strokeLinecap="round" />,
  designer: <path d="M12 3L2 12h3v8h6v-6h2v6h6v-8h3L12 3z" strokeWidth="1.5" fill="none" />,
  runs: (
    <>
      <circle cx="12" cy="12" r="9" strokeWidth="2" fill="none" />
      <path d="M10 8l6 4-6 4V8z" fill="currentColor" stroke="none" />
    </>
  ),
  tools: (
    <path
      d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z"
      strokeWidth="1.5"
      fill="none"
    />
  ),
  providers: (
    <>
      <rect x="2" y="6" width="20" height="12" rx="2" strokeWidth="2" fill="none" />
      <path d="M6 10h.01M10 10h.01M14 10h.01" strokeWidth="3" strokeLinecap="round" />
    </>
  ),
  settings: (
    <>
      <circle cx="12" cy="12" r="3" strokeWidth="2" fill="none" />
      <path
        d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"
        strokeWidth="1.5"
        fill="none"
      />
    </>
  ),
  play: <path d="M5 3l14 9-14 9V3z" fill="currentColor" stroke="none" />,
  check: (
    <path d="M5 12l5 5L20 7" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round" />
  ),
  x: (
    <>
      <path d="M18 6L6 18M6 6l12 12" strokeWidth="2" strokeLinecap="round" />
    </>
  ),
  clock: (
    <>
      <circle cx="12" cy="12" r="9" strokeWidth="2" fill="none" />
      <path d="M12 7v5l3 3" strokeWidth="2" strokeLinecap="round" />
    </>
  ),
  zap: <path d="M13 2L3 14h9l-1 8 10-12h-9l1-8z" strokeWidth="1.5" fill="none" />,
  sun: (
    <>
      <circle cx="12" cy="12" r="4" strokeWidth="2" fill="none" />
      <path
        d="M12 2v2m0 16v2M4.93 4.93l1.41 1.41m11.32 11.32l1.41 1.41M2 12h2m16 0h2M4.93 19.07l1.41-1.41m11.32-11.32l1.41-1.41"
        strokeWidth="2"
        strokeLinecap="round"
      />
    </>
  ),
  moon: <path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z" strokeWidth="2" fill="none" />,
  chevron: (
    <path d="M9 18l6-6-6-6" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
  ),
  'chevron-right': (
    <path d="M9 18l6-6-6-6" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
  ),
  plus: <path d="M12 5v14m-7-7h14" strokeWidth="2" strokeLinecap="round" />,
  cpu: (
    <>
      <rect x="4" y="4" width="16" height="16" rx="2" strokeWidth="2" fill="none" />
      <rect x="9" y="9" width="6" height="6" strokeWidth="2" fill="none" />
      <path
        d="M9 1v3m6-3v3M9 20v3m6-3v3M1 9h3m-3 6h3M20 9h3m-3 6h3"
        strokeWidth="2"
        strokeLinecap="round"
      />
    </>
  ),
  git: (
    <>
      <circle cx="12" cy="6" r="2" strokeWidth="2" fill="none" />
      <circle cx="6" cy="18" r="2" strokeWidth="2" fill="none" />
      <circle cx="18" cy="18" r="2" strokeWidth="2" fill="none" />
      <path d="M12 8v4m-4 2.5L12 12l4 2.5" strokeWidth="2" />
    </>
  ),
  file: (
    <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8l-6-6z" strokeWidth="1.5" fill="none" />
  ),
  user: (
    <>
      <circle cx="12" cy="8" r="4" strokeWidth="2" fill="none" />
      <path d="M20 21a8 8 0 0 0-16 0" strokeWidth="2" />
    </>
  ),
  search: (
    <>
      <circle cx="11" cy="11" r="7" strokeWidth="2" fill="none" />
      <path d="M21 21l-4.35-4.35" strokeWidth="2" strokeLinecap="round" />
    </>
  ),
  bot: (
    <>
      <rect x="3" y="8" width="18" height="12" rx="2" strokeWidth="2" fill="none" />
      <circle cx="9" cy="14" r="1.5" fill="currentColor" />
      <circle cx="15" cy="14" r="1.5" fill="currentColor" />
      <path d="M12 2v4M8 8V6m8 2V6" strokeWidth="2" strokeLinecap="round" />
    </>
  ),
  list: (
    <path
      d="M8 6h13M8 12h13M8 18h13M3 6h.01M3 12h.01M3 18h.01"
      strokeWidth="2"
      strokeLinecap="round"
    />
  ),
  arrow: (
    <path
      d="M5 12h14m-7-7l7 7-7 7"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
    />
  ),
  'arrow-left': (
    <path
      d="M19 12H5m7 7l-7-7 7-7"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
    />
  ),
  export: (
    <>
      <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" strokeWidth="2" />
      <path
        d="M7 10l5 5 5-5M12 15V3"
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </>
  ),
  eject: (
    <path
      d="M12 5l-8 10h16L12 5zm-8 14h16"
      strokeWidth="1.5"
      fill="none"
      strokeLinecap="round"
      strokeLinejoin="round"
    />
  ),
  trash: (
    <>
      <path d="M3 6h18M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" strokeWidth="2" strokeLinecap="round" />
      <path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6" strokeWidth="2" fill="none" />
    </>
  ),
  edit: (
    <path
      d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"
      strokeWidth="2"
      fill="none"
    />
  ),
  copy: (
    <>
      <rect x="9" y="9" width="13" height="13" rx="2" strokeWidth="2" fill="none" />
      <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" strokeWidth="2" />
    </>
  ),
  code: (
    <path
      d="M16 18l6-6-6-6M8 6l-6 6 6 6"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
    />
  ),
  eye: (
    <>
      <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z" strokeWidth="2" fill="none" />
      <circle cx="12" cy="12" r="3" strokeWidth="2" fill="none" />
    </>
  ),
  'eye-off': (
    <>
      <path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94" strokeWidth="2" fill="none" />
      <path d="M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19" strokeWidth="2" fill="none" />
      <path d="M14.12 14.12a3 3 0 1 1-4.24-4.24" strokeWidth="2" fill="none" />
      <path d="M1 1l22 22" strokeWidth="2" strokeLinecap="round" />
    </>
  ),
  logout: (
    <>
      <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4" strokeWidth="2" />
      <path d="M16 17l5-5-5-5M21 12H9" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
    </>
  ),
}

export function Icon({ name, size = 16, className }: IconProps) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      className={cn('shrink-0', className)}
    >
      {icons[name]}
    </svg>
  )
}
