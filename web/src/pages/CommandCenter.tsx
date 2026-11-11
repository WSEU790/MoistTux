import React, { useMemo, useState } from "react";
import { DEFAULT_LAYOUT, navigate, useApp, useFetch } from "../lib/state";
import { Icon } from "../components/Icon";
import { Bar, Empty, Panel, SkeletonRows, StatusDot } from "../components/UI";
import { Legend, Ring, Sparkline, TimeSeriesChart } from "../components/Charts";
import { ago, bytes, levelClass, maxFS, netTotals, pct, rate, sevClass, statusClass } from "../lib/format";
import type { Alert, Execution, Incident, Point, Server } from "../lib/types";

const PANEL_NAMES: Record<string, string> = {
  kpis: "Infrastructure overview",
  fleet: "Fleet health",
  alerts: "Active alerts & incidents",
  trend: "Fleet utilisation trend",
  ai: "AI operator",
  activity: "Recent operations",
};

export function CommandCenter() {
  const { servers, serversLoaded, overview, appearance, setAppearance } = useApp();
  const [customize, setCustomize] = useState(false);
  const layout = appearance.layout?.length ? appearance.layout : DEFAULT_LAYOUT;

  if (serversLoaded && servers.length === 0) return <FirstRun />;

  const move = (i: number, d: number) => {
    const l = [...layout];
    const j = i + d;
    if (j < 0 || j >= l.length) return;
    [l[i], l[j]] = [l[j], l[i]];
    setAppearance({ layout: l });
  };
  const toggle = (id: string) => setAppearance({ layout: layout.map((p) => (p.id === id ? { ...p, visible: !p.visible } : p)) });

  const panels: Record<string, React.ReactNode> = {
    kpis: <Overview ov={overview} />,
    fleet: <FleetHealth servers={servers} />,
    alerts: <AlertsPanel />,
    trend: <FleetTrend servers={servers} />,
    ai: <AIPanel />,
    activity: <RecentOps />,
  };
  return (
    <div className="page">
      <div className="page-head">
        <div className="grow">
          <h1>Command Center</h1>
          <p>Live state of {servers.length} managed server{servers.length === 1 ? "" : "s"}. Every value on this page comes from agent telemetry.</p>
        </div>
        <button className="btn ghost" onClick={() => setCustomize(!customize)}>
          <Icon name="layers" /> {customize ? "Done" : "Customize layout"}
        </button>
      </div>
      {customize && (
        <Panel title="Dashboard layout" className="" style={{ marginBottom: 16 }}>
          <div className="col" style={{ gap: 6 }}>
            {layout.map((p, i) => (
              <div key={p.id} className="row" style={{ padding: "6px 8px", background: "var(--surface-2)", borderRadius: "var(--radius-sm)" }}>
                <Icon name="grip" />
                <span className="grow" style={{ opacity: p.visible ? 1 : 0.5 }}>{PANEL_NAMES[p.id] ?? p.id}</span>
                <button className="btn ghost sm icon" onClick={() => move(i, -1)} disabled={i === 0} aria-label="Move up"><Icon name="arrowup" size={13} /></button>
                <button className="btn ghost sm icon" onClick={() => move(i, 1)} disabled={i === layout.length - 1} aria-label="Move down"><Icon name="arrowdown" size={13} /></button>
                <button className="btn sm" onClick={() => toggle(p.id)}>{p.visible ? "Hide" : "Show"}</button>
              </div>
            ))}
            <div><button className="btn ghost sm" onClick={() => setAppearance({ layout: DEFAULT_LAYOUT })}>Reset to default</button></div>
          </div>
        </Panel>
      )}
      <div className="col" style={{ gap: 16 }}>
        {pairUp(layout.filter((p) => p.visible).map((p) => p.id)).map((row, i) =>
          row.length === 1 ? (
            <div key={i}>{panels[row[0]]}</div>
          ) : (
            <div key={i} className="grid" style={{ gridTemplateColumns: "minmax(0,1.55fr) minmax(0,1fr)" }}>
              {panels[row[0]]}
              {panels[row[1]]}
            </div>
          ),
        )}
      </div>
    </div>
  );
}

// Wide panels take a full row; the others are paired side by side.
function pairUp(ids: string[]): string[][] {
  const wide = new Set(["kpis"]);
  const out: string[][] = [];
  let pending: string | null = null;
  for (const id of ids) {
    if (wide.has(id)) {
      if (pending) out.push([pending]), (pending = null);
      out.push([id]);
    } else if (pending) {
      out.push([pending, id]);
      pending = null;
    } else pending = id;
  }
  if (pending) out.push([pending]);
  return out;
}

