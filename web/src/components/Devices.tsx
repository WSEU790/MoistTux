// Hardware-style topology nodes: rack servers, a CPU for the AI core, a GPU
// for the inference engine, switches, storage and shipping containers.
// Drawn as SVG centred on (0,0). Status shows on the power LED; activity
// LEDs blink in the animation theme's colour (CSS variable --act).

export const DEVICE: Record<string, { w: number; h: number }> = {
  desktop: { w: 66, h: 52 },
  core: { w: 62, h: 62 },
  ollama: { w: 88, h: 36 },
  server: { w: 100, h: 42 },
  docker: { w: 70, h: 44 },
  service: { w: 74, h: 24 },
  proxy: { w: 92, h: 28 },
  database: { w: 46, h: 54 },
  container: { w: 48, h: 26 },
};

export const deviceOf = (kind: string) => DEVICE[kind] ?? DEVICE.container;

const LED: Record<string, string> = { ok: "#34d399", warning: "#fbbf24", critical: "#f43f5e", offline: "#475569", unknown: "#64748b" };

/** Shared gradients and filters; render once inside the topology <svg>. */
export function DeviceDefs() {
  return (
    <>
      <linearGradient id="hw-body" x1="0" y1="0" x2="0" y2="1">
        <stop offset="0" stopColor="#2b3344" />
        <stop offset="0.5" stopColor="#1a2030" />
        <stop offset="1" stopColor="#11151f" />
      </linearGradient>
      <linearGradient id="hw-face" x1="0" y1="0" x2="0" y2="1">
        <stop offset="0" stopColor="#1d2331" />
        <stop offset="1" stopColor="#0d1017" />
      </linearGradient>
      <linearGradient id="hw-metal" x1="0" y1="0" x2="0" y2="1">
        <stop offset="0" stopColor="#9aa4b6" />
        <stop offset="1" stopColor="#5b6577" />
      </linearGradient>
      <linearGradient id="hw-gold" x1="0" y1="0" x2="0" y2="1">
        <stop offset="0" stopColor="#f2d27a" />
        <stop offset="1" stopColor="#b8862f" />
      </linearGradient>
      <linearGradient id="hw-screen" x1="0" y1="0" x2="1" y2="1">
        <stop offset="0" stopColor="#0f1a3a" />
        <stop offset="1" stopColor="#090d1c" />
      </linearGradient>
      <linearGradient id="hw-crate" x1="0" y1="0" x2="0" y2="1">
        <stop offset="0" stopColor="#24405f" />
        <stop offset="1" stopColor="#152536" />
      </linearGradient>
      <linearGradient id="hw-disk" x1="0" y1="0" x2="1" y2="0">
        <stop offset="0" stopColor="#151a26" />
        <stop offset="0.45" stopColor="#2c3446" />
        <stop offset="1" stopColor="#11151f" />
      </linearGradient>
      <radialGradient id="hw-die">
        <stop offset="0" stopColor="var(--act)" stopOpacity="0.55" />
        <stop offset="0.6" stopColor="#1b1f31" />
        <stop offset="1" stopColor="#141725" />
      </radialGradient>
      <filter id="ledglow" x="-200%" y="-200%" width="500%" height="500%">
        <feGaussianBlur stdDeviation="1.6" result="b" />
        <feMerge><feMergeNode in="b" /><feMergeNode in="SourceGraphic" /></feMerge>
      </filter>
      <filter id="hwshadow" x="-30%" y="-30%" width="160%" height="180%">
        <feDropShadow dx="0" dy="3" stdDeviation="3" floodColor="#000" floodOpacity="0.45" />
      </filter>
    </>
  );
}

