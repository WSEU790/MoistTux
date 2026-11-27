import { useEffect, useRef } from "react";

// Animated data flow for the topology: thin deep-blue/violet packets travel
// along the links and end in a small lightning burst where they reach a
// node. Drawn on a canvas under the SVG nodes, with additive blending in dark
// themes so overlapping light adds up.

export type FlowNode = { x: number; y: number; kind: string; status: string };
export type FlowEdge = { from: string; to: string; kind: string };
type View = { x: number; y: number; k: number };

type Packet = { a: string; b: string; t: number; speed: number; len: number; hue: number; w: number };
type Bolt = { pts: [number, number][] };
type Burst = { x: number; y: number; born: number; life: number; bolts: Bolt[]; ring: number; hue: number };
type Arc = { pts: [number, number][]; born: number; life: number };

const MAX_PACKETS = 420;

// Hues between deep blue (232) and violet (275).
const hue = () => 232 + Math.random() * 43;

function sprite(dark: boolean) {
  const c = document.createElement("canvas");
  c.width = c.height = 64;
  const g = c.getContext("2d")!;
  const grd = g.createRadialGradient(32, 32, 0, 32, 32, 32);
  if (dark) {
    grd.addColorStop(0, "rgba(236,232,255,1)");
    grd.addColorStop(0.12, "rgba(180,160,255,0.85)");
    grd.addColorStop(0.35, "rgba(124,77,255,0.28)");
    grd.addColorStop(1, "rgba(60,40,200,0)");
  } else {
    grd.addColorStop(0, "rgba(76,29,149,1)");
    grd.addColorStop(0.2, "rgba(91,33,182,0.6)");
    grd.addColorStop(1, "rgba(67,56,202,0)");
  }
  g.fillStyle = grd;
  g.fillRect(0, 0, 64, 64);
  return c;
}

