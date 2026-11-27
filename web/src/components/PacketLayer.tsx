import { useEffect, useRef } from "react";

// Animated data flow for the topology, drawn on a canvas under the SVG
// devices. Several animation themes share one engine: links, packets that
// travel along them, and an effect where each packet arrives. The animation
// shows the direction of flow on real links, not measured traffic.

export type FlowTheme = "plasma" | "fiber" | "circuit" | "matrix" | "calm" | "off";
export type FlowNode = { x: number; y: number; kind: string; status: string };
export type FlowEdge = { from: string; to: string; kind: string };
type View = { x: number; y: number; k: number };

export const FLOW_THEMES: { id: FlowTheme; label: string; desc: string; act: string }[] = [
  { id: "plasma", label: "Plasma", desc: "Thin deep-blue and violet streaks that end in small lightning bursts.", act: "#8b5cf6" },
  { id: "fiber", label: "Fiber optic", desc: "Pulses of cyan light travelling through glowing cables.", act: "#22d3ee" },
  { id: "circuit", label: "Circuit board", desc: "Gold signals running along right-angled PCB traces, sparking at each component.", act: "#f5b544" },
  { id: "matrix", label: "Matrix", desc: "Streams of green binary flowing between devices.", act: "#22c55e" },
  { id: "calm", label: "Calm", desc: "Slow, soft dots without glow, for a quiet screen.", act: "#94a3b8" },
  { id: "off", label: "Off", desc: "Static links, no animation.", act: "#64748b" },
];

type Pt = [number, number];
type Path = { pts: Pt[]; cum: number[]; len: number };
type Packet = { key: string; d: number; speed: number; len: number; hue: number; w: number; glyphs: string };
type Burst = { kind: "lightning" | "ripple" | "spark" | "scatter"; x: number; y: number; ux: number; uy: number; born: number; life: number; hue: number; bolts?: Pt[][]; glyphs?: string };
type Arc = { pts: Pt[]; born: number; life: number };

const MAX_PACKETS = 420;

function jag(x0: number, y0: number, x1: number, y1: number, rough: number, depth: number): Pt[] {
  let pts: Pt[] = [[x0, y0], [x1, y1]];
  let r = rough;
  for (let d = 0; d < depth; d++) {
    const next: Pt[] = [pts[0]];
    for (let i = 1; i < pts.length; i++) {
      const [ax, ay] = pts[i - 1], [bx, by] = pts[i];
      const dx = bx - ax, dy = by - ay;
      const len = Math.hypot(dx, dy) || 1;
      const off = (Math.random() - 0.5) * r;
      next.push([(ax + bx) / 2 - (dy / len) * off, (ay + by) / 2 + (dx / len) * off], pts[i]);
    }
    pts = next;
    r /= 2;
  }
  return pts;
}

/** Distance from a box centre to its edge along (ux, uy). */
function boxExit(ux: number, uy: number, hw: number, hh: number) {
  const a = Math.abs(ux) > 1e-6 ? hw / Math.abs(ux) : Infinity;
  const b = Math.abs(uy) > 1e-6 ? hh / Math.abs(uy) : Infinity;
  return Math.min(a, b) + 4;
}

function mkPath(pts: Pt[]): Path {
  const cum = [0];
  for (let i = 1; i < pts.length; i++) cum.push(cum[i - 1] + Math.hypot(pts[i][0] - pts[i - 1][0], pts[i][1] - pts[i - 1][1]));
  return { pts, cum, len: cum[cum.length - 1] };
}

function at(p: Path, d: number): [number, number, number, number] {
  d = Math.max(0, Math.min(p.len, d));
  let i = 1;
  while (i < p.cum.length - 1 && p.cum[i] < d) i++;
  const [ax, ay] = p.pts[i - 1], [bx, by] = p.pts[i];
  const seg = p.cum[i] - p.cum[i - 1] || 1;
  const f = (d - p.cum[i - 1]) / seg;
  return [ax + (bx - ax) * f, ay + (by - ay) * f, (bx - ax) / seg, (by - ay) / seg];
}