export const DEVICE_CSS = `
.led-act{animation:ledblink 1.3s steps(2,jump-none) infinite}
.led-crit{animation:ledcrit .8s ease-in-out infinite}
.fan{animation:fanspin 1.4s linear infinite;transform-box:fill-box;transform-origin:center}
.die{animation:diepulse 3.2s ease-in-out infinite}
.alarm{animation:alarm 1.6s ease-in-out infinite}
.sel{animation:selmarch 1s linear infinite}
.scr{stroke-dasharray:4 3;animation:scr 1.6s linear infinite}
@keyframes ledblink{0%{opacity:1}50%{opacity:.15}100%{opacity:1}}
@keyframes ledcrit{0%,100%{opacity:1}50%{opacity:.25}}
@keyframes fanspin{to{transform:rotate(360deg)}}
@keyframes diepulse{0%,100%{opacity:.75}50%{opacity:1}}
@keyframes alarm{0%,100%{stroke-opacity:.15}50%{stroke-opacity:.8}}
@keyframes selmarch{to{stroke-dashoffset:-12}}
@keyframes scr{to{stroke-dashoffset:-14}}
@media (prefers-reduced-motion:reduce){.led-act,.led-crit,.fan,.die,.alarm,.sel,.scr{animation:none}}
`;

// Stable pseudo-random blink delays so LEDs do not blink in sync.
function delay(seed: string, i: number) {
  let h = 2166136261;
  for (let c = 0; c < seed.length; c++) h = Math.imul(h ^ seed.charCodeAt(c), 16777619);
  h = Math.imul(h ^ (i + 1) * 2654435761, 16777619);
  return `${-((h >>> 0) % 1300) / 1000}s`;
}

function Act({ x, y, w = 1.6, h = 1.6, id, i, round }: { x: number; y: number; w?: number; h?: number; id: string; i: number; round?: boolean }) {
  const style = { animationDelay: delay(id, i), animationDuration: `${0.7 + ((i * 37) % 9) / 10}s` };
  return round
    ? <circle cx={x} cy={y} r={w / 2} fill="var(--act)" className="led-act" style={style} filter="url(#ledglow)" />
    : <rect x={x} y={y} width={w} height={h} rx={0.4} fill="var(--act)" className="led-act" style={style} filter="url(#ledglow)" />;
}

function Power({ x, y, status, r = 2.1 }: { x: number; y: number; status: string; r?: number }) {
  const c = LED[status] ?? LED.unknown;
  return <circle cx={x} cy={y} r={r} fill={c} filter={status === "offline" ? undefined : "url(#ledglow)"} className={status === "critical" ? "led-crit" : undefined} />;
}

function Server({ id, status }: { id: string; status: string }) {
  return (
    <g>
      <rect x={-50} y={-19} width={5} height={38} rx={1} fill="url(#hw-metal)" />
      <rect x={45} y={-19} width={5} height={38} rx={1} fill="url(#hw-metal)" />
      {[-12, 12].map((y) => <g key={y}><circle cx={-47.5} cy={y} r={1.1} fill="#1a1f29" /><circle cx={47.5} cy={y} r={1.1} fill="#1a1f29" /></g>)}
      <rect x={-45} y={-21} width={90} height={42} rx={3} fill="url(#hw-body)" stroke="#3d485c" strokeWidth={0.8} />
      <line x1={-44} y1={-20.2} x2={44} y2={-20.2} stroke="#fff" strokeOpacity={0.14} strokeWidth={0.6} />
      <line x1={-44} y1={0} x2={44} y2={0} stroke="#0a0d13" strokeWidth={0.8} />
      {[-16, 3].map((y, r) =>
        [0, 1, 2, 3].map((i) => (
          <g key={`${r}${i}`}>
            <rect x={-41 + i * 12.5} y={y} width={11.5} height={13} rx={1} fill="url(#hw-face)" stroke="#2d3647" strokeWidth={0.5} />
            <line x1={-39.5 + i * 12.5} y1={y + 3} x2={-32 + i * 12.5} y2={y + 3} stroke="#3a4558" strokeWidth={0.6} />
            <line x1={-39.5 + i * 12.5} y1={y + 5} x2={-32 + i * 12.5} y2={y + 5} stroke="#3a4558" strokeWidth={0.6} />
            <Act x={-38.5 + i * 12.5} y={y + 9.2} w={2} h={1.4} id={id} i={r * 4 + i} />
          </g>
        )),
      )}
      {Array.from({ length: 9 }, (_, i) => <line key={i} x1={10 + i * 2.6} y1={-16} x2={10 + i * 2.6} y2={16} stroke="#090c12" strokeWidth={1.3} />)}
      <rect x={34} y={-17} width={8} height={34} rx={1.5} fill="#0c0f16" stroke="#2d3647" strokeWidth={0.5} />
      <Power x={38} y={-11} status={status} />
      <Act x={38} y={-4} w={2.2} id={id} i={9} round />
      <Act x={38} y={2} w={2.2} id={id} i={10} round />
      <rect x={35.5} y={9} width={5} height={5} rx={1} fill="none" stroke="#3d485c" strokeWidth={0.6} />
    </g>
  );
}

