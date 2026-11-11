// A coherent 1.6px-stroke icon set drawn for NEXUS (24×24 grid).
import React from "react";

const P: Record<string, React.ReactNode> = {
  command: (
    <>
      <rect x="3" y="3" width="7" height="7" rx="1.5" />
      <rect x="14" y="3" width="7" height="5" rx="1.5" />
      <rect x="14" y="12" width="7" height="9" rx="1.5" />
      <rect x="3" y="14" width="7" height="7" rx="1.5" />
    </>
  ),
  fleet: (
    <>
      <rect x="3" y="4" width="18" height="6" rx="1.5" />
      <rect x="3" y="14" width="18" height="6" rx="1.5" />
      <path d="M7 7h.01M7 17h.01M11 7h6M11 17h6" />
    </>
  ),
  topology: (
    <>
      <circle cx="12" cy="5" r="2.2" />
      <circle cx="5" cy="18" r="2.2" />
      <circle cx="19" cy="18" r="2.2" />
      <circle cx="12" cy="13" r="1.6" />
      <path d="M12 7.2v4.2M10.7 14l-4 2.6M13.3 14l4 2.6" />
    </>
  ),
  ai: (
    <>
      <path d="M12 3l1.8 4.6L18.5 9.4l-4.7 1.8L12 16l-1.8-4.8L5.5 9.4l4.7-1.8z" />
      <path d="M18 15l.8 2 2 .8-2 .8-.8 2-.8-2-2-.8 2-.8z" />
    </>
  ),
  alert: (
    <>
      <path d="M12 3.5l9 16H3z" />
      <path d="M12 10v4.2M12 17.2h.01" />
    </>
  ),
  approval: (
    <>
      <path d="M12 3l7 3v5.5c0 4.4-3 7.9-7 9.5-4-1.6-7-5.1-7-9.5V6z" />
      <path d="M8.8 12.2l2.2 2.2 4.4-4.6" />
    </>
  ),
  audit: (
    <>
      <path d="M7 3h7l4 4v14H7z" />
      <path d="M14 3v4h4M10 12h5M10 16h5" />
    </>
  ),
  memory: (
    <>
      <path d="M12 4.5c-3.6 0-6.5 1.3-6.5 3v9c0 1.7 2.9 3 6.5 3s6.5-1.3 6.5-3v-9c0-1.7-2.9-3-6.5-3z" />
      <path d="M5.5 7.5c0 1.7 2.9 3 6.5 3s6.5-1.3 6.5-3M5.5 12c0 1.7 2.9 3 6.5 3s6.5-1.3 6.5-3" />
    </>
  ),
  policy: (
    <>
      <path d="M4 6h10M4 12h6M4 18h10" />
      <circle cx="17" cy="6" r="2.2" />
      <circle cx="13" cy="12" r="2.2" />
      <circle cx="17" cy="18" r="2.2" />
      <path d="M19.2 6H20M15.2 12H20M19.2 18H20" />
    </>
  ),
  settings: (
    <>
      <circle cx="12" cy="12" r="3" />
      <path d="M12 2.8v2.4M12 18.8v2.4M4.2 7.5l2.1 1.2M17.7 15.3l2.1 1.2M4.2 16.5l2.1-1.2M17.7 8.7l2.1-1.2" />
      <circle cx="12" cy="12" r="7" />
    </>
  ),
  server: (
    <>
      <rect x="4" y="3" width="16" height="7" rx="1.5" />
      <rect x="4" y="14" width="16" height="7" rx="1.5" />
      <path d="M8 6.5h.01M8 17.5h.01" />
    </>
  ),
  cpu: (
    <>
      <rect x="6" y="6" width="12" height="12" rx="1.5" />
      <rect x="9.5" y="9.5" width="5" height="5" rx=".6" />
      <path d="M9 3v3M15 3v3M9 18v3M15 18v3M3 9h3M3 15h3M18 9h3M18 15h3" />
    </>
  ),
  memchip: (
    <>
      <rect x="3" y="7" width="18" height="10" rx="1.5" />
      <path d="M7 10v4M11 10v4M15 10v4M6 17v3M10 17v3M14 17v3M18 17v3" />
    </>
  ),
  disk: (
    <>
      <ellipse cx="12" cy="6" rx="7.5" ry="2.8" />
      <path d="M4.5 6v12c0 1.5 3.4 2.8 7.5 2.8s7.5-1.3 7.5-2.8V6M4.5 12c0 1.5 3.4 2.8 7.5 2.8s7.5-1.3 7.5-2.8" />
    </>
  ),
  network: (
    <>
      <circle cx="12" cy="12" r="8.5" />
      <path d="M3.5 12h17M12 3.5c2.3 2.4 3.4 5.2 3.4 8.5s-1.1 6.1-3.4 8.5c-2.3-2.4-3.4-5.2-3.4-8.5s1.1-6.1 3.4-8.5z" />
    </>
  ),
  docker: (
    <>
      <path d="M3 13h15.5c1.6 0 2.5-1 2.5-1-.8-1.4-2.2-1.4-2.2-1.4-.3-1.4-1.6-2.1-1.6-2.1-1 1.2-.6 2.6-.6 2.6" />
      <path d="M3 13c.3 4 3.3 6.5 8 6.5 4.8 0 8-2.6 9.2-6.5" />
      <rect x="6" y="9" width="3" height="3" />
      <rect x="9" y="9" width="3" height="3" />
      <rect x="12" y="9" width="3" height="3" />
      <rect x="9" y="6" width="3" height="3" />
    </>
  ),
  service: (
    <>
      <circle cx="12" cy="12" r="3.2" />
      <path d="M12 4v2.5M12 17.5V20M4 12h2.5M17.5 12H20M6.3 6.3l1.8 1.8M15.9 15.9l1.8 1.8M6.3 17.7l1.8-1.8M15.9 8.1l1.8-1.8" />
    </>
  ),
  logs: (
    <>
      <rect x="3.5" y="4" width="17" height="16" rx="1.5" />
      <path d="M7 9l2.5 2.5L7 14M11.5 15h5" />
    </>
  ),
  package: (
    <>
      <path d="M12 3l8 4.3v9.4L12 21l-8-4.3V7.3z" />
      <path d="M4 7.3l8 4.3 8-4.3M12 11.6V21M8 5.2l8 4.3" />
    </>
  ),
  shield: (
    <>
      <path d="M12 3l7 3v5.5c0 4.4-3 7.9-7 9.5-4-1.6-7-5.1-7-9.5V6z" />
    </>
  ),
  process: (
    <>
      <path d="M4 18l4.5-6 3.5 3.5L16 9l4 5" />
      <path d="M4 4v16h16" />
    </>
  ),
  activity: (
    <>
      <path d="M3 12h4l2.5-6 5 12 2.5-6H21" />
    </>
  ),
  bell: (
    <>
      <path d="M6 16.5V11a6 6 0 0112 0v5.5l1.5 1.5h-15z" />
      <path d="M10 20.5a2 2 0 004 0" />
    </>
  ),
  search: (
    <>
      <circle cx="11" cy="11" r="6.5" />
      <path d="M16 16l4.5 4.5" />
    </>
  ),
  plus: <path d="M12 5v14M5 12h14" />,
  x: <path d="M6 6l12 12M18 6L6 18" />,
  check: <path d="M5 12.5l4.5 4.5L19 7.5" />,
  chevron: <path d="M9 6l6 6-6 6" />,
  chevdown: <path d="M6 9l6 6 6-6" />,
  refresh: (
    <>
      <path d="M20 11a8 8 0 00-14.3-4.6L4 8.5" />
      <path d="M4 4v4.5h4.5M4 13a8 8 0 0014.3 4.6L20 15.5" />
      <path d="M20 20v-4.5h-4.5" />
    </>
  ),
  play: <path d="M8 5.5v13l10.5-6.5z" />,
  stop: <rect x="6.5" y="6.5" width="11" height="11" rx="1.5" />,
  pause: <path d="M8.5 6v12M15.5 6v12" />,
  restart: (
    <>
      <path d="M4.5 12a7.5 7.5 0 107.5-7.5H8.5" />
      <path d="M11 1.8L8.3 4.5 11 7.2" />
    </>
  ),
  send: <path d="M4 12l16-7.5-6.5 16-2.6-6.9z" />,
  trash: (
    <>
      <path d="M4.5 7h15M9.5 7V4.5h5V7M6.5 7l1 13h9l1-13" />
    </>
  ),
  edit: (
    <>
      <path d="M4 20h4l11-11-4-4L4 16z" />
      <path d="M13.5 6.5l4 4" />
    </>
  ),
  external: (
    <>
      <path d="M14 4h6v6M20 4l-9 9" />
      <path d="M18 14v6H4V6h6" />
    </>
  ),
  lock: (
    <>
      <rect x="5" y="10.5" width="14" height="10" rx="1.5" />
      <path d="M8 10.5V7.5a4 4 0 018 0v3" />
    </>
  ),
  key: (
    <>
      <circle cx="8" cy="15" r="4" />
      <path d="M11 12.2L19 4.5M16 7.5l2.5 2.5M14 9.5l2 2" />
    </>
  ),
  eye: (
    <>
      <path d="M2.5 12S6 5.5 12 5.5 21.5 12 21.5 12 18 18.5 12 18.5 2.5 12 2.5 12z" />
      <circle cx="12" cy="12" r="3" />
    </>
  ),
  info: (
    <>
      <circle cx="12" cy="12" r="8.5" />
      <path d="M12 11v5.5M12 7.8h.01" />
    </>
  ),
  bolt: <path d="M13 3L5 13.5h6L10 21l8-10.5h-6z" />,
  sun: (
    <>
      <circle cx="12" cy="12" r="3.8" />
      <path d="M12 2.5v2M12 19.5v2M2.5 12h2M19.5 12h2M5.3 5.3l1.4 1.4M17.3 17.3l1.4 1.4M5.3 18.7l1.4-1.4M17.3 6.7l1.4-1.4" />
    </>
  ),
  moon: <path d="M19 14.5A7.5 7.5 0 019.5 5a7.5 7.5 0 109.5 9.5z" />,
  sidebar: (
    <>
      <rect x="3.5" y="4.5" width="17" height="15" rx="1.5" />
      <path d="M9.5 4.5v15" />
    </>
  ),
  download: (
    <>
      <path d="M12 4v11M7.5 10.5L12 15l4.5-4.5M5 19.5h14" />
    </>
  ),
  layers: (
    <>
      <path d="M12 4l8.5 4.5L12 13 3.5 8.5z" />
      <path d="M3.5 12.5L12 17l8.5-4.5M3.5 16.5L12 21l8.5-4.5" />
    </>
  ),
  thermo: (
    <>
      <path d="M10 14.5V5a2 2 0 014 0v9.5a4 4 0 11-4 0z" />
      <path d="M12 9v7" />
    </>
  ),
  clock: (
    <>
      <circle cx="12" cy="12" r="8.5" />
      <path d="M12 7.5V12l3 2" />
    </>
  ),
  user: (
    <>
      <circle cx="12" cy="8.5" r="3.8" />
      <path d="M4.5 20c.8-3.8 3.8-6 7.5-6s6.7 2.2 7.5 6" />
    </>
  ),
  flame: <path d="M12 3c1 3.5 5 5.2 5 10a5 5 0 01-10 0c0-2.5 1.2-4 2.5-5.2.2 1.7 1 2.7 2 3.2-.3-3 .2-5.5.5-8z" />,
  globe: (
    <>
      <circle cx="12" cy="12" r="8.5" />
      <path d="M3.5 12h17M12 3.5v17" />
    </>
  ),
  database: (
    <>
      <ellipse cx="12" cy="5.5" rx="7" ry="2.5" />
      <path d="M5 5.5v13c0 1.4 3.1 2.5 7 2.5s7-1.1 7-2.5v-13M5 12c0 1.4 3.1 2.5 7 2.5s7-1.1 7-2.5" />
    </>
  ),
  proxy: (
    <>
      <path d="M3 12h5M16 12h5M8 7h8v10H8z" />
      <path d="M11 10l2 2-2 2" />
    </>
  ),
  desktop: (
    <>
      <rect x="3" y="4" width="18" height="12" rx="1.5" />
      <path d="M9 20h6M12 16v4" />
    </>
  ),
  copy: (
    <>
      <rect x="8.5" y="8.5" width="11" height="11" rx="1.5" />
      <path d="M15.5 8.5V5.5a1 1 0 00-1-1h-9a1 1 0 00-1 1v9a1 1 0 001 1h3" />
    </>
  ),
  filter: <path d="M4 5h16l-6 7.5V19l-4 1.5v-8z" />,
  grip: <path d="M9 6h.01M15 6h.01M9 12h.01M15 12h.01M9 18h.01M15 18h.01" />,
  arrowup: <path d="M12 19V5M6 11l6-6 6 6" />,
  arrowdown: <path d="M12 5v14M6 13l6 6 6-6" />,
};

export function Icon({ name, size = 16, stroke = 1.6, className, style }: { name: string; size?: number; stroke?: number; className?: string; style?: React.CSSProperties }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={stroke} strokeLinecap="round" strokeLinejoin="round" className={className} style={style} aria-hidden="true">
      {P[name] ?? P.info}
    </svg>
  );
}

export function Logo({ size = 22 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 32 32" className="brand-mark" aria-hidden="true">
      <defs>
        <linearGradient id="nx-g" x1="0" y1="0" x2="1" y2="1">
          <stop offset="0" stopColor="var(--accent-2)" />
          <stop offset="1" stopColor="var(--accent)" />
        </linearGradient>
      </defs>
      <rect x="1.5" y="1.5" width="29" height="29" rx="7" fill="none" stroke="var(--line-3)" />
      <path d="M9 23V9l14 14V9" fill="none" stroke="url(#nx-g)" strokeWidth="2.6" strokeLinecap="round" strokeLinejoin="round" />
      <circle cx="23" cy="9" r="1.8" fill="var(--ok)" />
    </svg>
  );
}