function Overview({ ov }: { ov: any }) {
  if (!ov) return <div className="kpis"><SkeletonRows n={2} /></div>;
  const s = ov.servers ?? {};
  const crit = ov.alerts?.critical ?? 0;
  const warn = ov.alerts?.warning ?? 0;
  const inv = ov.inventoried ?? {};
  const tile = (label: string, value: React.ReactNode, sub: React.ReactNode, onClick?: () => void, cls?: string) => (
    <div className={"kpi" + (onClick ? " clickable" : "")} onClick={onClick}>
      <div className="label">{label}</div>
      <div className={"kpi-v " + (cls ?? "")}>{value}</div>
      <div className="kpi-s">{sub}</div>
    </div>
  );
  return (
    <div className="kpis scan">
      {tile("Managed servers", s.total ?? 0, <span className="row" style={{ gap: 10 }}><span className="ok-t">{s.online ?? 0} online</span>{(s.degraded ?? 0) > 0 && <span className="warn-t">{s.degraded} degraded</span>}{(s.offline ?? 0) > 0 && <span className="crit-t">{s.offline} offline</span>}</span>, () => navigate("/fleet"))}
      {tile("Critical alerts", crit, `${warn} warnings · ${ov.open_incidents} open incidents`, () => navigate("/incidents"), crit ? "crit-t" : "")}
      {tile("Running services", inv.services ? ov.services_running : "—", inv.services ? <span className={ov.services_failed ? "crit-t" : ""}>{ov.services_failed} failed units</span> : "no systemd inventory yet")}
      {tile("Containers", inv.containers ? <>{ov.containers_running}<small>/ {ov.containers_total}</small></> : "—", inv.containers ? "running / total" : "Docker not detected")}
      {tile("Pending updates", inv.updates ? ov.updates_pending : "—", inv.updates ? <span className={ov.security_updates ? "warn-t" : ""}>{ov.security_updates} security</span> : "first check pending")}
      {tile("Approvals waiting", ov.pending_approvals, "operations awaiting you", () => navigate("/approvals"), ov.pending_approvals ? "warn-t" : "")}
      {tile("Backups", "—", "Backup Center not configured")}
    </div>
  );
}