function Desktop({ id, status }: { id: string; status: string }) {
  return (
    <g>
      <rect x={-33} y={-26} width={66} height={42} rx={3.5} fill="url(#hw-body)" stroke="#3d485c" strokeWidth={0.8} />
      <rect x={-30} y={-23} width={60} height={34} rx={1.5} fill="url(#hw-screen)" />
      <polyline points="-26,6 -18,0 -11,3 -4,-8 3,-2 10,-12 17,-6 26,-14" fill="none" stroke="var(--act)" strokeWidth={1.1} strokeOpacity={0.9} className="scr" filter="url(#ledglow)" />
      {[0, 1, 2].map((i) => <rect key={i} x={-26 + i * 5} y={-19} width={3.5} height={2} rx={0.5} fill="var(--act)" opacity={0.35 + i * 0.2} />)}
      <rect x={-5} y={16} width={10} height={6} fill="url(#hw-metal)" />
      <rect x={-16} y={22} width={32} height={3.5} rx={1.75} fill="url(#hw-metal)" />
      <Power x={28} y={13.5} status={status} r={1.3} />
      <Act x={-28} y={13.5} w={1.6} id={id} i={0} round />
    </g>
  );
}

function Chip({ id, status }: { id: string; status: string }) {
  const pins = [];
  for (let i = 0; i < 7; i++) {
    const p = -18 + i * 6;
    pins.push(<rect key={`t${i}`} x={p - 1.2} y={-31} width={2.4} height={6} rx={0.6} fill="url(#hw-gold)" />);
    pins.push(<rect key={`b${i}`} x={p - 1.2} y={25} width={2.4} height={6} rx={0.6} fill="url(#hw-gold)" />);
    pins.push(<rect key={`l${i}`} x={-31} y={p - 1.2} width={6} height={2.4} rx={0.6} fill="url(#hw-gold)" />);
    pins.push(<rect key={`r${i}`} x={25} y={p - 1.2} width={6} height={2.4} rx={0.6} fill="url(#hw-gold)" />);
  }
  return (
    <g>
      {pins}
      <rect x={-26} y={-26} width={52} height={52} rx={4} fill="url(#hw-body)" stroke="#414c61" strokeWidth={0.8} />
      <rect x={-21} y={-21} width={42} height={42} rx={2.5} fill="none" stroke="#2c3445" strokeWidth={0.6} />
      <rect x={-14} y={-14} width={28} height={28} rx={2} fill="url(#hw-die)" stroke="var(--act)" strokeOpacity={0.8} strokeWidth={0.8} className="die" filter="url(#ledglow)" />
      {[-9, -3, 3, 9].map((v) => <g key={v} stroke="var(--act)" strokeOpacity={0.18} strokeWidth={0.5}><line x1={v} y1={-13} x2={v} y2={13} /><line x1={-13} y1={v} x2={13} y2={v} /></g>)}
      <path d="M0 -7 L1.8 -1.8 L7 0 L1.8 1.8 L0 7 L-1.8 1.8 L-7 0 L-1.8 -1.8 Z" fill="#efeaff" opacity={0.95} filter="url(#ledglow)" />
      <circle cx={-19.5} cy={-19.5} r={1.6} fill="#0b0e14" />
      <Power x={19} y={19} status={status} r={1.6} />
      <Act x={19} y={-19} w={1.6} id={id} i={1} round />
    </g>
  );
}