/** The drawn route of a link, trimmed to the device outlines. */
function route(s: FlowNode, t: FlowNode, ext: (k: string) => [number, number], ortho: boolean): Path | null {
  let pts: Pt[];
  if (ortho) {
    if (Math.abs(t.y - s.y) > 30) {
      const my = (s.y + t.y) / 2;
      pts = [[s.x, s.y], [s.x, my], [t.x, my], [t.x, t.y]];
    } else {
      const mx = (s.x + t.x) / 2;
      pts = [[s.x, s.y], [mx, s.y], [mx, t.y], [t.x, t.y]];
    }
    pts = pts.filter((p, i) => i === 0 || Math.hypot(p[0] - pts[i - 1][0], p[1] - pts[i - 1][1]) > 0.5);
    if (pts.length < 2) pts = [[s.x, s.y], [t.x, t.y]];
  } else pts = [[s.x, s.y], [t.x, t.y]];
  const trim = (a: Pt, b: Pt, kind: string): Pt => {
    const dx = b[0] - a[0], dy = b[1] - a[1];
    const len = Math.hypot(dx, dy) || 1;
    const [hw, hh] = ext(kind);
    const e = Math.min(boxExit(dx / len, dy / len, hw, hh), len * 0.95);
    return [a[0] + (dx / len) * e, a[1] + (dy / len) * e];
  };
  const n = pts.length;
  const first = trim(pts[0], pts[1], s.kind);
  const last = trim(pts[n - 1], pts[n - 2], t.kind);
  pts = [first, ...pts.slice(1, n - 1), last];
  const p = mkPath(pts);
  return p.len > 4 ? p : null;
}

const spriteCache = new Map<string, HTMLCanvasElement>();
function sprite(h: number, s: number, dark: boolean) {
  const key = `${Math.round(h / 5)}:${s}:${dark}`;
  let c = spriteCache.get(key);
  if (c) return c;
  c = document.createElement("canvas");
  c.width = c.height = 64;
  const g = c.getContext("2d")!;
  const grd = g.createRadialGradient(32, 32, 0, 32, 32, 32);
  if (dark) {
    grd.addColorStop(0, `hsla(${h},${s}%,96%,1)`);
    grd.addColorStop(0.12, `hsla(${h},${s}%,80%,0.85)`);
    grd.addColorStop(0.35, `hsla(${h},${s}%,60%,0.28)`);
    grd.addColorStop(1, `hsla(${h},${s}%,45%,0)`);
  } else {
    grd.addColorStop(0, `hsla(${h},${s}%,30%,1)`);
    grd.addColorStop(0.2, `hsla(${h},${s}%,38%,0.6)`);
    grd.addColorStop(1, `hsla(${h},${s}%,45%,0)`);
  }
  g.fillStyle = grd;
  g.fillRect(0, 0, 64, 64);
  spriteCache.set(key, c);
  return c;
}

// Per-theme parameters.
const CFG: Record<FlowTheme, { ortho: boolean; rate: number; speed: [number, number]; hue: () => number; sat: number; packet: "streak" | "glyph" | "dot" | null; len: [number, number]; arrive: Burst["kind"] | null; arcs: boolean }> = {
  plasma: { ortho: false, rate: 1, speed: [170, 110], hue: () => 232 + Math.random() * 43, sat: 95, packet: "streak", len: [22, 12], arrive: "lightning", arcs: true },
  fiber: { ortho: false, rate: 0.85, speed: [230, 160], hue: () => 182 + Math.random() * 14, sat: 95, packet: "streak", len: [46, 18], arrive: "ripple", arcs: false },
  circuit: { ortho: true, rate: 0.9, speed: [120, 90], hue: () => 36 + Math.random() * 10, sat: 92, packet: "streak", len: [12, 6], arrive: "spark", arcs: false },
  matrix: { ortho: false, rate: 0.75, speed: [70, 55], hue: () => 125 + Math.random() * 20, sat: 85, packet: "glyph", len: [0, 0], arrive: "scatter", arcs: false },
  calm: { ortho: false, rate: 0.35, speed: [55, 40], hue: () => 215, sat: 25, packet: "dot", len: [0, 0], arrive: null, arcs: false },
  off: { ortho: false, rate: 0, speed: [0, 0], hue: () => 215, sat: 15, packet: null, len: [0, 0], arrive: null, arcs: false },
};

const bits = (n: number) => Array.from({ length: n }, () => (Math.random() < 0.5 ? "0" : "1")).join("");