/** A jagged polyline from (x0,y0) to (x1,y1) by midpoint displacement. */
function jag(x0: number, y0: number, x1: number, y1: number, rough: number, depth: number): [number, number][] {
  let pts: [number, number][] = [[x0, y0], [x1, y1]];
  let r = rough;
  for (let d = 0; d < depth; d++) {
    const next: [number, number][] = [pts[0]];
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

export function PacketLayer({ nodes, edges, view, radius }: { nodes: React.MutableRefObject<Map<string, FlowNode>>; edges: FlowEdge[]; view: React.MutableRefObject<View>; radius: (kind: string) => number }) {
  const cv = useRef<HTMLCanvasElement>(null);
  const edgesRef = useRef(edges);
  edgesRef.current = edges;

  useEffect(() => {
    const canvas = cv.current!;
    const ctx = canvas.getContext("2d")!;
    const reduce = window.matchMedia?.("(prefers-reduced-motion: reduce)").matches ?? false;
    let dark = true;
    let glow = sprite(true);
    const checkTheme = () => {
      const m = getComputedStyle(document.body).backgroundColor.match(/\d+/g);
      const lum = m ? (0.299 * +m[0] + 0.587 * +m[1] + 0.114 * +m[2]) / 255 : 0;
      const d = lum < 0.5;
      if (d !== dark) { dark = d; glow = sprite(d); }
    };
    checkTheme();
    const themeTimer = setInterval(checkTheme, 1500);

    let packets: Packet[] = [];
    let bursts: Burst[] = [];
    let arcs: Arc[] = [];
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

    const burstAt = (x: number, y: number, dirx: number, diry: number, h: number, now: number) => {
      const bolts: Bolt[] = [];
      const n = 3 + Math.floor(Math.random() * 3);
      const base = Math.atan2(diry, dirx);
      for (let i = 0; i < n; i++) {
        // Spray forward and sideways from the impact point.
        const ang = base + (Math.random() - 0.5) * 2.6;
        const len = 9 + Math.random() * 13;
        bolts.push({ pts: jag(x, y, x + Math.cos(ang) * len, y + Math.sin(ang) * len, len * 0.7, 2) });
      }
      bursts.push({ x, y, born: now, life: 260 + Math.random() * 140, bolts, ring: 2 + Math.random() * 2, hue: h });
    };

    const frame = (now: number) => {
      const dt = Math.min(0.05, (now - last) / 1000);
      last = now;
      const dpr = resize();
      const v = view.current;
      const k = v.k;
      const ns = nodes.current;
      const es = edgesRef.current;
      ctx.setTransform(1, 0, 0, 1, 0, 0);
      ctx.clearRect(0, 0, canvas.width, canvas.height);
      ctx.setTransform(dpr * k, 0, 0, dpr * k, dpr * v.x, dpr * v.y);
      ctx.globalCompositeOperation = dark ? "lighter" : "source-over";
      ctx.lineCap = "round";
      ctx.lineJoin = "round";
      const px = 1 / k; // one screen pixel in world units

      // Links: a hairline with a faint wide glow.
      for (const e of es) {
        const s = ns.get(e.from), t = ns.get(e.to);
        if (!s || !t) continue;
        const off = s.status === "offline" || t.status === "offline";
        const g = ctx.createLinearGradient(s.x, s.y, t.x, t.y);
        g.addColorStop(0, off ? "rgba(120,60,90,0.10)" : dark ? "rgba(59,76,255,0.16)" : "rgba(67,56,202,0.18)");
        g.addColorStop(1, off ? "rgba(120,60,90,0.10)" : dark ? "rgba(139,92,246,0.16)" : "rgba(109,40,217,0.18)");
        ctx.strokeStyle = g;
        ctx.lineWidth = 4 * px;
        ctx.beginPath(); ctx.moveTo(s.x, s.y); ctx.lineTo(t.x, t.y); ctx.stroke();
        const h = ctx.createLinearGradient(s.x, s.y, t.x, t.y);
        h.addColorStop(0, off ? "rgba(160,70,100,0.35)" : dark ? "rgba(79,96,255,0.55)" : "rgba(67,56,202,0.5)");
        h.addColorStop(1, off ? "rgba(160,70,100,0.35)" : dark ? "rgba(157,107,255,0.55)" : "rgba(109,40,217,0.5)");
        ctx.strokeStyle = h;
        ctx.lineWidth = 0.7 * px;
        ctx.stroke();
      }

      if (!reduce) {
        // Spawn packets per link; agent links carry traffic both ways.
        for (const e of es) {
          const s = ns.get(e.from), t = ns.get(e.to);
          if (!s || !t || s.status === "offline" || t.status === "offline") continue;
          const agent = e.kind === "agent" || e.kind === "control";
          const dirs: [string, string, number][] = agent
            ? [[e.to, e.from, 2.2], [e.from, e.to, 1.1]] // telemetry up, commands down
            : e.kind === "network" ? [[e.from, e.to, 0.5], [e.to, e.from, 0.5]] : [[e.from, e.to, 0.8]];
          for (const [a, b, rate] of dirs) {
            const key = a + ">" + b;
            let acc = (spawnAcc.get(key) ?? Math.random()) + rate * dt;
            while (acc >= 1 && packets.length < MAX_PACKETS) {
              acc -= 1 + (Math.random() - 0.5) * 0.6; // irregular spacing
              const big = agent && Math.random() < 0.18;
              packets.push({ a, b, t: 0, speed: (agent ? 170 : 110) * (0.75 + Math.random() * 0.6), len: big ? 40 : 22 + Math.random() * 12, hue: hue(), w: big ? 1.5 : 1.1 });
            }
            spawnAcc.set(key, Math.min(acc, 2));
          }
          // A rare faint arc of lightning along busy agent links.
          if (agent && Math.random() < dt * 0.12) {
            arcs.push({ pts: jag(s.x, s.y, t.x, t.y, Math.hypot(t.x - s.x, t.y - s.y) * 0.08, 5), born: now, life: 180 });
          }
        }

        // Packets: a thin tail fading into the past and a bright head.
        const alive: Packet[] = [];
        for (const p of packets) {
          const s = ns.get(p.a), t = ns.get(p.b);
          if (!s || !t) continue;
          const dx = t.x - s.x, dy = t.y - s.y;
          const dist = Math.hypot(dx, dy) || 1;
          const ux = dx / dist, uy = dy / dist;
          const start = radius(s.kind) + 2, end = dist - radius(t.kind) - 2;
          if (end <= start) continue;
          p.t += (p.speed * dt) / (end - start);
          if (p.t >= 1) {
            burstAt(s.x + ux * end, s.y + uy * end, ux, uy, p.hue, now);
            continue;
          }
          alive.push(p);
          const d = start + (end - start) * p.t;
          const hx = s.x + ux * d, hy = s.y + uy * d;
          const tl = Math.min(p.len, d - start);
          const tx = hx - ux * tl, ty = hy - uy * tl;
          const g = ctx.createLinearGradient(tx, ty, hx, hy);
          g.addColorStop(0, `hsla(${p.hue},95%,${dark ? 60 : 40}%,0)`);
          g.addColorStop(0.7, `hsla(${p.hue},95%,${dark ? 64 : 42}%,${dark ? 0.55 : 0.6})`);
          g.addColorStop(1, `hsla(${p.hue - 10},100%,${dark ? 88 : 35}%,0.95)`);
          ctx.strokeStyle = g;
          ctx.lineWidth = p.w * 3.5 * px; // soft bloom around the streak
          ctx.globalAlpha = dark ? 0.22 : 0.15;
          ctx.beginPath(); ctx.moveTo(tx, ty); ctx.lineTo(hx, hy); ctx.stroke();
          ctx.globalAlpha = 1;
          ctx.lineWidth = p.w * px;
          ctx.stroke();
          const gs = (p.w > 1.2 ? 20 : 15) * px;
          ctx.drawImage(glow, hx - gs / 2, hy - gs / 2, gs, gs);
        }
        packets = alive;

        // Arrival bursts: micro-bolts and a quick ring that flicker out.
        const keep: Burst[] = [];
        for (const b of bursts) {
          const age = (now - b.born) / b.life;
          if (age >= 1) continue;
          keep.push(b);
          const fade = (1 - age) * (0.75 + Math.random() * 0.25); // flicker
          ctx.strokeStyle = `hsla(${b.hue},100%,${dark ? 78 : 40}%,${fade})`;
          ctx.lineWidth = 0.85 * px;
          ctx.beginPath();
          for (const bolt of b.bolts) {
            // Bolts grow out quickly, then fade.
            const n = Math.max(2, Math.ceil(bolt.pts.length * Math.min(1, age * 4)));
            ctx.moveTo(bolt.pts[0][0], bolt.pts[0][1]);
            for (let i = 1; i < n; i++) ctx.lineTo(bolt.pts[i][0], bolt.pts[i][1]);
          }
          ctx.stroke();
          ctx.strokeStyle = `hsla(${b.hue + 15},90%,${dark ? 70 : 45}%,${fade * 0.5})`;
          ctx.lineWidth = 0.5 * px;
          ctx.beginPath(); ctx.arc(b.x, b.y, b.ring + age * 9, 0, Math.PI * 2); ctx.stroke();
          const gs = (14 + (1 - age) * 16) * px;
          ctx.globalAlpha = fade * 0.9;
          ctx.drawImage(glow, b.x - gs / 2, b.y - gs / 2, gs, gs);
          ctx.globalAlpha = 1;
        }
        bursts = keep.slice(-200);

        const arcKeep: Arc[] = [];
        for (const a of arcs) {
          const age = (now - a.born) / a.life;
          if (age >= 1) continue;
          arcKeep.push(a);
          ctx.strokeStyle = `hsla(${250 + Math.random() * 20},100%,${dark ? 75 : 45}%,${(1 - age) * (Math.random() < 0.3 ? 0.2 : 0.45)})`;
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