function Fan({ cx }: { cx: number }) {
  return (
    <g transform={`translate(${cx},-2)`}>
      <circle r={11} fill="#0a0d13" stroke="#323c4f" strokeWidth={0.8} />
      <g className="fan">
        <circle r={10} fill="none" />
        {[0, 72, 144, 216, 288].map((a) => <ellipse key={a} cx={0} cy={-5.5} rx={2.6} ry={5.2} fill="#2a3346" transform={`rotate(${a}) rotate(18 0 -5.5)`} />)}
      </g>
      <circle r={2.8} fill="url(#hw-metal)" />
    </g>
  );
}

function GPU({ id, status }: { id: string; status: string }) {
  return (
    <g>
      <rect x={-46} y={-18} width={4} height={36} rx={0.8} fill="url(#hw-metal)" />
      <rect x={-42} y={-16} width={86} height={28} rx={3} fill="url(#hw-body)" stroke="#3d485c" strokeWidth={0.8} />
      <line x1={-40} y1={-15} x2={42} y2={-15} stroke="var(--act)" strokeOpacity={0.75} strokeWidth={0.9} filter="url(#ledglow)" />
      <Fan cx={-16} />
      <Fan cx={16} />
      {Array.from({ length: 11 }, (_, i) => <rect key={i} x={-30 + i * 4.6} y={12} width={3.4} height={4} fill="url(#hw-gold)" />)}
      <Power x={38} y={-9} status={status} r={1.7} />
      <Act x={38} y={-3} w={1.6} id={id} i={2} round />
    </g>
  );
}

function Crate({ x, y, w, h }: { x: number; y: number; w: number; h: number }) {
  return (
    <g>
      <rect x={x} y={y} width={w} height={h} rx={1.2} fill="url(#hw-crate)" stroke="#3f6288" strokeWidth={0.7} />
      {Array.from({ length: Math.floor((w - 4) / 3) }, (_, i) => <line key={i} x1={x + 3 + i * 3} y1={y + 2} x2={x + 3 + i * 3} y2={y + h - 2} stroke="#fff" strokeOpacity={0.08} strokeWidth={0.8} />)}
      <line x1={x} y1={y + 1.5} x2={x + w} y2={y + 1.5} stroke="#fff" strokeOpacity={0.12} strokeWidth={0.5} />
    </g>
  );
}

function Docker({ id, status }: { id: string; status: string }) {
  return (
    <g>
      <Crate x={-34} y={3} w={33} h={16} />
      <Crate x={1} y={3} w={33} h={16} />
      <Crate x={-17} y={-15} w={33} h={16} />
      <rect x={-35} y={19} width={70} height={3} rx={1} fill="url(#hw-metal)" />
      <Power x={28} y={-11} status={status} r={1.8} />
      <Act x={28} y={-5} w={1.6} id={id} i={3} round />
    </g>
  );
}

function Blade({ id, status }: { id: string; status: string }) {
  return (
    <g>
      <rect x={-37} y={-12} width={74} height={24} rx={3} fill="url(#hw-body)" stroke="#3d485c" strokeWidth={0.8} />
      <rect x={-34} y={-8} width={3} height={16} rx={1} fill="url(#hw-metal)" />
      <rect x={-27} y={-6} width={40} height={12} rx={1} fill="url(#hw-screen)" />
      {[0, 1, 2].map((i) => <line key={i} x1={-25} y1={-3 + i * 3} x2={-25 + 8 + ((i * 13 + id.length * 7) % 26)} y2={-3 + i * 3} stroke="var(--act)" strokeOpacity={0.55} strokeWidth={0.8} />)}
      <Power x={25} y={-4} status={status} r={1.7} />
      <Act x={25} y={3.5} w={1.6} id={id} i={4} round />
      <Act x={31} y={3.5} w={1.6} id={id} i={5} round />
    </g>
  );
}

