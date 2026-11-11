import React, { useEffect, useMemo, useRef, useState } from "react";

function cssVar(el: Element, name: string) {
  return getComputedStyle(el).getPropertyValue(name).trim() || "#888";
}

export type Series = {
  label: string;
  color: string; // css var name e.g. --c1 or literal
  values: (number | null)[];
  fill?: boolean;
  dashed?: boolean;
};

type Props = {
  times: number[];
  series: Series[];
  height?: number;
  yMax?: number; // fixed max (e.g. 100 for percent)
  format?: (v: number) => string;
  thresholds?: { value: number; color: string; label?: string }[];
  emptyText?: string;
};

const PAD = { l: 44, r: 12, t: 10, b: 22 };

function niceMax(v: number) {
  if (v <= 0) return 1;
  const p = Math.pow(10, Math.floor(Math.log10(v)));
  for (const m of [1, 1.2, 1.5, 2, 2.5, 3, 4, 5, 6, 8, 10]) if (m * p >= v) return m * p;
  return 10 * p;
}

/**
 * Canvas time-series chart: crisp at any DPI, animated transitions between
 * data updates, crosshair tooltip. Gaps (null) break the line rather than
 * being interpolated, so missing telemetry is never drawn as data.
 */
export function TimeSeriesChart({ times, series, height = 180, yMax, format = (v) => v.toFixed(0), thresholds, emptyText = "No telemetry recorded in this range yet" }: Props) {
  const wrap = useRef<HTMLDivElement>(null);
  const canvas = useRef<HTMLCanvasElement>(null);
  const [w, setW] = useState(600);
  const [hover, setHover] = useState<number | null>(null);
  const anim = useRef({ from: 0, to: 0, start: 0 });
  const padRef = useRef(PAD.l);
  const [, force] = useState(0);

  const max = useMemo(() => {
    if (yMax) return yMax;
    let m = 0;
    for (const s of series) for (const v of s.values) if (v !== null && v > m) m = v;
    for (const t of thresholds ?? []) m = Math.max(m, t.value);
    return niceMax(m * 1.1);
  }, [series, yMax, thresholds]);

  useEffect(() => {
    const el = wrap.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setW(el.clientWidth));
    ro.observe(el);
    setW(el.clientWidth);
    return () => ro.disconnect();
  }, []);

  // Animate the y-scale smoothly when the data range changes.
  useEffect(() => {
    const a = anim.current;
    a.from = a.to || max;
    a.to = max;
    a.start = performance.now();
    let raf = 0;
    const tick = () => {
      force((x) => x + 1);
      if (performance.now() - a.start < 400) raf = requestAnimationFrame(tick);
    };
    raf = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(raf);
  }, [max]);

  const hasData = times.length > 1 && series.some((s) => s.values.some((v) => v !== null));

  useEffect(() => {
    const c = canvas.current;
    if (!c || !wrap.current) return;
    const dpr = window.devicePixelRatio || 1;
    c.width = Math.floor(w * dpr);
    c.height = Math.floor(height * dpr);
    c.style.width = w + "px";
    c.style.height = height + "px";
    const g = c.getContext("2d")!;
    g.setTransform(dpr, 0, 0, dpr, 0, 0);
    g.clearRect(0, 0, w, height);
    const root = wrap.current;
    const line = cssVar(root, "--line");
    const text3 = cssVar(root, "--text-3");
    const a = anim.current;
    const k = Math.min(1, (performance.now() - a.start) / 400);
    const e = 1 - Math.pow(1 - k, 3);
    const ymax = a.from + (a.to - a.from) * e || max;
    g.font = `10.5px ${cssVar(root, "--font-mono")}`;
    let lw = 0;
    for (let i = 0; i <= 4; i++) lw = Math.max(lw, g.measureText(format((ymax / 4) * i)).width);
    const padL = Math.max(PAD.l, Math.ceil(lw) + 14);
    padRef.current = padL;
    const iw = w - padL - PAD.r;
    const ih = height - PAD.t - PAD.b;
    const t0 = times[0] ?? 0;
    const t1 = times[times.length - 1] ?? 1;
    const x = (t: number) => padL + ((t - t0) / Math.max(1, t1 - t0)) * iw;
    const y = (v: number) => PAD.t + ih - (Math.min(v, ymax) / ymax) * ih;

    g.fillStyle = text3;
    g.strokeStyle = line;
    g.lineWidth = 1;
    g.textAlign = "right";
    g.textBaseline = "middle";
    for (let i = 0; i <= 4; i++) {
      const v = (ymax / 4) * i;
      const yy = Math.round(y(v)) + 0.5;
      g.beginPath();
      g.moveTo(padL, yy);
      g.lineTo(w - PAD.r, yy);
      g.stroke();
      g.fillText(format(v), padL - 8, yy);
    }
    if (!hasData) return;
    // time labels
    g.textAlign = "center";
    g.textBaseline = "top";
    const span = t1 - t0;
    const n = Math.max(2, Math.floor(iw / 110));
    for (let i = 0; i <= n; i++) {
      const t = t0 + (span / n) * i;
      const d = new Date(t);
      const lbl = span > 2 * 86400e3 ? d.toLocaleDateString(undefined, { month: "short", day: "2-digit" }) : d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
      g.fillText(lbl, x(t), height - PAD.b + 6);
    }
    for (const th of thresholds ?? []) {
      g.save();
      g.setLineDash([3, 4]);
      g.strokeStyle = th.color.startsWith("--") ? cssVar(root, th.color) : th.color;
      g.globalAlpha = 0.55;
      g.beginPath();
      g.moveTo(padL, Math.round(y(th.value)) + 0.5);
      g.lineTo(w - PAD.r, Math.round(y(th.value)) + 0.5);
      g.stroke();
      g.restore();
    }
    // A gap larger than 3× the median spacing breaks the line.
    const spacing = times.slice(1).map((t, i) => t - times[i]).sort((p, q) => p - q);
    const gap = (spacing[Math.floor(spacing.length / 2)] || 10000) * 3.5;
    for (const s of series) {
      const col = s.color.startsWith("--") ? cssVar(root, s.color) : s.color;
      const segs: [number, number][][] = [];
      let cur: [number, number][] = [];
      for (let i = 0; i < times.length; i++) {
        const v = s.values[i];
        if (v === null || v === undefined || (i > 0 && times[i] - times[i - 1] > gap)) {
          if (cur.length) segs.push(cur);
          cur = [];
          if (v === null || v === undefined) continue;
        }
        cur.push([x(times[i]), y(v)]);
      }
      if (cur.length) segs.push(cur);
      for (const seg of segs) {
        if (s.fill && seg.length > 1) {
          // Works for any CSS colour syntax (hex, hsl(), rgb()): fade via globalAlpha.
          const grad = g.createLinearGradient(0, PAD.t, 0, PAD.t + ih);
          grad.addColorStop(0, col);
          grad.addColorStop(1, "rgba(0,0,0,0)");
          g.save();
          g.globalAlpha = 0.28;
          g.fillStyle = grad;
          g.beginPath();
          g.moveTo(seg[0][0], PAD.t + ih);
          for (const [px, py] of seg) g.lineTo(px, py);
          g.lineTo(seg[seg.length - 1][0], PAD.t + ih);
          g.closePath();
          g.fill();
          g.restore();
        }
        g.strokeStyle = col;
        g.lineWidth = 1.6;
        g.lineJoin = "round";
        g.setLineDash(s.dashed ? [4, 3] : []);
        g.beginPath();
        seg.forEach(([px, py], i) => (i ? g.lineTo(px, py) : g.moveTo(px, py)));
        g.stroke();
        if (seg.length === 1) {
          g.fillStyle = col;
          g.beginPath();
          g.arc(seg[0][0], seg[0][1], 2, 0, Math.PI * 2);
          g.fill();
        }
      }
    }
    g.setLineDash([]);
    if (hover !== null && times[hover] !== undefined) {
      const hx = Math.round(x(times[hover])) + 0.5;
      g.strokeStyle = cssVar(root, "--line-3");
      g.beginPath();
      g.moveTo(hx, PAD.t);
      g.lineTo(hx, PAD.t + ih);
      g.stroke();
      for (const s of series) {
        const v = s.values[hover];
        if (v === null || v === undefined) continue;
        g.fillStyle = s.color.startsWith("--") ? cssVar(root, s.color) : s.color;
        g.beginPath();
        g.arc(hx, y(v), 3.2, 0, Math.PI * 2);
        g.fill();
      }
    }
  });

  const onMove = (ev: React.MouseEvent) => {
    if (!hasData) return;
    const r = (ev.currentTarget as HTMLElement).getBoundingClientRect();
    const px = ev.clientX - r.left;
    const t0 = times[0];
    const t1 = times[times.length - 1];
    const t = t0 + ((px - padRef.current) / (w - padRef.current - PAD.r)) * (t1 - t0);
    let best = 0;
    for (let i = 1; i < times.length; i++) if (Math.abs(times[i] - t) < Math.abs(times[best] - t)) best = i;
    setHover(best);
  };

  const hx = hover !== null && hasData ? padRef.current + ((times[hover] - times[0]) / Math.max(1, times[times.length - 1] - times[0])) * (w - padRef.current - PAD.r) : 0;
  return (
    <div ref={wrap} style={{ position: "relative", height }} onMouseMove={onMove} onMouseLeave={() => setHover(null)}>
      <canvas ref={canvas} />
      {!hasData && (
        <div className="faint" style={{ position: "absolute", inset: 0, display: "grid", placeItems: "center", fontSize: "var(--fs-sm)" }}>
          {emptyText}
        </div>
      )}
      {hover !== null && hasData && (
        <div className="tip" style={{ position: "absolute", left: Math.min(hx + 12, w - 190), top: 8 }}>
          <div className="faint mono" style={{ marginBottom: 4 }}>
            {new Date(times[hover]).toLocaleString(undefined, { month: "short", day: "2-digit", hour: "2-digit", minute: "2-digit", second: "2-digit" })}
          </div>
          {series.map((s) => (
            <div key={s.label} className="row" style={{ gap: 8 }}>
              <i style={{ width: 8, height: 2, background: s.color.startsWith("--") ? `var(${s.color})` : s.color, display: "inline-block" }} />
              <span className="dim">{s.label}</span>
              <span className="spacer" />
              <b className="num mono">{s.values[hover] === null || s.values[hover] === undefined ? "—" : format(s.values[hover]!)}</b>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

export function Legend({ series }: { series: { label: string; color: string }[] }) {
  return (
    <div className="row" style={{ gap: 14 }}>
      {series.map((s) => (
        <span key={s.label} className="row faint" style={{ gap: 6, fontSize: "var(--fs-xs)" }}>
          <i style={{ width: 10, height: 2, borderRadius: 1, background: `var(${s.color})`, display: "inline-block" }} />
          {s.label}
        </span>
      ))}
    </div>
  );
}

/** Small inline trend line. Missing values break the line. */
export function Sparkline({ values, color = "--accent", height = 28, width = 120, max, fill = true }: { values: (number | null)[]; color?: string; height?: number; width?: number; max?: number; fill?: boolean }) {
  const id = useMemo(() => "sg" + Math.random().toString(36).slice(2), []);
  const vals = values.filter((v): v is number => v !== null && v !== undefined);
  if (vals.length < 2) return <svg width={width} height={height} />;
  const m = max ?? Math.max(...vals, 1e-9) * 1.15;
  const step = width / Math.max(1, values.length - 1);
  let d = "";
  let pen = false;
  values.forEach((v, i) => {
    if (v === null || v === undefined) {
      pen = false;
      return;
    }
    const x = i * step;
    const y = height - 2 - (Math.min(v, m) / m) * (height - 4);
    d += `${pen ? "L" : "M"}${x.toFixed(1)},${y.toFixed(1)}`;
    pen = true;
  });
  return (
    <svg width={width} height={height} style={{ display: "block", overflow: "visible" }}>
      <defs>
        <linearGradient id={id} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0" stopColor={`var(${color})`} stopOpacity="0.28" />
          <stop offset="1" stopColor={`var(${color})`} stopOpacity="0" />
        </linearGradient>
      </defs>
      {fill && <path d={`${d}L${width},${height}L0,${height}Z`} fill={`url(#${id})`} stroke="none" />}
      <path d={d} fill="none" stroke={`var(${color})`} strokeWidth="1.4" strokeLinejoin="round" />
    </svg>
  );
}

/** Ring gauge for a 0–100 value. */
export function Ring({ value, size = 56, stroke = 5, label, cls }: { value: number | null; size?: number; stroke?: number; label?: string; cls?: string }) {
  const r = (size - stroke) / 2;
  const c = 2 * Math.PI * r;
  const v = value === null ? 0 : Math.max(0, Math.min(100, value));
  const color = cls === "crit" ? "var(--crit)" : cls === "warn" ? "var(--warn)" : cls === "ok" ? "var(--ok)" : "var(--accent)";
  return (
    <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`}>
      <circle cx={size / 2} cy={size / 2} r={r} fill="none" stroke="var(--surface-3)" strokeWidth={stroke} />
      <circle
        cx={size / 2}
        cy={size / 2}
        r={r}
        fill="none"
        stroke={color}
        strokeWidth={stroke}
        strokeLinecap="round"
        strokeDasharray={`${(v / 100) * c} ${c}`}
        transform={`rotate(-90 ${size / 2} ${size / 2})`}
        style={{ transition: "stroke-dasharray 600ms var(--ease)" }}
      />
      <text x="50%" y="50%" dominantBaseline="central" textAnchor="middle" fill="var(--text)" style={{ font: `600 ${size * 0.26}px var(--font-display)` }}>
        {value === null ? "—" : Math.round(value)}
      </text>
      {label && (
        <text x="50%" y={size - 2} textAnchor="middle" fill="var(--text-3)" style={{ font: `500 9px var(--font-ui)` }}>
          {label}
        </text>
      )}
    </svg>
  );
}

/** Per-core heat strip. */
export function CoreStrip({ cores }: { cores: number[] | undefined }) {
  if (!cores || cores.length === 0) return null;
  return (
    <div style={{ display: "grid", gridTemplateColumns: `repeat(${Math.min(cores.length, 32)}, 1fr)`, gap: 3 }}>
      {cores.map((c, i) => (
        <div
          key={i}
          title={`CPU ${i}: ${c.toFixed(1)}%`}
          style={{
            height: 18,
            borderRadius: 2,
            background: c >= 90 ? "var(--crit)" : c >= 70 ? "var(--warn)" : "var(--accent)",
            opacity: 0.18 + (c / 100) * 0.82,
            transition: "opacity 400ms",
          }}
        />
      ))}
    </div>
  );
}