export function PacketLayer({ nodes, edges, view, extent, theme }: { nodes: React.MutableRefObject<Map<string, FlowNode>>; edges: FlowEdge[]; view: React.MutableRefObject<View>; extent: (kind: string) => [number, number]; theme: FlowTheme }) {
  const cv = useRef<HTMLCanvasElement>(null);
  const edgesRef = useRef(edges);
  edgesRef.current = edges;
  const themeRef = useRef(theme);
  themeRef.current = theme;
  const extRef = useRef(extent);
  extRef.current = extent;

  useEffect(() => {
    const canvas = cv.current!;
    const ctx = canvas.getContext("2d")!;
    const reduce = window.matchMedia?.("(prefers-reduced-motion: reduce)").matches ?? false;
    let dark = true;
    const checkTheme = () => {
      const m = getComputedStyle(document.body).backgroundColor.match(/\d+/g);
      const lum = m ? (0.299 * +m[0] + 0.587 * +m[1] + 0.114 * +m[2]) / 255 : 0;
      dark = lum < 0.5;
    };
    checkTheme();
    const themeTimer = setInterval(checkTheme, 1500);

    let packets: Packet[] = [];
    let bursts: Burst[] = [];
    let arcs: Arc[] = [];
    let lastTheme = themeRef.current;
    const spawnAcc = new Map<string, number>();
    let last = performance.now();
    let raf = 0;

    const resize = () => {
      const dpr = Math.min(2, window.devicePixelRatio || 1);
      const w = canvas.clientWidth, h = canvas.clientHeight;
      if (canvas.width !== Math.round(w * dpr) || canvas.height !== Math.round(h * dpr)) {
        canvas.width = Math.round(w * dpr);
        canvas.height = Math.round(h * dpr);
      }
      return dpr;
    };

    const arrive = (kind: Burst["kind"], x: number, y: number, ux: number, uy: number, hue: number, now: number) => {
      const b: Burst = { kind, x, y, ux, uy, born: now, life: 300, hue };
      if (kind === "lightning") {
        b.life = 260 + Math.random() * 140;
        b.bolts = [];
        const base = Math.atan2(uy, ux);
        for (let i = 0, n = 3 + Math.floor(Math.random() * 3); i < n; i++) {
          const ang = base + (Math.random() - 0.5) * 2.6;
          const len = 9 + Math.random() * 13;
          b.bolts.push(jag(x, y, x + Math.cos(ang) * len, y + Math.sin(ang) * len, len * 0.7, 2));
        }
      } else if (kind === "ripple") b.life = 520;
      else if (kind === "spark") b.life = 240;
      else if (kind === "scatter") { b.life = 650; b.glyphs = bits(4); }
      bursts.push(b);
    };

    const frame = (now: number) => {
      const dt = Math.min(0.05, (now - last) / 1000);
      last = now;
      const th = themeRef.current;
      if (th !== lastTheme) { packets = []; bursts = []; arcs = []; spawnAcc.clear(); lastTheme = th; }
      const cfg = CFG[th];
      const dpr = resize();
      const v = view.current;
      const k = v.k;
      const ns = nodes.current;
      const es = edgesRef.current;
      const ext = extRef.current;
      ctx.setTransform(1, 0, 0, 1, 0, 0);
      ctx.clearRect(0, 0, canvas.width, canvas.height);
      ctx.setTransform(dpr * k, 0, 0, dpr * k, dpr * v.x, dpr * v.y);
      ctx.globalCompositeOperation = dark && th !== "calm" && th !== "off" ? "lighter" : "source-over";
      ctx.lineCap = "round";
      ctx.lineJoin = "round";
      const px = 1 / k;
      const L = (l: number) => (dark ? l : Math.max(25, l - 30)); // lightness for the theme background

      // Routes in both directions.
      const paths = new Map<string, Path>();
      const live: { e: FlowEdge; s: FlowNode; t: FlowNode; p: Path }[] = [];
      for (const e of es) {
        const s = ns.get(e.from), t = ns.get(e.to);
        if (!s || !t) continue;
        const p = route(s, t, ext, cfg.ortho);
        if (!p) continue;
        paths.set(e.from + ">" + e.to, p);
        paths.set(e.to + ">" + e.from, mkPath([...p.pts].reverse()));
        live.push({ e, s, t, p });
      }

      // Links.
      for (const { e, s, t, p } of live) {
        const off = s.status === "offline" || t.status === "offline";
        const poly = () => { ctx.beginPath(); ctx.moveTo(p.pts[0][0], p.pts[0][1]); for (const q of p.pts.slice(1)) ctx.lineTo(q[0], q[1]); };
        poly();
        if (off) {
          ctx.setLineDash([3 * px, 4 * px]);
          ctx.strokeStyle = dark ? "rgba(244,63,94,0.45)" : "rgba(190,18,60,0.5)";
          ctx.lineWidth = 0.8 * px;
          ctx.stroke();
          ctx.setLineDash([]);
          continue;
        }
        if (e.kind === "network") ctx.setLineDash([2 * px, 3 * px]);
        switch (th) {
          case "plasma": {
            const [a, b] = [p.pts[0], p.pts[p.pts.length - 1]];
            const g = ctx.createLinearGradient(a[0], a[1], b[0], b[1]);
            g.addColorStop(0, dark ? "rgba(59,76,255,0.16)" : "rgba(67,56,202,0.18)");
            g.addColorStop(1, dark ? "rgba(139,92,246,0.16)" : "rgba(109,40,217,0.18)");
            ctx.strokeStyle = g; ctx.lineWidth = 4 * px; ctx.stroke();
            const h = ctx.createLinearGradient(a[0], a[1], b[0], b[1]);
            h.addColorStop(0, dark ? "rgba(79,96,255,0.55)" : "rgba(67,56,202,0.5)");
            h.addColorStop(1, dark ? "rgba(157,107,255,0.55)" : "rgba(109,40,217,0.5)");
            ctx.strokeStyle = h; ctx.lineWidth = 0.7 * px; ctx.stroke();
            break;
          }
          case "fiber":
            ctx.strokeStyle = `hsla(190,95%,${L(60)}%,0.05)`; ctx.lineWidth = 7 * px; ctx.stroke();
            ctx.strokeStyle = `hsla(190,95%,${L(62)}%,0.12)`; ctx.lineWidth = 2.4 * px; ctx.stroke();
            ctx.strokeStyle = `hsla(188,95%,${L(72)}%,0.55)`; ctx.lineWidth = 0.6 * px; ctx.stroke();
            break;
          case "circuit":
            ctx.strokeStyle = dark ? "rgba(214,160,60,0.10)" : "rgba(146,100,20,0.10)"; ctx.lineWidth = 4 * px; ctx.stroke();
            ctx.strokeStyle = dark ? "rgba(214,160,60,0.55)" : "rgba(146,100,20,0.65)"; ctx.lineWidth = 1.2 * px; ctx.stroke();
            ctx.setLineDash([]);
            for (const q of p.pts.slice(1, -1)) { // vias at the bends
              ctx.beginPath(); ctx.arc(q[0], q[1], 2.2 * px, 0, Math.PI * 2);
              ctx.fillStyle = dark ? "#0b0e14" : "#f3f5f8"; ctx.fill();
              ctx.strokeStyle = dark ? "rgba(232,180,80,0.8)" : "rgba(146,100,20,0.8)"; ctx.lineWidth = 0.9 * px; ctx.stroke();
            }
            for (const q of [p.pts[0], p.pts[p.pts.length - 1]]) { // pads
              ctx.fillStyle = dark ? "rgba(232,180,80,0.7)" : "rgba(146,100,20,0.7)";
              ctx.fillRect(q[0] - 1.6 * px, q[1] - 1.6 * px, 3.2 * px, 3.2 * px);
            }
            break;
          case "matrix":
            ctx.strokeStyle = `hsla(140,70%,${L(45)}%,0.28)`; ctx.lineWidth = 0.7 * px; ctx.stroke();
            break;
          default:
            ctx.strokeStyle = dark ? "rgba(148,163,184,0.35)" : "rgba(71,85,105,0.4)"; ctx.lineWidth = 0.8 * px; ctx.stroke();
        }
        ctx.setLineDash([]);
      }

      if (!reduce && cfg.packet) {
        // Spawn packets: agent links both ways, others parent to child.
        for (const { e, s, t, p } of live) {
          if (s.status === "offline" || t.status === "offline") continue;
          const agent = e.kind === "agent" || e.kind === "control";
          const dirs: [string, string, number][] = agent
            ? [[e.to, e.from, 2.2], [e.from, e.to, 1.1]] // telemetry up, commands down
            : e.kind === "network" ? [[e.from, e.to, 0.5], [e.to, e.from, 0.5]] : [[e.from, e.to, 0.8]];
          for (const [a, b, rate] of dirs) {
            const key = a + ">" + b;
            let acc = (spawnAcc.get(key) ?? Math.random()) + rate * cfg.rate * dt;
            while (acc >= 1 && packets.length < MAX_PACKETS) {
              acc -= 1 + (Math.random() - 0.5) * 0.6;
              const big = agent && Math.random() < 0.18;
              packets.push({
                key, d: 0, speed: (agent ? cfg.speed[0] : cfg.speed[1]) * (0.75 + Math.random() * 0.6),
                len: cfg.len[0] * (big ? 1.7 : 1) + Math.random() * cfg.len[1], hue: cfg.hue(), w: big ? 1.5 : 1.1, glyphs: bits(6),
              });
            }
            spawnAcc.set(key, Math.min(acc, 2));
          }
          if (cfg.arcs && agent && Math.random() < dt * 0.12) {
            const [a, b] = [p.pts[0], p.pts[p.pts.length - 1]];
            arcs.push({ pts: jag(a[0], a[1], b[0], b[1], p.len * 0.08, 5), born: now, life: 180 });
          }
        }

        const alive: Packet[] = [];
        for (const pk of packets) {
          const p = paths.get(pk.key);
          if (!p) continue;
          pk.d += pk.speed * dt;
          if (pk.d >= p.len) {
            const [x, y, ux, uy] = at(p, p.len);
            if (cfg.arrive) arrive(cfg.arrive, x, y, ux, uy, pk.hue, now);
            continue;
          }
          alive.push(pk);
          const [hx, hy] = at(p, pk.d);
          if (cfg.packet === "streak") {
            // Tail sampled along the route (so it follows PCB corners).
            const seg = 7;
            const tl = Math.min(pk.len, pk.d);
            let prev = at(p, pk.d - tl);
            for (let i = 1; i <= seg; i++) {
              const cur = at(p, pk.d - tl + (tl * i) / seg);
              const f = i / seg;
              ctx.strokeStyle = `hsla(${pk.hue},${cfg.sat}%,${L(58 + f * 30)}%,${(dark ? 0.75 : 0.8) * f * f})`;
              ctx.lineWidth = pk.w * px * (th === "fiber" ? 1.3 : 1);
              ctx.beginPath(); ctx.moveTo(prev[0], prev[1]); ctx.lineTo(cur[0], cur[1]); ctx.stroke();
              if (i > seg / 2) { // soft bloom near the head
                ctx.globalAlpha = dark ? 0.2 : 0.12;
                ctx.lineWidth = pk.w * 3.5 * px;
                ctx.stroke();
                ctx.globalAlpha = 1;
              }
              prev = cur;
            }
            const gs = (pk.w > 1.2 ? 20 : 15) * px * (th === "fiber" ? 1.25 : th === "circuit" ? 0.8 : 1);
            ctx.drawImage(sprite(pk.hue, cfg.sat, dark), hx - gs / 2, hy - gs / 2, gs, gs);
          } else if (cfg.packet === "glyph") {
            ctx.font = `600 ${(9 * px).toFixed(3)}px ui-monospace, Consolas, monospace`;
            ctx.textAlign = "center";
            ctx.textBaseline = "middle";
            for (let i = 0; i < 6; i++) {
              const dd = pk.d - i * 9 * px;
              if (dd < 0) break;
              const [gx, gy] = at(p, dd);
              const a = i === 0 ? 1 : (1 - i / 6) * 0.7;
              ctx.fillStyle = i === 0 ? `hsla(${pk.hue},90%,${L(85)}%,${a})` : `hsla(${pk.hue},${cfg.sat}%,${L(55)}%,${a})`;
              // Glyphs flicker as they move.
              const ch = Math.random() < 0.04 ? (pk.glyphs[i] === "0" ? "1" : "0") : pk.glyphs[i];
              ctx.fillText(ch, gx, gy);
            }
            const gs = 10 * px;
            ctx.globalAlpha = 0.6;
            ctx.drawImage(sprite(pk.hue, cfg.sat, dark), hx - gs / 2, hy - gs / 2, gs, gs);
            ctx.globalAlpha = 1;
          } else {
            ctx.fillStyle = dark ? "rgba(203,213,225,0.75)" : "rgba(51,65,85,0.7)";
            ctx.beginPath(); ctx.arc(hx, hy, 1.6 * px, 0, Math.PI * 2); ctx.fill();
          }
        }
        packets = alive;

        // Arrival effects.
        const keep: Burst[] = [];
        for (const b of bursts) {
          const age = (now - b.born) / b.life;
          if (age >= 1) continue;
          keep.push(b);
          const fade = (1 - age) * (0.75 + Math.random() * 0.25);
          const spr = sprite(b.hue, CFG[th].sat, dark);
          if (b.kind === "lightning" && b.bolts) {
            ctx.strokeStyle = `hsla(${b.hue},100%,${L(78)}%,${fade})`;
            ctx.lineWidth = 0.85 * px;
            ctx.beginPath();
            for (const bolt of b.bolts) {
              const n = Math.max(2, Math.ceil(bolt.length * Math.min(1, age * 4)));
              ctx.moveTo(bolt[0][0], bolt[0][1]);
              for (let i = 1; i < n; i++) ctx.lineTo(bolt[i][0], bolt[i][1]);
            }
            ctx.stroke();
            ctx.strokeStyle = `hsla(${b.hue + 15},90%,${L(70)}%,${fade * 0.5})`;
            ctx.lineWidth = 0.5 * px;
            ctx.beginPath(); ctx.arc(b.x, b.y, 3 + age * 9, 0, Math.PI * 2); ctx.stroke();
            const gs = (14 + (1 - age) * 16) * px;
            ctx.globalAlpha = fade * 0.9; ctx.drawImage(spr, b.x - gs / 2, b.y - gs / 2, gs, gs); ctx.globalAlpha = 1;
          } else if (b.kind === "ripple") {
            for (const [r0, a0] of [[2, 1], [6, 0.6]] as const) {
              ctx.strokeStyle = `hsla(${b.hue},95%,${L(75)}%,${(1 - age) * 0.6 * a0})`;
              ctx.lineWidth = 0.7 * px;
              ctx.beginPath(); ctx.arc(b.x, b.y, r0 + age * 16, 0, Math.PI * 2); ctx.stroke();
            }
            const gs = (12 + (1 - age) * 22) * px;
            ctx.globalAlpha = (1 - age) * 0.9; ctx.drawImage(spr, b.x - gs / 2, b.y - gs / 2, gs, gs); ctx.globalAlpha = 1;
          } else if (b.kind === "spark") {
            const r = (2 + age * 7) * 1;
            ctx.strokeStyle = `hsla(${b.hue},100%,${L(75)}%,${fade})`;
            ctx.lineWidth = 0.8 * px;
            ctx.beginPath();
            for (const [dx, dy] of [[1, 0], [-1, 0], [0, 1], [0, -1]]) { ctx.moveTo(b.x + dx * r * 0.4, b.y + dy * r * 0.4); ctx.lineTo(b.x + dx * r, b.y + dy * r); }
            for (const [dx, dy] of [[0.7, 0.7], [-0.7, 0.7], [0.7, -0.7], [-0.7, -0.7]]) { ctx.moveTo(b.x + dx * r * 0.3, b.y + dy * r * 0.3); ctx.lineTo(b.x + dx * r * 0.6, b.y + dy * r * 0.6); }
            ctx.stroke();
            const gs = (10 + (1 - age) * 10) * px;
            ctx.globalAlpha = fade; ctx.drawImage(spr, b.x - gs / 2, b.y - gs / 2, gs, gs); ctx.globalAlpha = 1;
          } else if (b.kind === "scatter" && b.glyphs) {
            ctx.font = `600 ${(8 * px).toFixed(3)}px ui-monospace, Consolas, monospace`;
            ctx.textAlign = "center"; ctx.textBaseline = "middle";
            for (let i = 0; i < b.glyphs.length; i++) {
              const ang = Math.atan2(b.uy, b.ux) + Math.PI + (i - 1.5) * 0.7;
              const r = 3 + age * 14;
              ctx.fillStyle = `hsla(${b.hue},85%,${L(70)}%,${(1 - age) * 0.8})`;
              ctx.fillText(b.glyphs[i], b.x + Math.cos(ang) * r, b.y + Math.sin(ang) * r);
            }
          }
        }
        bursts = keep.slice(-220);

        const arcKeep: Arc[] = [];
        for (const a of arcs) {
          const age = (now - a.born) / a.life;
          if (age >= 1) continue;
          arcKeep.push(a);
          ctx.strokeStyle = `hsla(${250 + Math.random() * 20},100%,${L(75)}%,${(1 - age) * (Math.random() < 0.3 ? 0.2 : 0.45)})`;
          ctx.lineWidth = 0.55 * px;
          ctx.beginPath();
          ctx.moveTo(a.pts[0][0], a.pts[0][1]);
          for (const [x, y] of a.pts) ctx.lineTo(x, y);
          ctx.stroke();
        }
        arcs = arcKeep;
      }
      raf = requestAnimationFrame(frame);
    };
    raf = requestAnimationFrame(frame);
    return () => { cancelAnimationFrame(raf); clearInterval(themeTimer); };
  }, []);

  return <canvas ref={cv} style={{ position: "absolute", inset: 0, width: "100%", height: "100%", pointerEvents: "none" }} />;
}