function Switch({ id, status }: { id: string; status: string }) {
  return (
    <g>
      <rect x={-46} y={-14} width={92} height={28} rx={3} fill="url(#hw-body)" stroke="#3d485c" strokeWidth={0.8} />
      <line x1={-45} y1={-13.2} x2={45} y2={-13.2} stroke="#fff" strokeOpacity={0.14} strokeWidth={0.6} />
      {Array.from({ length: 8 }, (_, i) => (
        <g key={i}>
          <Act x={-28 + i * 8.6} y={-8} w={2.6} h={1.4} id={id} i={i} />
          <rect x={-30 + i * 8.6} y={-4.5} width={7} height={7} rx={0.8} fill="#06080c" stroke="#323c4f" strokeWidth={0.6} />
          <rect x={-28.5 + i * 8.6} y={-3} width={4} height={2} fill="#1d2433" />
        </g>
      ))}
      <Power x={-39} y={-5} status={status} r={1.8} />
      <rect x={-41.5} y={2} width={5} height={6} rx={1} fill="#06080c" stroke="#323c4f" strokeWidth={0.5} />
      <rect x={40} y={-8} width={2} height={16} rx={1} fill="#0a0d13" />
    </g>
  );
}

function Storage({ id, status }: { id: string; status: string }) {
  const tiers = [-18, -6, 6];
  return (
    <g>
      {tiers.map((y, i) => (
        <g key={y}>
          <path d={`M-21 ${y} L-21 ${y + 12} A21 6 0 0 0 21 ${y + 12} L21 ${y} Z`} fill="url(#hw-disk)" stroke="#3d485c" strokeWidth={0.7} />
          <ellipse cx={0} cy={y} rx={21} ry={6} fill="#262e3f" stroke="#4a566d" strokeWidth={0.7} />
          <Act x={14} y={y + 9} w={1.8} id={id} i={6 + i} round />
        </g>
      ))}
      <ellipse cx={0} cy={-18} rx={8} ry={2.2} fill="none" stroke="var(--act)" strokeOpacity={0.45} strokeWidth={0.7} />
      <Power x={-14} y={15} status={status} r={1.7} />
    </g>
  );
}

function Container({ id, status }: { id: string; status: string }) {
  return (
    <g>
      <Crate x={-24} y={-12} w={48} h={24} />
      <rect x={13} y={-10} width={9} height={20} fill="none" stroke="#4d73a0" strokeWidth={0.6} />
      <line x1={17.5} y1={-10} x2={17.5} y2={10} stroke="#4d73a0" strokeWidth={0.6} />
      <line x1={15} y1={-3} x2={15} y2={3} stroke="url(#hw-metal)" strokeWidth={1} />
      <line x1={20} y1={-3} x2={20} y2={3} stroke="url(#hw-metal)" strokeWidth={1} />
      <Power x={-18} y={-6} status={status} r={1.5} />
      <Act x={-18} y={0} w={1.4} id={id} i={7} round />
    </g>
  );
}

/** One topology node drawn as hardware. */
export function Device({ id, kind, status, selected }: { id: string; kind: string; status: string; selected?: boolean }) {
  const d = deviceOf(kind);
  const body = (() => {
    switch (kind) {
      case "server": return <Server id={id} status={status} />;
      case "desktop": return <Desktop id={id} status={status} />;
      case "core": return <Chip id={id} status={status} />;
      case "ollama": return <GPU id={id} status={status} />;
      case "docker": return <Docker id={id} status={status} />;
      case "service": return <Blade id={id} status={status} />;
      case "proxy": return <Switch id={id} status={status} />;
      case "database": return <Storage id={id} status={status} />;
      default: return <Container id={id} status={status} />;
    }
  })();
  const off = status === "offline";
  const alarm = status === "critical" || status === "warning";
  return (
    <g>
      {/* hit area */}
      <rect x={-d.w / 2 - 4} y={-d.h / 2 - 4} width={d.w + 8} height={d.h + 8} fill="transparent" />
      {alarm && <rect x={-d.w / 2 - 5} y={-d.h / 2 - 5} width={d.w + 10} height={d.h + 10} rx={6} fill="none" stroke={LED[status]} strokeWidth={1.2} className="alarm" filter="url(#ledglow)" />}
      {selected && <rect x={-d.w / 2 - 8} y={-d.h / 2 - 8} width={d.w + 16} height={d.h + 16} rx={7} fill="none" stroke="var(--accent)" strokeWidth={1.2} strokeDasharray="6 6" className="sel" />}
      <g filter="url(#hwshadow)" style={off ? { opacity: 0.45, filter: "grayscale(1)" } : undefined}>{body}</g>
    </g>
  );
}

export { LED as LED_COLOR };
