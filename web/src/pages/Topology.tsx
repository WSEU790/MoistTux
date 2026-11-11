import React, { useEffect, useMemo, useRef, useState } from "react";
import { navigate, useApp, useFetch } from "../lib/state";
import { Icon } from "../components/Icon";
import { KV, Notice, Panel, SkeletonRows } from "../components/UI";

type TNode = { id: string; kind: string; label: string; sub?: string; status: string; server_id?: string; group?: string; meta?: Record<string, any> };
type TEdge = { from: string; to: string; kind: string; label?: string };
type Sim = TNode & { x: number; y: number; vx: number; vy: number; tier: number; pinned?: boolean };

const KIND_META: Record<string, { icon: string; tier: number; r: number; label: string }> = {
  desktop: { icon: "desktop", tier: 0, r: 22, label: "Desktop" },
  core: { icon: "ai", tier: 1, r: 26, label: "AI core" },
  ollama: { icon: "bolt", tier: 1, r: 20, label: "Ollama" },
  server: { icon: "server", tier: 2, r: 24, label: "Servers" },
  docker: { icon: "docker", tier: 3, r: 17, label: "Docker" },
  service: { icon: "service", tier: 3, r: 14, label: "Services" },
  proxy: { icon: "proxy", tier: 3, r: 16, label: "Proxies" },
  database: { icon: "database", tier: 3, r: 16, label: "Databases" },
  container: { icon: "layers", tier: 4, r: 13, label: "Containers" },
};

const STATUS_COLOR: Record<string, string> = { ok: "var(--ok)", warning: "var(--warn)", critical: "var(--crit)", offline: "var(--crit)", unknown: "var(--idle)" };
const POS_KEY = "nexus.topology.pins";

function loadPins(): Record<string, [number, number]> {
  try {
    return JSON.parse(localStorage.getItem(POS_KEY) || "{}");
  } catch {
    return {};
  }
}
function savePins(p: Record<string, [number, number]>) {
  try {
    localStorage.setItem(POS_KEY, JSON.stringify(p));
  } catch {
    /* storage unavailable */
  }
}