function FleetHealth({ servers }: { servers: Server[] }) {
  const { live, liveTick } = useApp();
  void liveTick;
  return (
    <Panel title="Fleet health" actions={<a href="#/fleet" className="btn ghost sm">Open fleet <Icon name="chevron" size={12} /></a>} bodyClass="flush">
      <div className="table-wrap" style={{ maxHeight: 460 }}>
        <table className="t">
          <thead>
            <tr>
              <th>Server</th>
              <th>Health</th>
              <th>CPU</th>
              <th>Memory</th>
              <th>Disk</th>
              <th className="r">Network</th>
              <th className="r">Alerts</th>
            </tr>
          </thead>
          <tbody>
            {servers.map((s) => {
              const l = s.latest;
              const hist = live(s.id);
              const d = maxFS(l?.fs);
              const n = netTotals(l?.net);
              return (
                <tr key={s.id} className="clickable" onClick={() => navigate("/server/" + s.id)}>
                  <td>
                    <div className="row">
                      <StatusDot status={statusClass(s.status)} live={s.status === "online"} />
                      <div style={{ minWidth: 0 }}>
                        <div style={{ fontWeight: 500 }}>{s.name}</div>
                        <div className="faint ellipsis" style={{ fontSize: "var(--fs-xs)", maxWidth: 200 }}>
                          {s.status === "online" || s.status === "degraded" ? s.info?.os_name ?? s.address : s.status_reason || s.status}
                        </div>
                      </div>
                    </div>
                  </td>
                  <td>{s.health_score === null ? <span className="faint">—</span> : <Ring value={s.health_score} size={34} stroke={3.5} cls={s.health_score >= 85 ? "ok" : s.health_score >= 60 ? "warn" : "crit"} />}</td>
                  <td style={{ minWidth: 140 }}>
                    <div className="row">
                      <span className="num mono" style={{ width: 44 }}>{pct(l?.cpu.usage_pct, 0)}</span>
                      <Sparkline values={hist.map((h) => h.cpu.usage_pct)} max={100} width={80} height={22} color={levelClass(l?.cpu.usage_pct) === "crit" ? "--crit" : "--accent"} />
                    </div>
                  </td>
                  <td style={{ minWidth: 120 }}>
                    <div className="num mono" style={{ fontSize: 12 }}>{pct(l?.mem.used_pct, 0)}</div>
                    <Bar value={l?.mem.used_pct} cls={levelClass(l?.mem.used_pct, 85, 95)} />
                  </td>
                  <td style={{ minWidth: 110 }}>
                    <div className="num mono" style={{ fontSize: 12 }}>{pct(d, 0)}</div>
                    <Bar value={d} cls={levelClass(d, 85, 95)} />
                  </td>
                  <td className="r num mono" style={{ fontSize: 12 }}>
                    <div>↓ {rate(n.rx)}</div>
                    <div className="faint">↑ {rate(n.tx)}</div>
                  </td>
                  <td className="r">
                    {s.alerts.critical ? <span className="chip crit">{s.alerts.critical}</span> : null} {s.alerts.warning ? <span className="chip warn">{s.alerts.warning}</span> : null}
                    {!s.alerts.critical && !s.alerts.warning && <span className="faint">—</span>}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </Panel>
  );
}

function AlertsPanel() {
  const { serverById } = useApp();
  const alerts = useFetch<Alert[]>("/api/alerts?status=firing&limit=12", [], 15000);
  const incidents = useFetch<Incident[]>("/api/incidents?status=open&limit=5", [], 15000);
  return (
    <Panel title="Active alerts" actions={<a href="#/incidents" className="btn ghost sm">All <Icon name="chevron" size={12} /></a>} bodyClass="flush">
      {!alerts.data && <SkeletonRows n={4} />}
      {alerts.data?.length === 0 && <Empty icon="check" title="All clear">No monitoring rule is firing. Thresholds are configured in Settings → Monitoring.</Empty>}
      {incidents.data && incidents.data.length > 0 && (
        <div style={{ padding: "0 16px 8px" }}>
          {incidents.data.map((i) => (
            <a key={i.id} href={"#/incidents/" + i.id} className="row" style={{ padding: "8px 10px", marginBottom: 6, borderRadius: "var(--radius)", background: i.severity === "critical" ? "var(--crit-soft)" : "var(--warn-soft)", color: "var(--text)", textDecoration: "none" }}>
              <Icon name="flame" size={15} className={i.severity === "critical" ? "crit-t" : "warn-t"} />
              <span className="grow ellipsis"><b>Incident</b> · {i.title}</span>
              {i.ai_status && <span className="chip accent">AI: {i.ai_status.split(":")[0]}</span>}
              <span className="faint" style={{ fontSize: "var(--fs-xs)" }}>{ago(i.opened_at)}</span>
            </a>
          ))}
        </div>
      )}
      {alerts.data?.map((a) => (
        <a key={a.id} href={"#/server/" + a.server_id} className="row" style={{ padding: "10px 16px", borderTop: "1px solid var(--line)", color: "var(--text)", textDecoration: "none" }}>
          <span className={"dot " + sevClass(a.severity)} />
          <div className="grow" style={{ minWidth: 0 }}>
            <div className="ellipsis" style={{ fontWeight: 500 }}>{a.title}</div>
            <div className="faint ellipsis" style={{ fontSize: "var(--fs-xs)" }}>{serverById(a.server_id)?.name} · {a.detail}</div>
          </div>
          <span className="faint nowrap" style={{ fontSize: "var(--fs-xs)" }}>{ago(a.first_seen)}</span>
        </a>
      ))}
    </Panel>
  );
}

function FleetTrend({ servers }: { servers: Server[] }) {
  const [range, setRange] = useState("1h");
  const online = servers.filter((s) => s.status === "online" || s.status === "degraded").slice(0, 8);
  const [metric, setMetric] = useState<"cpu" | "mem">("cpu");
  const data = useFetchMany(online.map((s) => `/api/servers/${s.id}/telemetry?range=${range}&points=180`), 30000);
  const { times, series } = useMemo(() => {
    const colors = ["--c1", "--c2", "--c3", "--c4", "--c5", "--info", "--warn", "--ok"];
    const all = new Set<number>();
    const maps = data.map((d) => {
      const m = new Map<number, Point>();
      for (const p of d?.points ?? []) {
        const t = Math.round(p.t / 30000) * 30000;
        m.set(t, p);
        all.add(t);
      }
      return m;
    });
    const times = [...all].sort((a, b) => a - b);
    const series = online.map((s, i) => ({ label: s.name, color: colors[i % colors.length], values: times.map((t) => (maps[i]?.get(t)?.[metric] ?? null) as number | null) }));
    return { times, series };
  }, [data, metric, online.map((s) => s.id).join()]);
  return (
    <Panel
      title="Fleet utilisation"
      actions={
        <div className="row">
          <div className="seg">
            {(["cpu", "mem"] as const).map((m) => <button key={m} className={metric === m ? "on" : ""} onClick={() => setMetric(m)}>{m === "cpu" ? "CPU" : "Memory"}</button>)}
          </div>
          <div className="seg">
            {["1h", "6h", "24h", "7d"].map((r) => <button key={r} className={range === r ? "on" : ""} onClick={() => setRange(r)}>{r}</button>)}
          </div>
        </div>
      }
    >
      <TimeSeriesChart times={times} series={series} yMax={100} height={210} format={(v) => v.toFixed(0) + "%"} />
      <div style={{ marginTop: 10 }}><Legend series={series} /></div>
    </Panel>
  );
}

function useFetchMany(urls: string[], interval: number) {
  const [data, setData] = useState<any[]>([]);
  const key = urls.join("|");
  React.useEffect(() => {
    let alive = true;
    const load = () => Promise.all(urls.map((u) => fetch(u, { credentials: "same-origin" }).then((r) => (r.ok ? r.json() : null)).catch(() => null))).then((d) => alive && setData(d));
    load();
    const t = setInterval(load, interval);
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, [key, interval]);
  return data;
}

function AIPanel() {
  const { ai, overview } = useApp();
  const [q, setQ] = useState("");
  const general = ai?.roles?.find((r) => r.role === "general");
  const ask = async (text: string) => {
    if (!text.trim()) return;
    const r = await fetch("/api/conversations", { method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ mode: "analyst" }) });
    const c = await r.json();
    await fetch(`/api/conversations/${c.id}/messages`, { method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ content: text }) });
    navigate("/ai/" + c.id);
  };
  return (
    <Panel title="AI operator" actions={<a href="#/ai" className="btn ghost sm">Open <Icon name="chevron" size={12} /></a>}>
      <div className="row" style={{ marginBottom: 14 }}>
        <div style={{ width: 40, height: 40, borderRadius: 10, display: "grid", placeItems: "center", background: "var(--accent-soft)", color: "var(--accent)" }}>
          <Icon name="ai" size={20} />
        </div>
        <div className="grow">
          <div style={{ fontWeight: 600 }}>{!ai ? "Checking Ollama…" : !ai.reachable ? "Local AI unavailable" : general ? general.model : "No model installed"}</div>
          <div className="faint" style={{ fontSize: "var(--fs-xs)" }}>
            {!ai ? "" : !ai.reachable ? ai.error : `Ollama ${ai.version} · ${ai.models} models · ${ai.local ? "runs on this machine" : "remote endpoint"}`}
          </div>
        </div>
        <span className={"chip " + (overview?.monitoring?.auto_investigate ? "accent" : "")}>{overview?.monitoring?.auto_investigate ? "Auto-investigation on" : "Recommend-only"}</span>
      </div>
      {ai && !ai.reachable && (
        <div className="notice warn" style={{ marginBottom: 12 }}>
          <Icon name="alert" size={15} />
          <div>Monitoring and dashboards keep working. Start Ollama (<span className="mono">ollama serve</span>) or set its address in <a href="#/settings/ai">Settings → AI</a>.</div>
        </div>
      )}
      <form onSubmit={(e) => { e.preventDefault(); ask(q); }} className="row" style={{ marginBottom: 12 }}>
        <input className="input grow" placeholder="Ask about your infrastructure…" value={q} onChange={(e) => setQ(e.target.value)} disabled={!ai?.reachable} />
        <button className="btn primary icon" disabled={!ai?.reachable || !q.trim()} aria-label="Ask"><Icon name="send" /></button>
      </form>
      <div className="col" style={{ gap: 6 }}>
        {["Check all my servers and tell me if anything needs attention.", "Find out what is consuming the most memory.", "Check for security updates on every server."].map((p) => (
          <button key={p} className="btn ghost sm" style={{ justifyContent: "flex-start", height: "auto", padding: "6px 8px", whiteSpace: "normal", textAlign: "left" }} disabled={!ai?.reachable} onClick={() => ask(p)}>
            <Icon name="chevron" size={12} /> {p}
          </button>
        ))}
      </div>
    </Panel>
  );
}

function RecentOps() {
  const { serverById } = useApp();
  const ex = useFetch<Execution[]>("/api/executions?limit=40", [], 15000);
  const changes = (ex.data ?? []).filter((e) => e.risk !== "read").slice(0, 8);
  const reads = (ex.data ?? []).filter((e) => e.risk === "read").length;
  return (
    <Panel title="Recent operations" actions={<a href="#/activity" className="btn ghost sm">Audit log <Icon name="chevron" size={12} /></a>} bodyClass="flush">
      {!ex.data && <SkeletonRows n={4} />}
      {ex.data && changes.length === 0 && <Empty icon="activity" title="No changes yet">{reads ? `${reads} read-only investigations recently; no operation changed a server.` : "Operations performed by you or the AI appear here."}</Empty>}
      {changes.map((e) => (
        <div key={e.id} className="row" style={{ padding: "10px 16px", borderTop: "1px solid var(--line)" }}>
          <span className={"chip sq " + ({ succeeded: "ok", failed: "crit", blocked: "crit", pending_approval: "warn", denied: "", expired: "" } as any)[e.status]}>{e.status.replace("_", " ")}</span>
          <div className="grow" style={{ minWidth: 0 }}>
            <div className="mono ellipsis" style={{ fontSize: 12 }}>{e.tool} {Object.values(e.args ?? {}).join(" ")}</div>
            <div className="faint" style={{ fontSize: "var(--fs-xs)" }}>{serverById(e.server_id)?.name ?? "fleet"} · by {e.requested_by}</div>
          </div>
          <span className="faint nowrap" style={{ fontSize: "var(--fs-xs)" }}>{ago(e.created_at)}</span>
        </div>
      ))}
    </Panel>
  );
}

function FirstRun() {
  const { ai, bootstrap } = useApp();
  return (
    <div className="page ambient-grid" style={{ minHeight: "100%", maxWidth: "none" }}>
      <div style={{ maxWidth: 980, margin: "6vh auto 0" }}>
        <div className="label accent-t" style={{ marginBottom: 10 }}>Welcome to NEXUS</div>
        <h1 style={{ fontFamily: "var(--font-display)", fontSize: 40, fontWeight: 600, letterSpacing: "-0.02em", margin: "0 0 10px" }}>Connect your first Linux server.</h1>
        <p className="dim" style={{ fontSize: 16, maxWidth: 680, marginTop: 0 }}>
          NEXUS manages servers through a small, authenticated agent. It never needs root SSH for monitoring, and its AI runs locally through Ollama.
        </p>
        <div className="grid g3" style={{ marginTop: 28 }}>
          {[
            ["1", "Install the agent", "Copy nexus-agent to the server and run: sudo ./nexus-agent install. It creates a least-privilege service and prints a single-use enrollment code."],
            ["2", "Enroll it here", "Fleet → Add server. Enter the address and the code. NEXUS checks the agent's certificate fingerprint before trusting it."],
            ["3", "Let NEXUS watch", "Telemetry, services, containers, updates and security posture flow in. The AI investigates when something meaningful happens."],
          ].map(([n, t, d]) => (
            <div key={n} className="panel" style={{ padding: 20 }}>
              <div className="mono accent-t" style={{ fontSize: 12 }}>STEP {n}</div>
              <div style={{ fontWeight: 600, fontSize: 16, margin: "6px 0" }}>{t}</div>
              <div className="dim" style={{ fontSize: "var(--fs-sm)" }}>{d}</div>
            </div>
          ))}
        </div>
        <div className="row" style={{ marginTop: 24, gap: 10 }}>
          <a className="btn primary" href="#/fleet?add=1"><Icon name="plus" /> Add server</a>
          <a className="btn" href="#/settings/ai"><Icon name="ai" /> Configure local AI</a>
          <span className="faint" style={{ fontSize: "var(--fs-xs)", marginLeft: 8 }}>
            Ollama: {ai ? (ai.reachable ? `connected (v${ai.version}, ${ai.models} models)` : "not reachable") : "checking…"} · Core identity {bootstrap?.core_fingerprint?.slice(0, 16)}…
          </span>
        </div>
      </div>
    </div>
  );
}

export { bytes };