export function Topology() {
  const { servers, on } = useApp();
  const topo = useFetch<{ nodes: TNode[]; edges: TEdge[]; note: string }>("/api/topology", [], 20000);
  useEffect(() => on((e) => (e.type === "inventory" || e.type === "server.status" || e.type === "alert") && topo.reload()), [on]);
  const [hidden, setHidden] = useState<Set<string>>(new Set());
  const [serverFilter, setServerFilter] = useState("");
  const [q, setQ] = useState("");
  const [sel, setSel] = useState<TNode | null>(null);

  const { nodes, edges } = useMemo(() => {
    const ns = (topo.data?.nodes ?? []).filter((n) => !hidden.has(n.kind) && (!serverFilter || !n.server_id || n.server_id === serverFilter));
    const ids = new Set(ns.map((n) => n.id));
    return { nodes: ns, edges: (topo.data?.edges ?? []).filter((e) => ids.has(e.from) && ids.has(e.to)) };
  }, [topo.data, hidden, serverFilter]);

  const kinds = Object.keys(KIND_META);
  const counts = useMemo(() => {
    const c: Record<string, number> = {};
    for (const n of topo.data?.nodes ?? []) c[n.kind] = (c[n.kind] ?? 0) + 1;
    return c;
  }, [topo.data]);

  return (
    <div className="page full" style={{ display: "grid", gridTemplateColumns: "minmax(0,1fr) 300px", height: "100%" }}>
      <div style={{ position: "relative", minHeight: 0 }} className="ambient-grid">
        <div className="row" style={{ position: "absolute", top: 14, left: 14, right: 14, zIndex: 2, flexWrap: "wrap" }}>
          <div className="row" style={{ position: "relative" }}>
            <Icon name="search" size={14} style={{ position: "absolute", left: 10, color: "var(--text-3)" }} />
            <input className="input" style={{ paddingLeft: 30, width: 220, background: "var(--surface)" }} placeholder="Highlight…" value={q} onChange={(e) => setQ(e.target.value)} />
          </div>
          <select className="select" style={{ background: "var(--surface)" }} value={serverFilter} onChange={(e) => setServerFilter(e.target.value)}>
            <option value="">All servers</option>
            {servers.map((s) => <option key={s.id} value={s.id}>{s.name}</option>)}
          </select>
          <div className="seg" style={{ background: "var(--surface)" }}>
            {kinds.filter((k) => counts[k]).map((k) => (
              <button key={k} className={hidden.has(k) ? "" : "on"} onClick={() => { const h = new Set(hidden); h.has(k) ? h.delete(k) : h.add(k); setHidden(h); }}>
                {KIND_META[k].label} {counts[k]}
              </button>
            ))}
          </div>
          <span className="spacer" />
          <button className="btn sm" style={{ background: "var(--surface)" }} onClick={() => { savePins({}); topo.reload(); setSel(null); window.dispatchEvent(new Event("nexus-topology-reset")); }}>Reset layout</button>
        </div>
        {!topo.data ? <div style={{ padding: 80 }}><SkeletonRows /></div> : <Graph nodes={nodes} edges={edges} highlight={q} selected={sel?.id} onSelect={setSel} />}
        <div className="faint" style={{ position: "absolute", bottom: 12, left: 14, fontSize: 11 }}>Scroll to zoom · drag background to pan · drag nodes to rearrange (positions are remembered) · {topo.data?.note}</div>
      </div>
      <aside style={{ borderLeft: "1px solid var(--line)", background: "var(--chrome)", overflow: "auto", padding: 16 }}>
        {sel ? <NodeDetail n={sel} /> : (
          <div className="col" style={{ gap: 12 }}>
            <div className="label">Legend</div>
            {Object.entries(STATUS_COLOR).map(([k, c]) => <div key={k} className="row" style={{ fontSize: 12 }}><span style={{ width: 10, height: 10, borderRadius: 5, border: `2px solid ${c}` }} />{k}</div>)}
            <div className="sep" />
            <div className="row" style={{ fontSize: 12 }}><svg width="40" height="6"><line x1="0" y1="3" x2="40" y2="3" stroke="var(--accent)" strokeDasharray="4 4" /></svg> authenticated agent link</div>
            <div className="row" style={{ fontSize: 12 }}><svg width="40" height="6"><line x1="0" y1="3" x2="40" y2="3" stroke="var(--line-3)" /></svg> hosts / runs</div>
            <div className="row" style={{ fontSize: 12 }}><svg width="40" height="6"><line x1="0" y1="3" x2="40" y2="3" stroke="var(--c2)" strokeDasharray="2 3" /></svg> shared Docker network</div>
            <Notice>Topology is generated from what the agents discovered: listening processes, systemd failures, containers and their networks. Select a node for details.</Notice>
          </div>
        )}
      </aside>
    </div>
  );
}

function NodeDetail({ n }: { n: TNode }) {
  const { serverById } = useApp();
  const srv = n.server_id ? serverById(n.server_id) : undefined;
  let link: [string, string] | null = null;
  if (n.kind === "server") link = ["/server/" + n.server_id, "Open server workspace"];
  else if (n.kind === "container" || n.kind === "docker" || (n.meta?.container && n.server_id)) link = [`/server/${n.server_id}/docker`, "Open Docker"];
  else if (n.kind === "service" || n.kind === "proxy" || n.kind === "database") link = [`/server/${n.server_id}/${n.meta?.unit ? "services" : "network"}`, n.meta?.unit ? "Open services" : "Open network"];
  else if (n.kind === "ollama") link = ["/settings/ai", "AI settings"];
  else if (n.kind === "core") link = ["/ai", "AI Command Center"];
  return (
    <div className="col" style={{ gap: 14 }}>
      <div className="row">
        <span style={{ width: 34, height: 34, borderRadius: 9, display: "grid", placeItems: "center", background: "var(--surface-2)", border: `1.5px solid ${STATUS_COLOR[n.status]}` }}><Icon name={KIND_META[n.kind]?.icon ?? "info"} /></span>
        <div className="grow">
          <div style={{ fontWeight: 600 }}>{n.label}</div>
          <div className="faint" style={{ fontSize: 12 }}>{KIND_META[n.kind]?.label ?? n.kind} · {n.status}</div>
        </div>
      </div>
      {n.sub && <div className="mono dim" style={{ fontSize: 12 }}>{n.sub}</div>}
      <KV rows={[...(srv ? [["Server", srv.name] as [string, string]] : []), ...(n.group ? [["Group", n.group] as [string, string]] : []), ...Object.entries(n.meta ?? {}).filter(([, v]) => v !== "" && v !== undefined && v !== null).map(([k, v]) => [k, String(v)] as [string, string])]} />
      {link && <button className="btn primary" onClick={() => navigate(link![0])}>{link[1]} <Icon name="chevron" size={12} /></button>}
    </div>
  );
}

function Graph({ nodes, edges, highlight, selected, onSelect }: { nodes: TNode[]; edges: TEdge[]; highlight: string; selected?: string; onSelect: (n: TNode | null) => void }) {
  const wrap = useRef<HTMLDivElement>(null);
  const [size, setSize] = useState({ w: 1000, h: 700 });
  const [view, setView] = useState({ x: 0, y: 0, k: 1 });
  const sim = useRef(new Map<string, Sim>());
  const [, tick] = useState(0);
  const drag = useRef<{ id?: string; sx: number; sy: number; vx: number; vy: number; moved: boolean } | null>(null);
  const alpha = useRef(1);

  useEffect(() => {
    const el = wrap.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setSize({ w: el.clientWidth, h: el.clientHeight }));
    ro.observe(el);
    const reset = () => { sim.current.clear(); alpha.current = 1; tick((t) => t + 1); };
    window.addEventListener("nexus-topology-reset", reset);
    return () => { ro.disconnect(); window.removeEventListener("nexus-topology-reset", reset); };
  }, []);

  // Seed new nodes near their parent on their tier; keep existing positions.
  useEffect(() => {
    const pins = loadPins();
    const m = sim.current;
    const parent = new Map<string, string>();
    for (const e of edges) if (!parent.has(e.to) && e.kind !== "network") parent.set(e.to, e.from);
    const tierY = (t: number) => 90 + t * 150;
    const byTier: Record<number, TNode[]> = {};
    for (const n of nodes) (byTier[KIND_META[n.kind]?.tier ?? 4] ??= []).push(n);
    for (const n of nodes) {
      if (m.has(n.id)) {
        Object.assign(m.get(n.id)!, n);
        continue;
      }
      const tier = KIND_META[n.kind]?.tier ?? 4;
      const row = byTier[tier];
      const idx = row.indexOf(n);
      let x = size.w / 2 + (idx - (row.length - 1) / 2) * Math.min(160, (size.w - 120) / Math.max(1, row.length));
      const p = parent.get(n.id);
      if (p && m.has(p)) x = m.get(p)!.x + (Math.random() - 0.5) * 80;
      const pin = pins[n.id];
      m.set(n.id, { ...n, x: pin ? pin[0] : x, y: pin ? pin[1] : tierY(tier) + (Math.random() - 0.5) * 30, vx: 0, vy: 0, tier, pinned: !!pin });
    }
    for (const id of [...m.keys()]) if (!nodes.some((n) => n.id === id)) m.delete(id);
    alpha.current = Math.max(alpha.current, 0.6);
  }, [nodes, edges, size.w]);

  // Force simulation (velocity Verlet with damping), paused when settled.
  useEffect(() => {
    let raf = 0;
    const step = () => {
      const m = [...sim.current.values()];
      const a = alpha.current;
      if (a > 0.01 && m.length) {
        const byId = sim.current;
        for (let i = 0; i < m.length; i++) {
          const p = m[i];
          for (let j = i + 1; j < m.length; j++) {
            const q2 = m[j];
            let dx = p.x - q2.x;
            let dy = p.y - q2.y;
            let d2 = dx * dx + dy * dy;
            if (d2 < 1) { dx = Math.random(); dy = Math.random(); d2 = 1; }
            const d = Math.sqrt(d2);
            const minD = (KIND_META[p.kind]?.r ?? 14) + (KIND_META[q2.kind]?.r ?? 14) + 70;
            const f = ((5200 / d2) + (d < minD ? (minD - d) * 0.08 : 0)) * a;
            p.vx += (dx / d) * f; p.vy += (dy / d) * f;
            q2.vx -= (dx / d) * f; q2.vy -= (dy / d) * f;
          }
        }
        for (const e of edges) {
          const s = byId.get(e.from), t = byId.get(e.to);
          if (!s || !t) continue;
          const dx = t.x - s.x, dy = t.y - s.y;
          const d = Math.sqrt(dx * dx + dy * dy) || 1;
          const target = e.kind === "network" ? 90 : 130;
          const f = ((d - target) / d) * 0.035 * a;
          s.vx += dx * f; s.vy += dy * f;
          t.vx -= dx * f; t.vy -= dy * f;
        }
        for (const p of m) {
          p.vy += ((90 + p.tier * 150) - p.y) * 0.02 * a; // keep tiers
          p.vx += (size.w / 2 - p.x) * 0.0015 * a;
          if (p.pinned || drag.current?.id === p.id) { p.vx = 0; p.vy = 0; continue; }
          p.vx *= 0.82; p.vy *= 0.82;
          p.x += Math.max(-30, Math.min(30, p.vx));
          p.y += Math.max(-30, Math.min(30, p.vy));
        }
        alpha.current = a * 0.985;
        tick((t) => (t + 1) % 1e9);
      }
      raf = requestAnimationFrame(step);
    };
    raf = requestAnimationFrame(step);
    return () => cancelAnimationFrame(raf);
  }, [edges, size.w]);

  const toWorld = (cx: number, cy: number) => {
    const r = wrap.current!.getBoundingClientRect();
    return { x: (cx - r.left - view.x) / view.k, y: (cy - r.top - view.y) / view.k };
  };
  const onWheel = (e: React.WheelEvent) => {
    const r = wrap.current!.getBoundingClientRect();
    const k = Math.max(0.3, Math.min(2.5, view.k * (e.deltaY < 0 ? 1.1 : 0.9)));
    const mx = e.clientX - r.left, my = e.clientY - r.top;
    setView({ k, x: mx - ((mx - view.x) / view.k) * k, y: my - ((my - view.y) / view.k) * k });
  };
  const onDown = (e: React.PointerEvent, id?: string) => {
    e.stopPropagation();
    (e.target as Element).setPointerCapture?.(e.pointerId);
    drag.current = { id, sx: e.clientX, sy: e.clientY, vx: view.x, vy: view.y, moved: false };
  };
  const onMove = (e: React.PointerEvent) => {
    const d = drag.current;
    if (!d) return;
    if (Math.abs(e.clientX - d.sx) + Math.abs(e.clientY - d.sy) > 3) d.moved = true;
    if (d.id) {
      const p = sim.current.get(d.id);
      if (p) {
        const w = toWorld(e.clientX, e.clientY);
        p.x = w.x; p.y = w.y;
        alpha.current = Math.max(alpha.current, 0.3);
        tick((t) => t + 1);
      }
    } else setView((v) => ({ ...v, x: d.vx + e.clientX - d.sx, y: d.vy + e.clientY - d.sy }));
  };
  const onUp = () => {
    const d = drag.current;
    drag.current = null;
    if (!d) return;
    if (d.id) {
      const p = sim.current.get(d.id);
      if (!d.moved) { onSelect(p ? (p as TNode) : null); return; }
      if (p) {
        p.pinned = true;
        const pins = loadPins();
        pins[p.id] = [Math.round(p.x), Math.round(p.y)];
        savePins(pins);
      }
    } else if (!d.moved) onSelect(null);
  };

  const hl = highlight.toLowerCase();
  const ns = [...sim.current.values()];
  return (
    <div ref={wrap} style={{ position: "absolute", inset: 0, cursor: drag.current && !drag.current.id ? "grabbing" : "grab" }} onWheel={onWheel} onPointerDown={(e) => onDown(e)} onPointerMove={onMove} onPointerUp={onUp}>
      <svg width="100%" height="100%" style={{ display: "block" }}>
        <defs>
          <filter id="glow" x="-50%" y="-50%" width="200%" height="200%"><feGaussianBlur stdDeviation="4" result="b" /><feMerge><feMergeNode in="b" /><feMergeNode in="SourceGraphic" /></feMerge></filter>
        </defs>
        <style>{`.flow{animation:flow 1.2s linear infinite}@keyframes flow{to{stroke-dashoffset:-16}}`}</style>
        <g transform={`translate(${view.x},${view.y}) scale(${view.k})`}>
          {edges.map((e, i) => {
            const s = sim.current.get(e.from), t = sim.current.get(e.to);
            if (!s || !t) return null;
            const agentLink = e.kind === "agent" || e.kind === "control";
            const off = t.status === "offline";
            return (
              <g key={i}>
                <line x1={s.x} y1={s.y} x2={t.x} y2={t.y} stroke={off ? "var(--crit)" : agentLink ? "var(--accent)" : e.kind === "network" ? "var(--c2)" : "var(--line-3)"} strokeOpacity={agentLink ? 0.7 : 0.55} strokeWidth={agentLink ? 1.4 : 1} strokeDasharray={agentLink ? "4 4" : e.kind === "network" ? "2 3" : undefined} className={agentLink && !off ? "flow" : undefined} />
                {e.label && (agentLink || view.k > 1.4) && <text x={(s.x + t.x) / 2} y={(s.y + t.y) / 2 - 4} textAnchor="middle" fill="var(--text-4)" style={{ font: "9.5px var(--font-mono)" }}>{e.label}</text>}
              </g>
            );
          })}
          {ns.map((n) => {
            const km = KIND_META[n.kind] ?? KIND_META.container;
            const dim = hl && !(`${n.label} ${n.sub ?? ""}`.toLowerCase().includes(hl));
            const col = STATUS_COLOR[n.status] ?? "var(--idle)";
            const isSel = selected === n.id;
            return (
              <g key={n.id} transform={`translate(${n.x},${n.y})`} style={{ cursor: "pointer", opacity: dim ? 0.2 : 1, transition: "opacity 200ms" }} onPointerDown={(e) => onDown(e, n.id)}>
                {(n.status === "critical" || n.status === "offline") && <circle r={km.r + 6} fill="none" stroke={col} strokeOpacity={0.35} filter="url(#glow)"><animate attributeName="r" values={`${km.r + 3};${km.r + 9};${km.r + 3}`} dur="2s" repeatCount="indefinite" /></circle>}
                <circle r={km.r} fill="var(--surface)" stroke={isSel ? "var(--accent)" : col} strokeWidth={isSel ? 2.5 : 1.8} />
                <foreignObject x={-9} y={-9} width={18} height={18} style={{ pointerEvents: "none", color: "var(--text-2)" }}>
                  <Icon name={km.icon} size={18} stroke={1.5} />
                </foreignObject>
                <text y={km.r + 14} textAnchor="middle" fill="var(--text)" style={{ font: `${n.kind === "server" || n.kind === "core" ? 600 : 500} 11.5px var(--font-ui)`, pointerEvents: "none" }}>{n.label.length > 24 ? n.label.slice(0, 23) + "…" : n.label}</text>
                {n.sub && view.k > 0.7 && <text y={km.r + 27} textAnchor="middle" fill="var(--text-3)" style={{ font: "10px var(--font-mono)", pointerEvents: "none" }}>{n.sub.length > 34 ? n.sub.slice(0, 33) + "…" : n.sub}</text>}
              </g>
            );
          })}
        </g>
      </svg>
    </div>
  );
}

export { Panel };
