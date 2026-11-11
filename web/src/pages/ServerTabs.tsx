import { useEffect, useMemo, useState } from "react";
import { agent, api } from "../lib/api";
import { useApp, useFetch } from "../lib/state";
import { Icon } from "../components/Icon";
import { ActionDialog, Bar, Drawer, Empty, ErrorNote, JSONView, KV, Notice, Panel, RiskChip, Seg, SkeletonRows, Switch, Tabs, useSort } from "../components/UI";
import { ago, bytes, duration, levelClass, pct, sevClass, time } from "../lib/format";
import type { BlockDevice, Execution, Server, Socket } from "../lib/types";

type Act = { tool: string; args: Record<string, unknown>; title: string } | null;

function Unavailable({ s, cap, what }: { s: Server; cap: string; what: string }) {
  if (s.health?.capabilities?.includes(cap)) return null;
  return <Notice kind="info">{what} is not available on {s.name}: the agent did not detect it ({cap}). Capabilities: {(s.health?.capabilities ?? []).join(", ") || "unknown"}.</Notice>;
}

// ---------------------------------------------------------------- processes

export function ProcessesTab({ s }: { s: Server }) {
  const [sort, setSort] = useState<"cpu" | "mem">("cpu");
  const [auto, setAuto] = useState(true);
  const [q, setQ] = useState("");
  const [pid, setPid] = useState<number | null>(null);
  const r = useFetch<any>(agent(s.id, "processes", { sort, limit: 300 }), [sort], auto ? 5000 : undefined);
  const rows = (r.data?.processes ?? []).filter((p: any) => !q || `${p.name} ${p.cmdline} ${p.user} ${p.unit ?? ""} ${p.pid}`.toLowerCase().includes(q.toLowerCase()));
  return (
    <div className="col" style={{ gap: 12 }}>
      <div className="row">
        <input className="input" style={{ width: 300 }} placeholder="Filter by name, command, user, unit, PID" value={q} onChange={(e) => setQ(e.target.value)} />
        <Seg options={[{ id: "cpu", label: "Top CPU" }, { id: "mem", label: "Top memory" }]} value={sort} onChange={setSort} />
        <span className="spacer" />
        <span className="faint" style={{ fontSize: "var(--fs-xs)" }}>{r.data ? `${r.data.total_processes ?? r.data.total} processes · CPU measured over ${r.data.cpu_window_ms} ms` : ""}</span>
        <Switch on={auto} onChange={setAuto} label="Live" />
      </div>
      <ErrorNote error={r.error} />
      {r.data?.partial && <Notice>{r.data.partial}</Notice>}
      <Panel bodyClass="flush">
        {!r.data ? <SkeletonRows /> : (
          <div className="table-wrap" style={{ maxHeight: "62vh" }}>
            <table className="t">
              <thead>
                <tr><th>PID</th><th>Name</th><th>User</th><th className="r">CPU</th><th className="r">RSS</th><th className="r">Mem</th><th>State</th><th>Unit / container</th><th>Command</th><th className="r">Started</th></tr>
              </thead>
              <tbody>
                {rows.map((p: any) => (
                  <tr key={p.pid} className="clickable" onClick={() => setPid(p.pid)}>
                    <td className="mono faint">{p.pid}</td>
                    <td style={{ fontWeight: 500 }}>{p.name}</td>
                    <td className="dim">{p.user}</td>
                    <td className={"r mono " + (p.cpu_pct > 80 ? "crit-t" : p.cpu_pct > 30 ? "warn-t" : "")}>{p.cpu_pct.toFixed(1)}%</td>
                    <td className="r mono">{bytes(p.rss)}</td>
                    <td className="r mono faint">{p.mem_pct.toFixed(1)}%</td>
                    <td className="mono faint">{p.state}</td>
                    <td className="mono" style={{ fontSize: 11 }}>{p.unit ?? (p.container ? <span className="chip outline">ctr {p.container}</span> : "")}</td>
                    <td className="mono faint ellipsis" style={{ maxWidth: 380, fontSize: 11 }}>{p.cmdline || `[${p.name}]`}</td>
                    <td className="r faint" style={{ fontSize: 11 }}>{ago(p.start_time)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Panel>
      {pid !== null && <ProcessDrawer s={s} pid={pid} onClose={() => setPid(null)} />}
    </div>
  );
}

function ProcessDrawer({ s, pid, onClose }: { s: Server; pid: number; onClose: () => void }) {
  const r = useFetch<any>(agent(s.id, `processes/${pid}`), [pid]);
  const d = r.data;
  return (
    <Drawer title={d ? `${d.name} · PID ${pid}` : `PID ${pid}`} sub={d?.cmdline} onClose={onClose}>
      <ErrorNote error={r.error} />
      {!d && !r.error && <SkeletonRows />}
      {d && (
        <div className="col" style={{ gap: 16 }}>
          <KV
            rows={[
              ["User", d.user], ["State", d.state], ["CPU", `${d.cpu_pct.toFixed(1)}%`], ["Resident memory", `${bytes(d.rss)} (${d.mem_pct.toFixed(1)}%)`], ["Virtual", bytes(d.vsize)],
              ["Threads", d.threads], ["Parent", d.ppid], ["Started", `${time(d.start_time)} (${ago(d.start_time)})`], ["Executable", <span className="mono">{d.exe ?? "—"}</span>],
              ["Working dir", <span className="mono">{d.cwd ?? "—"}</span>], ["Open files", d.open_fds >= 0 ? d.open_fds : "permission denied"], ["OOM score", d.oom_score],
              ["I/O", `read ${bytes(d.io_read_bytes)} · write ${bytes(d.io_write_bytes)}`], ["systemd unit", d.unit ?? "—"], ["Container", d.container ?? "—"], ["Children", (d.children ?? []).join(", ") || "none"],
            ]}
          />
          {d.listening?.length > 0 && (
            <Panel title="Listening sockets" bodyClass="flush"><SocketTable socks={d.listening} /></Panel>
          )}
          {d.limits && <Panel title="Limits"><KV rows={Object.entries(d.limits).map(([k, v]) => [k, <span className="mono">{String(v)}</span>])} /></Panel>}
          <Panel title="Status"><KV rows={Object.entries(d.status ?? {}).map(([k, v]) => [k, <span className="mono">{String(v)}</span>])} /></Panel>
          {d.unavailable?.length > 0 && <Notice>Not readable with the agent's privileges: {d.unavailable.join(", ")}</Notice>}
        </div>
      )}
    </Drawer>
  );
}

// ---------------------------------------------------------------- services

export function ServicesTab({ s }: { s: Server }) {
  const [filter, setFilter] = useState<"all" | "running" | "failed" | "inactive">("running");
  const [q, setQ] = useState("");
  const [unit, setUnit] = useState<string | null>(null);
  const [act, setAct] = useState<Act>(null);
  const r = useFetch<any>(agent(s.id, "services"), [], 15000);
  if (r.data && !r.data.available) return <Notice kind="info">{r.data.unavailable}</Notice>;
  const list = (r.data?.services ?? []).filter((x: any) => {
    if (filter === "running" && x.sub_state !== "running") return false;
    if (filter === "failed" && x.active_state !== "failed") return false;
    if (filter === "inactive" && x.active_state !== "inactive") return false;
    return !q || `${x.unit} ${x.description}`.toLowerCase().includes(q.toLowerCase());
  });
  return (
    <div className="col" style={{ gap: 12 }}>
      <Unavailable s={s} cap="services" what="systemd service management" />
      <div className="row">
        <input className="input" style={{ width: 280 }} placeholder="Filter units" value={q} onChange={(e) => setQ(e.target.value)} />
        <Seg options={[{ id: "running", label: `Running ${r.data?.running ?? ""}` }, { id: "failed", label: `Failed ${r.data?.failed ?? ""}` }, { id: "inactive", label: "Inactive" }, { id: "all", label: "All" }]} value={filter} onChange={setFilter} />
      </div>
      <ErrorNote error={r.error} />
      <Panel bodyClass="flush">
        {!r.data ? <SkeletonRows /> : list.length === 0 ? <Empty icon="service" title="No matching units" /> : (
          <div className="table-wrap" style={{ maxHeight: "62vh" }}>
            <table className="t">
              <thead><tr><th>Unit</th><th>Description</th><th>State</th><th>Enabled</th><th className="r">Actions</th></tr></thead>
              <tbody>
                {list.map((x: any) => (
                  <tr key={x.unit} className="clickable" onClick={() => setUnit(x.unit)}>
                    <td className="mono" style={{ fontWeight: 500 }}>{x.unit}</td>
                    <td className="dim ellipsis" style={{ maxWidth: 360 }}>{x.description}</td>
                    <td><span className={"chip " + (x.active_state === "failed" ? "crit" : x.sub_state === "running" ? "ok" : x.active_state === "active" ? "info" : "")}>{x.active_state}/{x.sub_state}</span></td>
                    <td className="faint">{x.unit_file_state || "—"}</td>
                    <td className="r" onClick={(e) => e.stopPropagation()}>
                      <div className="row" style={{ justifyContent: "flex-end", gap: 4 }}>
                        {x.active_state !== "active" && <button className="btn sm" onClick={() => setAct({ tool: "start_service", args: { unit: x.unit }, title: `Start ${x.unit}` })}><Icon name="play" size={12} /> Start</button>}
                        {x.active_state === "active" && <button className="btn sm" onClick={() => setAct({ tool: "restart_service", args: { unit: x.unit }, title: `Restart ${x.unit}` })}><Icon name="restart" size={12} /> Restart</button>}
                        {x.active_state === "active" && <button className="btn sm ghost" onClick={() => setAct({ tool: "stop_service", args: { unit: x.unit }, title: `Stop ${x.unit}` })}><Icon name="stop" size={12} /></button>}
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Panel>
      {unit && <ServiceDrawer s={s} unit={unit} onClose={() => setUnit(null)} onAction={setAct} />}
      {act && <ActionDialog serverId={s.id} {...act} onClose={() => setAct(null)} onDone={() => r.reload()} />}
    </div>
  );
}

function ServiceDrawer({ s, unit, onClose, onAction }: { s: Server; unit: string; onClose: () => void; onAction: (a: Act) => void }) {
  const d = useFetch<any>(agent(s.id, `services/${encodeURIComponent(unit)}`), [unit], 10000);
  const deps = useFetch<any>(agent(s.id, `services/${encodeURIComponent(unit)}/dependencies`), [unit]);
  const logs = useFetch<any>(agent(s.id, "logs", { unit, since: "-24h", limit: 80 }), [unit]);
  const x = d.data;
  return (
    <Drawer
      title={<span className="mono">{unit}</span>}
      sub={x?.description}
      onClose={onClose}
      actions={
        x && (
          <div className="row" style={{ gap: 6 }}>
            <button className="btn sm" onClick={() => onAction({ tool: "reload_service", args: { unit }, title: `Reload ${unit}` })}>Reload</button>
            <button className="btn sm" onClick={() => onAction({ tool: "restart_service", args: { unit }, title: `Restart ${unit}` })}><Icon name="restart" size={12} /> Restart</button>
          </div>
        )
      }
    >
      <ErrorNote error={d.error} />
      {!x && !d.error && <SkeletonRows />}
      {x && (
        <div className="col" style={{ gap: 16 }}>
          <Notice kind={x.health === "healthy" ? "" : x.health === "failed" ? "crit" : "warn"} icon={x.health === "healthy" ? "check" : undefined}>
            <b style={{ textTransform: "capitalize" }}>{x.health}</b> — {(x.health_reasons ?? []).join("; ")}
          </Notice>
          <KV
            rows={[
              ["State", `${x.active_state} / ${x.sub_state} (${x.load_state})`], ["Result", x.result], ["Main PID", x.main_pid || "—"], ["Restarts", x.n_restarts],
              ["Restart policy", x.restart_policy], ["Active since", x.active_enter ? `${time(x.active_enter)} (${ago(x.active_enter)})` : "—"], ["Memory", x.memory_current ? bytes(x.memory_current) : "—"],
              ["CPU time", x.cpu_usage_nsec ? duration(x.cpu_usage_nsec / 1e9) : "—"], ["Tasks", x.tasks_current || "—"], ["Unit file", <span className="mono">{x.fragment_path}</span>], ["Enabled", x.unit_file_state],
              ["Errors (1 h)", x.recent_log_errors], ["Listening", (x.listening_ports ?? []).map((p: Socket) => `${p.proto}/${p.local_port}`).join(", ") || "—"],
            ]}
          />
          <Panel title="Dependencies">
            <DepGraph unit={unit} requires={deps.data?.requires ?? x.requires ?? []} requiredBy={deps.data?.required_by ?? x.required_by ?? []} />
          </Panel>
          <Panel title="Recent logs (24 h)" bodyClass="flush">
            <LogLines entries={logs.data?.entries} source={logs.data?.source} note={logs.data?.note} error={logs.error} />
          </Panel>
        </div>
      )}
    </Drawer>
  );
}

function DepGraph({ unit, requires, requiredBy }: { unit: string; requires: string[]; requiredBy: string[] }) {
  const L = requires.slice(0, 10);
  const R = requiredBy.slice(0, 10);
  const rows = Math.max(L.length, R.length, 1);
  const H = rows * 28 + 20;
  const W = 560;
  const cy = H / 2;
  const y = (i: number, n: number) => (H - n * 28) / 2 + i * 28 + 14;
  return (
    <svg width="100%" viewBox={`0 0 ${W} ${H}`} style={{ display: "block" }}>
      {L.map((_d, i) => <path key={"l" + i} d={`M170 ${y(i, L.length)} C230 ${y(i, L.length)}, 230 ${cy}, 280 ${cy}`} stroke="var(--line-3)" fill="none" />)}
      {R.map((_d, i) => <path key={"r" + i} d={`M280 ${cy} C330 ${cy}, 330 ${y(i, R.length)}, 390 ${y(i, R.length)}`} stroke="var(--accent-line)" fill="none" />)}
      {L.map((d, i) => <text key={"lt" + i} x={165} y={y(i, L.length) + 4} textAnchor="end" fill="var(--text-2)" style={{ font: "11px var(--font-mono)" }}>{d.length > 24 ? d.slice(0, 23) + "…" : d}</text>)}
      {R.map((d, i) => <text key={"rt" + i} x={395} y={y(i, R.length) + 4} fill="var(--text-2)" style={{ font: "11px var(--font-mono)" }}>{d.length > 24 ? d.slice(0, 23) + "…" : d}</text>)}
      <rect x={220} y={cy - 13} width={120} height={26} rx={6} fill="var(--accent-soft)" stroke="var(--accent-line)" />
      <text x={280} y={cy + 4} textAnchor="middle" fill="var(--text)" style={{ font: "600 11px var(--font-mono)" }}>{unit.length > 18 ? unit.slice(0, 17) + "…" : unit}</text>
      <text x={10} y={12} fill="var(--text-3)" style={{ font: "600 9px var(--font-ui)", letterSpacing: ".12em" }}>REQUIRES / WANTS</text>
      <text x={W - 10} y={12} textAnchor="end" fill="var(--text-3)" style={{ font: "600 9px var(--font-ui)", letterSpacing: ".12em" }}>REQUIRED BY</text>
    </svg>
  );
}

// ---------------------------------------------------------------- logs

export function LogLines({ entries, source, note, error }: { entries?: any[]; source?: string; note?: string; error?: string | null }) {
  if (error) return <div style={{ padding: 12 }}><ErrorNote error={error} /></div>;
  if (!entries) return <SkeletonRows n={5} />;
  if (entries.length === 0) return <Empty icon="logs" title="No log entries">{note ?? `Nothing matched in ${source}.`}</Empty>;
  return (
    <div className="logview" style={{ maxHeight: "56vh", overflow: "auto", border: 0, borderRadius: 0 }}>
      {entries.map((e, i) => (
        <div key={i} className={"logline p" + e.priority}>
          <span className="ts">{e.time && !e.time.startsWith("0001") ? new Date(e.time).toLocaleString(undefined, { month: "short", day: "2-digit", hour: "2-digit", minute: "2-digit", second: "2-digit" }) : "—"}</span>
          <span className="id">{e.unit || e.ident}{e.pid ? `[${e.pid}]` : ""}</span>
          <span className="msg">{e.message}</span>
        </div>
      ))}
    </div>
  );
}

export function LogsTab({ s }: { s: Server }) {
  const files = useFetch<any>(agent(s.id, "logs/files"), []);
  const [q, setQ] = useState({ unit: "", grep: "", since: "-1h", priority: "0", limit: "300", file: "" });
  const [applied, setApplied] = useState(q);
  const [follow, setFollow] = useState(false);
  const r = useFetch<any>(agent(s.id, "logs", { ...applied, priority: applied.priority === "0" ? undefined : applied.priority }), [JSON.stringify(applied)], follow ? 5000 : undefined);
  const set = (k: string, v: string) => setQ((p) => ({ ...p, [k]: v }));
  return (
    <div className="col" style={{ gap: 12 }}>
      <form className="row wrap" onSubmit={(e) => { e.preventDefault(); setApplied(q); }}>
        <input className="input mono" style={{ width: 200 }} placeholder="unit (e.g. nginx)" value={q.unit} onChange={(e) => set("unit", e.target.value)} />
        <input className="input mono" style={{ width: 260 }} placeholder="regex filter" value={q.grep} onChange={(e) => set("grep", e.target.value)} />
        <select className="select" value={q.since} onChange={(e) => set("since", e.target.value)}>
          {[["-15m", "15 min"], ["-1h", "1 hour"], ["-6h", "6 hours"], ["-24h", "24 hours"], ["-7d", "7 days"]].map(([v, l]) => <option key={v} value={v}>Last {l}</option>)}
        </select>
        <select className="select" value={q.priority} onChange={(e) => set("priority", e.target.value)}>
          <option value="0">Any priority</option><option value="3">Errors and worse</option><option value="4">Warnings and worse</option><option value="6">Info and worse</option>
        </select>
        {files.data && !files.data.journald && (
          <select className="select" value={q.file} onChange={(e) => set("file", e.target.value)}>
            <option value="">Default system log</option>
            {(files.data.files ?? []).map((f: string) => <option key={f} value={f}>{f}</option>)}
          </select>
        )}
        <button className="btn primary"><Icon name="search" /> Query</button>
        <span className="spacer" />
        <Switch on={follow} onChange={setFollow} label="Follow" />
      </form>
      <Panel title={r.data ? `${r.data.entries.length} entries · ${r.data.source}${r.data.truncated ? " · truncated" : ""}` : "Logs"} bodyClass="flush">
        <LogLines entries={r.data?.entries} source={r.data?.source} note={r.data?.note} error={r.error} />
      </Panel>
    </div>
  );
}

// ---------------------------------------------------------------- docker

export function DockerTab({ s }: { s: Server }) {
  const [sub, setSub] = useState<"containers" | "images" | "volumes" | "networks" | "compose">("containers");
  const info = useFetch<any>(agent(s.id, "docker"), [], 30000);
  const [act, setAct] = useState<Act>(null);
  const [ctr, setCtr] = useState<string | null>(null);
  const list = useFetch<any[]>(sub === "containers" ? agent(s.id, "docker/containers") : null, [sub], 8000);
  const other = useFetch<any[]>(sub !== "containers" ? agent(s.id, "docker/" + sub) : null, [sub]);
  if (info.data && !info.data.available) return <Notice kind="info">Docker is not available: {info.data.unavailable}</Notice>;
  const i = info.data;
  return (
    <div className="col" style={{ gap: 12 }}>
      {i && (
        <div className="kpis">
          {[
            ["Engine", i.server_version, `API ${i.api_version} · ${i.storage_driver}`],
            ["Running", i.running, `${i.containers} containers total`],
            ["Stopped", i.stopped, `${i.paused} paused`],
            ["Images", i.images, i.docker_root_dir],
          ].map(([l, v, sub2]) => (
            <div className="kpi" key={l as string}><div className="label">{l}</div><div className="kpi-v" style={{ fontSize: 24 }}>{v}</div><div className="kpi-s ellipsis">{sub2}</div></div>
          ))}
        </div>
      )}
      {i?.warnings?.length > 0 && <Notice kind="warn">{i.warnings.join(" · ")}</Notice>}
      <Tabs tabs={[{ id: "containers", label: "Containers" }, { id: "compose", label: "Compose projects" }, { id: "images", label: "Images" }, { id: "volumes", label: "Volumes" }, { id: "networks", label: "Networks" }]} value={sub} onChange={setSub} />
      {sub === "containers" && (
        <Panel bodyClass="flush">
          <ErrorNote error={list.error} />
          {!list.data ? <SkeletonRows /> : list.data.length === 0 ? <Empty icon="docker" title="No containers" /> : (
            <div className="table-wrap">
              <table className="t">
                <thead><tr><th>Name</th><th>Image</th><th>State</th><th>Ports</th><th>Project</th><th className="r">Created</th><th className="r">Actions</th></tr></thead>
                <tbody>
                  {list.data.map((c) => (
                    <tr key={c.id} className="clickable" onClick={() => setCtr(c.name)}>
                      <td style={{ fontWeight: 500 }}>{c.name}<div className="faint mono" style={{ fontSize: 11 }}>{c.id}</div></td>
                      <td className="mono" style={{ fontSize: 12 }}>{c.image}</td>
                      <td>
                        <span className={"chip " + (c.health === "unhealthy" || (c.state === "exited" && !/Exited \(0\)/.test(c.status)) ? "crit" : c.state === "running" ? (c.health === "starting" ? "warn" : "ok") : "")}>{c.state}{c.health ? ` · ${c.health}` : ""}</span>
                        <div className="faint" style={{ fontSize: 11 }}>{c.status}</div>
                      </td>
                      <td className="mono" style={{ fontSize: 11 }}>{(c.ports ?? []).filter((p: any) => p.public_port).map((p: any) => `${p.ip ? p.ip + ":" : ""}${p.public_port}→${p.private_port}/${p.type}`).join(" ") || "—"}</td>
                      <td className="dim">{c.compose_project || "—"}</td>
                      <td className="r faint" style={{ fontSize: 12 }}>{ago(c.created)}</td>
                      <td className="r" onClick={(e) => e.stopPropagation()}>
                        <div className="row" style={{ justifyContent: "flex-end", gap: 4 }}>
                          {c.state !== "running" && <button className="btn sm" onClick={() => setAct({ tool: "start_container", args: { container: c.name }, title: `Start ${c.name}` })}><Icon name="play" size={12} /> Start</button>}
                          {c.state === "running" && <button className="btn sm" onClick={() => setAct({ tool: "restart_container", args: { container: c.name }, title: `Restart ${c.name}` })}><Icon name="restart" size={12} /> Restart</button>}
                          {c.state === "running" && <button className="btn sm ghost" onClick={() => setAct({ tool: "stop_container", args: { container: c.name }, title: `Stop ${c.name}` })}><Icon name="stop" size={12} /></button>}
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </Panel>
      )}
      {sub !== "containers" && (
        <Panel bodyClass="flush">
          <ErrorNote error={other.error} />
          {!other.data ? <SkeletonRows /> : other.data.length === 0 ? <Empty icon="docker" title={`No ${sub}`} /> : <DockerList kind={sub} rows={other.data} />}
        </Panel>
      )}
      {ctr && <ContainerDrawer s={s} name={ctr} onClose={() => setCtr(null)} onAction={setAct} />}
      {act && <ActionDialog serverId={s.id} {...act} onClose={() => setAct(null)} onDone={() => list.reload()} />}
    </div>
  );
}

function DockerList({ kind, rows }: { kind: string; rows: any[] }) {
  if (kind === "images")
    return (
      <table className="t">
        <thead><tr><th>Tags</th><th>ID</th><th className="r">Size</th><th className="r">Containers</th><th className="r">Created</th></tr></thead>
        <tbody>{rows.map((r) => <tr key={r.id}><td className="mono" style={{ fontSize: 12 }}>{r.tags?.length ? r.tags.join(", ") : <span className="warn-t">&lt;dangling&gt;</span>}</td><td className="mono faint">{r.id}</td><td className="r mono">{bytes(r.size)}</td><td className="r">{r.containers}</td><td className="r faint">{ago(r.created)}</td></tr>)}</tbody>
      </table>
    );
  if (kind === "volumes")
    return (
      <table className="t">
        <thead><tr><th>Name</th><th>Driver</th><th>Mountpoint</th><th>Project</th></tr></thead>
        <tbody>{rows.map((r) => <tr key={r.name}><td className="mono ellipsis" style={{ maxWidth: 280, fontSize: 12 }}>{r.name}</td><td>{r.driver}</td><td className="mono faint ellipsis" style={{ maxWidth: 360, fontSize: 11 }}>{r.mountpoint}</td><td className="dim">{r.labels?.["com.docker.compose.project"] ?? "—"}</td></tr>)}</tbody>
      </table>
    );
  if (kind === "networks")
    return (
      <table className="t">
        <thead><tr><th>Name</th><th>Driver</th><th>Subnets</th><th>Containers</th></tr></thead>
        <tbody>{rows.map((r) => <tr key={r.id}><td className="mono">{r.name}{r.internal && <span className="chip outline" style={{ marginLeft: 6 }}>internal</span>}</td><td>{r.driver}</td><td className="mono" style={{ fontSize: 12 }}>{(r.subnets ?? []).join(", ") || "—"}</td><td className="dim">{(r.containers ?? []).join(", ") || "—"}</td></tr>)}</tbody>
      </table>
    );
  return (
    <table className="t">
      <thead><tr><th>Project</th><th>Services</th><th>Running</th><th>Working directory</th><th>Config files</th></tr></thead>
      <tbody>{rows.map((r) => <tr key={r.name}><td style={{ fontWeight: 500 }}>{r.name}</td><td className="dim">{(r.services ?? []).join(", ")}</td><td><span className={"chip " + (r.running === r.containers ? "ok" : "warn")}>{r.running}/{r.containers}</span></td><td className="mono faint" style={{ fontSize: 11 }}>{r.working_dir}</td><td className="mono faint" style={{ fontSize: 11 }}>{(r.config_files ?? []).join(", ")}</td></tr>)}</tbody>
    </table>
  );
}

function ContainerDrawer({ s, name, onClose, onAction }: { s: Server; name: string; onClose: () => void; onAction: (a: Act) => void }) {
  const d = useFetch<any>(agent(s.id, `docker/containers/${name}`), [name], 10000);
  const logs = useFetch<any>(agent(s.id, `docker/containers/${name}/logs`, { tail: 200 }), [name], 10000);
  const c = d.data;
  return (
    <Drawer title={name} sub={c?.image} onClose={onClose} actions={c && <button className="btn sm" onClick={() => onAction({ tool: "restart_container", args: { container: name }, title: `Restart ${name}` })}><Icon name="restart" size={12} /> Restart</button>}>
      <ErrorNote error={d.error} />
      {!c && !d.error && <SkeletonRows />}
      {c && (
        <div className="col" style={{ gap: 16 }}>
          {c.stats && (
            <div className="kpis">
              <div className="kpi"><div className="label">CPU</div><div className="kpi-v" style={{ fontSize: 22 }}>{pct(c.stats.cpu_pct)}</div></div>
              <div className="kpi"><div className="label">Memory</div><div className="kpi-v" style={{ fontSize: 22 }}>{bytes(c.stats.mem_usage)}</div><div className="kpi-s">{c.stats.mem_limit ? `${pct(c.stats.mem_pct)} of ${bytes(c.stats.mem_limit)}` : ""}</div></div>
              <div className="kpi"><div className="label">Network</div><div className="kpi-v" style={{ fontSize: 16 }}>↓{bytes(c.stats.net_rx)} ↑{bytes(c.stats.net_tx)}</div></div>
              <div className="kpi"><div className="label">PIDs</div><div className="kpi-v" style={{ fontSize: 22 }}>{c.stats.pids}</div></div>
            </div>
          )}
          {c.oom_killed && <Notice kind="crit">The last exit was an out-of-memory kill.</Notice>}
          <KV
            rows={[
              ["State", `${c.state}${c.health ? ` · health ${c.health}` : ""}`], ["Exit code", c.exit_code], ["Restarts", c.restart_count], ["Restart policy", c.restart_policy || "no"],
              ["Started", c.started_at ? `${time(c.started_at)} (${ago(c.started_at)})` : "—"], ["Finished", c.finished_at ? time(c.finished_at) : "—"], ["Command", <span className="mono">{c.command}</span>],
              ["Ports", (c.ports ?? []).map((p: any) => `${p.public_port ? p.public_port + "→" : ""}${p.private_port}/${p.type}`).join(", ") || "—"], ["Networks", (c.networks ?? []).join(", ")],
              ["Mounts", <div className="mono" style={{ fontSize: 11 }}>{(c.mounts ?? []).map((m: string) => <div key={m}>{m}</div>)}</div>],
              ["Environment", <span className="faint">{(c.env_keys ?? []).length} variables (values hidden)</span>], ["Compose", c.compose_project ? `${c.compose_project} / ${c.compose_service}` : "—"],
            ]}
          />
          {c.health_log?.length > 0 && <Panel title="Health checks"><pre className="code">{c.health_log.join("\n")}</pre></Panel>}
          <Panel title="Logs" bodyClass="flush"><LogLines entries={logs.data?.entries} source={logs.data?.source} error={logs.error} /></Panel>
        </div>
      )}
    </Drawer>
  );
}

// ---------------------------------------------------------------- storage

export function StorageTab({ s }: { s: Server }) {
  const ov = useFetch<any>(agent(s.id, "storage"), [], 30000);
  const [path, setPath] = useState("/");
  const [scan, setScan] = useState<string | null>(null);
  const du = useFetch<any>(scan ? agent(s.id, "storage/usage", { path: scan, depth: 1, privileged: 1 }) : null, [scan]);
  const [lfq, setLfq] = useState({ path: "/var", min_mb: "100" });
  const [lfRun, setLfRun] = useState<any>(null);
  const lf = useFetch<any>(lfRun ? agent(s.id, "storage/large-files", { ...lfRun, limit: 50, privileged: 1 }) : null, [JSON.stringify(lfRun)]);
  const root = du.data?.root;
  return (
    <div className="col" style={{ gap: 16 }}>
      <ErrorNote error={ov.error} />
      <div className="grid g2">
        <Panel title="Filesystems" bodyClass="flush">
          {!ov.data ? <SkeletonRows /> : (
            <table className="t">
              <thead><tr><th>Mount</th><th style={{ width: "38%" }}>Capacity</th><th style={{ width: "22%" }}>Inodes</th><th className="r">Free</th></tr></thead>
              <tbody>
                {ov.data.filesystems.map((f: any) => (
                  <tr key={f.mount} className="clickable" onClick={() => { setPath(f.mount); setScan(f.mount); }}>
                    <td><div className="mono">{f.mount}</div><div className="faint mono" style={{ fontSize: 11 }}>{f.device} · {f.fstype}</div></td>
                    <td><div className="row"><Bar thick value={f.used_pct} cls={levelClass(f.used_pct, 85, 95)} /><span className="mono" style={{ width: 48, textAlign: "right", fontSize: 12 }}>{pct(f.used_pct, 0)}</span></div></td>
                    <td><div className="row"><Bar value={f.inodes_pct} cls={levelClass(f.inodes_pct, 85, 95)} /><span className="mono faint" style={{ width: 40, textAlign: "right", fontSize: 11 }}>{f.inodes_total ? pct(f.inodes_pct, 0) : "n/a"}</span></div></td>
                    <td className="r mono" style={{ fontSize: 12 }}>{bytes(f.avail)}<div className="faint">of {bytes(f.total)}</div></td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </Panel>
        <Panel title="Block devices">
          {!ov.data ? <SkeletonRows /> : <DevTree devs={ov.data.block_devices ?? []} />}
        </Panel>
      </div>
      <Panel title="Directory analysis" actions={<span className="faint" style={{ fontSize: "var(--fs-xs)" }}>Read-only scan · does not cross filesystems</span>}>
        <form className="row" onSubmit={(e) => { e.preventDefault(); setScan(path); }}>
          <input className="input mono grow" value={path} onChange={(e) => setPath(e.target.value)} />
          <button className="btn primary" disabled={du.loading}>{du.loading ? <Icon name="refresh" className="spin" /> : <Icon name="search" />} Analyse</button>
        </form>
        <ErrorNote error={du.error} />
        {root && (
          <div style={{ marginTop: 14 }}>
            <div className="row" style={{ marginBottom: 10 }}>
              {root.path !== "/" && <button className="btn sm ghost" onClick={() => { const p = root.path.replace(/\/[^/]+$/, "") || "/"; setPath(p); setScan(p); }}>↑ Up</button>}
              <b className="mono">{root.path}</b>
              <span className="dim">{bytes(root.size)} in {root.files.toLocaleString()} files</span>
              <span className="faint" style={{ fontSize: "var(--fs-xs)" }}>scanned in {du.data.scanned_ms} ms{du.data.errors ? ` · ${du.data.errors} unreadable` : ""}{du.data.truncated ? " · truncated" : ""}</span>
            </div>
            {(root.children ?? []).map((c: any) => (
              <div key={c.path} className="row clickable" style={{ padding: "5px 0", cursor: "pointer" }} onClick={() => { setPath(c.path); setScan(c.path); }}>
                <span className="mono ellipsis" style={{ width: 320, fontSize: 12 }}>{c.path}</span>
                <div className="grow"><Bar thick value={root.size ? (c.size / root.size) * 100 : 0} /></div>
                <span className="mono" style={{ width: 90, textAlign: "right", fontSize: 12 }}>{bytes(c.size)}</span>
                <span className="mono faint" style={{ width: 110, textAlign: "right", fontSize: 11 }}>{c.files.toLocaleString()} files</span>
              </div>
            ))}
          </div>
        )}
      </Panel>
      <Panel title="Large files" actions={<span className="faint" style={{ fontSize: "var(--fs-xs)" }}>Report only — NEXUS never deletes files because they are large</span>}>
        <form className="row" onSubmit={(e) => { e.preventDefault(); setLfRun({ ...lfq }); }}>
          <input className="input mono grow" value={lfq.path} onChange={(e) => setLfq({ ...lfq, path: e.target.value })} />
          <select className="select" value={lfq.min_mb} onChange={(e) => setLfq({ ...lfq, min_mb: e.target.value })}>
            {["10", "50", "100", "500", "1024"].map((m) => <option key={m} value={m}>≥ {m} MiB</option>)}
          </select>
          <button className="btn primary" disabled={lf.loading}>{lf.loading ? <Icon name="refresh" className="spin" /> : <Icon name="search" />} Find</button>
        </form>
        <ErrorNote error={lf.error} />
        {lf.data && (
          <div style={{ marginTop: 12 }}>
            <div className="faint" style={{ fontSize: "var(--fs-xs)", marginBottom: 8 }}>{lf.data.files.length} files · {lf.data.scanned.toLocaleString()} scanned in {lf.data.scanned_ms} ms{lf.data.errors ? ` · ${lf.data.errors} unreadable` : ""}</div>
            <table className="t">
              <thead><tr><th>Path</th><th>Owner</th><th className="r">Modified</th><th className="r">Size</th></tr></thead>
              <tbody>{lf.data.files.map((f: any) => <tr key={f.path}><td className="mono" style={{ fontSize: 12 }}>{f.path}</td><td className="dim">{f.owner}</td><td className="r faint">{ago(f.mod_time)}</td><td className="r mono">{bytes(f.size)}</td></tr>)}</tbody>
            </table>
          </div>
        )}
      </Panel>
    </div>
  );
}

function DevTree({ devs, depth = 0 }: { devs: BlockDevice[]; depth?: number }) {
  return (
    <div className="col" style={{ gap: 4 }}>
      {devs.map((d) => (
        <div key={d.name}>
          <div className="row" style={{ paddingLeft: depth * 18, fontSize: 12 }}>
            <Icon name={d.type === "part" ? "chevron" : "disk"} size={13} />
            <span className="mono" style={{ fontWeight: depth ? 400 : 600 }}>{d.name}</span>
            <span className="faint">{d.type}</span>
            {d.model && <span className="dim">{d.model}</span>}
            {d.fstype && <span className="chip outline">{d.fstype}</span>}
            {d.mounts?.map((m) => <span key={m} className="mono accent-t">{m}</span>)}
            <span className="spacer" />
            <span className="mono">{bytes(d.size)}</span>
            {depth === 0 && <span className="faint">{d.rotational ? "HDD" : "SSD/virtual"}</span>}
          </div>
          {d.children && <DevTree devs={d.children} depth={depth + 1} />}
        </div>
      ))}
    </div>
  );
}

// ---------------------------------------------------------------- network

function SocketTable({ socks }: { socks: Socket[] }) {
  return (
    <table className="t">
      <thead><tr><th>Port</th><th>Proto</th><th>Bind address</th><th>Scope</th><th>Process</th><th>User</th></tr></thead>
      <tbody>
        {socks.map((x, i) => (
          <tr key={i}>
            <td className="mono" style={{ fontWeight: 600 }}>{x.local_port}</td>
            <td className="mono faint">{x.proto}</td>
            <td className="mono">{x.local_addr}</td>
            <td><span className={"chip " + (x.scope === "loopback" ? "ok" : x.scope === "all-interfaces" ? "warn" : "info")}>{x.scope}</span></td>
            <td>{x.process ?? <span className="faint">unknown</span>}{x.pid ? <span className="faint mono"> [{x.pid}]</span> : null}</td>
            <td className="dim">{x.user ?? "—"}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

export function NetworkTab({ s }: { s: Server }) {
  const n = useFetch<any>(agent(s.id, "network"), [], 30000);
  const fw = useFetch<any>(agent(s.id, "network/firewall"), []);
  const d = n.data;
  return (
    <div className="col" style={{ gap: 16 }}>
      <ErrorNote error={n.error} />
      <div className="grid g2">
        <Panel title="Interfaces" bodyClass="flush">
          {!d ? <SkeletonRows /> : (
            <table className="t">
              <thead><tr><th>Interface</th><th>State</th><th>Addresses</th><th className="r">MTU</th></tr></thead>
              <tbody>{d.interfaces.map((i: any) => <tr key={i.name}><td><span className="mono" style={{ fontWeight: 500 }}>{i.name}</span>{i.virtual && <span className="faint"> · virtual</span>}<div className="faint mono" style={{ fontSize: 11 }}>{i.mac}{i.speed_mbps ? ` · ${i.speed_mbps} Mb/s` : ""}</div></td><td><span className={"chip " + (i.oper_state === "up" ? "ok" : i.oper_state === "down" ? "" : "info")}>{i.oper_state}</span></td><td className="mono" style={{ fontSize: 12 }}>{(i.addrs ?? []).join(", ") || "—"}</td><td className="r mono faint">{i.mtu}</td></tr>)}</tbody>
            </table>
          )}
        </Panel>
        <Panel title="Routing & DNS">
          {!d ? <SkeletonRows /> : (
            <div className="col" style={{ gap: 12 }}>
              <KV rows={[["Default gateway", d.routes.filter((r: any) => r.default).map((r: any) => `${r.gateway} via ${r.iface}`).join(", ") || "none"], ["DNS servers", d.dns_servers.join(", ") || "none"], ["Search domains", (d.dns_search ?? []).join(", ") || "—"], ["Established TCP", d.tcp_established]]} />
              <table className="t">
                <thead><tr><th>Destination</th><th>Gateway</th><th>Interface</th><th className="r">Metric</th></tr></thead>
                <tbody>{d.routes.slice(0, 20).map((r: any, i: number) => <tr key={i}><td className="mono" style={{ fontSize: 12 }}>{r.destination}</td><td className="mono faint" style={{ fontSize: 12 }}>{r.gateway ?? "link"}</td><td className="mono">{r.iface}</td><td className="r faint">{r.metric}</td></tr>)}</tbody>
              </table>
            </div>
          )}
        </Panel>
      </div>
      <Panel title={`Listening sockets${d ? ` (${d.listening.length})` : ""}`} bodyClass="flush">
        <div style={{ padding: "0 16px 10px" }}>
          <Notice>Scope describes the bind address only. "all-interfaces" means the service accepts connections on every local address; whether it is reachable from the internet depends on the firewall and any upstream NAT.</Notice>
        </div>
        {d?.partial && <div style={{ padding: "0 16px 10px" }}><Notice kind="warn">{d.partial}</Notice></div>}
        {!d ? <SkeletonRows /> : <SocketTable socks={d.listening} />}
      </Panel>
      <Panel title="Host firewall" actions={fw.data && <span className={"chip " + (fw.data.active ? "ok" : "warn")}>{fw.data.backend} · {fw.data.active ? "active" : "inactive"}</span>}>
        <ErrorNote error={fw.error} />
        {!fw.data ? <SkeletonRows n={3} /> : (
          <div className="col" style={{ gap: 12 }}>
            <KV rows={[["Detected tools", (fw.data.detected ?? []).join(", ") || "none"], ["Default incoming", fw.data.default_incoming || "—"], ["Default outgoing", fw.data.default_outgoing || "—"], ["Rules", fw.data.rules.length]]} />
            {fw.data.unavailable && <Notice kind="warn">{fw.data.unavailable}</Notice>}
            {fw.data.rules.length > 0 && <pre className="code" style={{ maxHeight: 300 }}>{fw.data.rules.join("\n")}</pre>}
            <div className="faint" style={{ fontSize: "var(--fs-xs)" }}>Firewall changes are high-risk operations and are never made automatically.</div>
          </div>
        )}
      </Panel>
    </div>
  );
}

// ---------------------------------------------------------------- packages

export function PackagesTab({ s }: { s: Server }) {
  const [sub, setSub] = useState<"updates" | "installed" | "history">("updates");
  const upd = useFetch<any>(agent(s.id, "packages/updates"), []);
  const [q, setQ] = useState("");
  const [applied, setApplied] = useState("");
  const inst = useFetch<any>(sub === "installed" ? agent(s.id, "packages", { q: applied }) : null, [sub, applied]);
  const hist = useFetch<any[]>(sub === "history" ? agent(s.id, "packages/history", { limit: 50 }) : null, [sub]);
  const [act, setAct] = useState<Act>(null);
  const [sel, setSel] = useState<Set<string>>(new Set());
  const u = upd.data;
  const { sorted, th } = useSort<any>(u?.updates ?? [], { key: "security", dir: -1 });
  if (!s.health?.capabilities?.includes("packages:apt")) return <Notice kind="info">Package management is implemented for APT (Debian, Ubuntu). {s.info?.package_manager ? `${s.name} uses ${s.info.package_manager}, which NEXUS detects but does not manage yet.` : "No supported package manager was detected."}</Notice>;
  return (
    <div className="col" style={{ gap: 12 }}>
      <Tabs tabs={[{ id: "updates", label: "Updates", badge: u ? <span className={"chip " + (u.security_count ? "warn" : "")}>{u.updates.length}</span> : undefined }, { id: "installed", label: "Installed" }, { id: "history", label: "History" }]} value={sub} onChange={setSub} />
      {sub === "updates" && (
        <>
          <ErrorNote error={upd.error} />
          {u && (
            <div className="row wrap">
              <span className="dim">{u.updates.length} pending · <b className={u.security_count ? "warn-t" : ""}>{u.security_count} security</b>{u.kernel_update && " · kernel update"}{u.reboot_required && <span className="warn-t"> · reboot required</span>}</span>
              <span className="faint" style={{ fontSize: "var(--fs-xs)" }}>Package lists {u.lists_updated_at ? `refreshed ${ago(u.lists_updated_at)}` : "never refreshed"}</span>
              <span className="spacer" />
              <button className="btn" onClick={() => setAct({ tool: "refresh_package_lists", args: {}, title: "Refresh package lists" })}><Icon name="refresh" /> Refresh lists</button>
              <button className="btn" disabled={sel.size === 0} onClick={() => setAct({ tool: "apply_package_updates", args: { packages: [...sel].join(" ") }, title: `Upgrade ${sel.size} package(s)` })}>Upgrade selected ({sel.size})</button>
              <button className="btn primary" disabled={!u.security_count} onClick={() => setAct({ tool: "apply_security_updates", args: {}, title: "Apply security updates" })}><Icon name="shield" /> Apply security updates</button>
            </div>
          )}
          {u?.note && <Notice>{u.note}</Notice>}
          <Panel bodyClass="flush">
            {!u ? <SkeletonRows /> : u.updates.length === 0 ? <Empty icon="check" title="Up to date">The simulated upgrade found nothing to install. Refresh package lists to check the repositories again.</Empty> : (
              <div className="table-wrap" style={{ maxHeight: "56vh" }}>
                <table className="t">
                  <thead><tr><th style={{ width: 30 }} />{th("name", "Package")}<th>Installed</th><th>Candidate</th>{th("security", "Type")}<th>Origin</th></tr></thead>
                  <tbody>
                    {sorted.map((x: any) => (
                      <tr key={x.name}>
                        <td><input type="checkbox" checked={sel.has(x.name)} onChange={(e) => { const n = new Set(sel); e.target.checked ? n.add(x.name) : n.delete(x.name); setSel(n); }} /></td>
                        <td className="mono" style={{ fontWeight: 500 }}>{x.name}</td>
                        <td className="mono faint" style={{ fontSize: 12 }}>{x.current || "new"}</td>
                        <td className="mono" style={{ fontSize: 12 }}>{x.candidate}</td>
                        <td>{x.security ? <span className="chip warn">security</span> : <span className="chip">regular</span>}</td>
                        <td className="faint" style={{ fontSize: 11 }}>{x.origin}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </Panel>
        </>
      )}
      {sub === "installed" && (
        <>
          <form className="row" onSubmit={(e) => { e.preventDefault(); setApplied(q); }}>
            <input className="input" style={{ width: 320 }} placeholder="Search packages" value={q} onChange={(e) => setQ(e.target.value)} />
            <button className="btn">Search</button>
            <span className="faint">{inst.data ? `${inst.data.packages.length} of ${inst.data.total}` : ""}</span>
          </form>
          <Panel bodyClass="flush">
            {!inst.data ? <SkeletonRows /> : (
              <div className="table-wrap" style={{ maxHeight: "60vh" }}>
                <table className="t">
                  <thead><tr><th>Package</th><th>Version</th><th>Arch</th><th className="r">Size</th><th>Summary</th></tr></thead>
                  <tbody>{inst.data.packages.slice(0, 1500).map((p: any) => <tr key={p.name + p.arch}><td className="mono">{p.name}</td><td className="mono faint" style={{ fontSize: 12 }}>{p.version}</td><td className="faint">{p.arch}</td><td className="r mono faint">{bytes(p.installed_kib * 1024)}</td><td className="dim ellipsis" style={{ maxWidth: 420 }}>{p.summary}</td></tr>)}</tbody>
                </table>
              </div>
            )}
          </Panel>
        </>
      )}
      {sub === "history" && (
        <Panel bodyClass="flush">
          {!hist.data ? <SkeletonRows /> : hist.data.length === 0 ? <Empty icon="package" title="No APT history" /> : (
            <table className="t">
              <thead><tr><th>When</th><th>Command</th><th>Changes</th></tr></thead>
              <tbody>{hist.data.map((h: any, i: number) => <tr key={i}><td className="nowrap">{time(h.start)}<div className="faint" style={{ fontSize: 11 }}>{h.requested_by}</div></td><td className="mono" style={{ fontSize: 11 }}>{h.command_line}</td><td className="dim" style={{ fontSize: 12 }}>{h.upgrade?.length ? `${h.upgrade.length} upgraded ` : ""}{h.install?.length ? `${h.install.length} installed ` : ""}{h.remove?.length ? `${h.remove.length} removed` : ""}{h.error && <span className="crit-t"> · {h.error}</span>}</td></tr>)}</tbody>
            </table>
          )}
        </Panel>
      )}
      {act && <ActionDialog serverId={s.id} {...act} onClose={() => setAct(null)} onDone={() => { upd.reload(); setSel(new Set()); }} />}
    </div>
  );
}

// ---------------------------------------------------------------- security

export function SecurityTab({ s }: { s: Server }) {
  const post = useFetch<any>(agent(s.id, "security/posture"), []);
  const ssh = useFetch<any>(agent(s.id, "security/ssh"), []);
  const [hours, setHours] = useState(24);
  const auth = useFetch<any>(agent(s.id, "security/auth", { hours }), [hours]);
  const users = useFetch<any[]>(agent(s.id, "security/users"), []);
  const sevOrder: Record<string, number> = { critical: 0, high: 1, medium: 2, low: 3, info: 4 };
  const findings = [...(post.data?.findings ?? [])].sort((a: any, b: any) => sevOrder[a.severity] - sevOrder[b.severity]);
  const counts = findings.reduce((m: any, f: any) => ((m[f.severity] = (m[f.severity] ?? 0) + 1), m), {});
  return (
    <div className="col" style={{ gap: 16 }}>
      <div className="grid" style={{ gridTemplateColumns: "minmax(0,1.4fr) minmax(0,1fr)" }}>
        <Panel title="Security posture" actions={post.data && <span className="faint" style={{ fontSize: "var(--fs-xs)" }}>checked {ago(post.data.checked_at)}</span>}>
          <ErrorNote error={post.error} />
          {!post.data ? <SkeletonRows /> : (
            <div className="col" style={{ gap: 10 }}>
              <div className="row wrap">{["critical", "high", "medium", "low", "info"].map((k) => <span key={k} className={"chip " + sevClass(k === "high" ? "critical" : k === "medium" ? "warning" : k)}>{counts[k] ?? 0} {k}</span>)}</div>
              {findings.map((f: any) => (
                <div key={f.id} style={{ padding: "10px 12px", borderRadius: "var(--radius)", background: "var(--surface-2)", borderLeft: `3px solid var(--${f.severity === "critical" || f.severity === "high" ? "crit" : f.severity === "medium" ? "warn" : f.severity === "low" ? "info" : "line-3"})` }}>
                  <div className="row"><b className="grow">{f.title}</b><span className="chip sq">{f.severity}</span></div>
                  <div className="mono dim" style={{ fontSize: 11.5, margin: "4px 0" }}>{f.evidence}</div>
                  {f.advice && <div className="faint" style={{ fontSize: "var(--fs-sm)" }}>{f.advice}</div>}
                </div>
              ))}
            </div>
          )}
        </Panel>
        <div className="col" style={{ gap: 16 }}>
          <Panel title="Security tooling">
            {post.data && <div className="row wrap">{Object.entries(post.data.tools).map(([k, v]) => <span key={k} className={"chip " + (v ? "ok" : "outline")}>{v ? "✓" : "–"} {k}</span>)}</div>}
          </Panel>
          <Panel title="SSH server">
            {ssh.error ? <Notice kind="info">{ssh.error}</Notice> : !ssh.data ? <SkeletonRows n={4} /> : (
              <>
                <div className="faint" style={{ fontSize: "var(--fs-xs)", marginBottom: 8 }}>{ssh.data.source}</div>
                <KV rows={["port", "permitrootlogin", "passwordauthentication", "pubkeyauthentication", "kbdinteractiveauthentication", "maxauthtries", "x11forwarding", "allowusers"].filter((k) => ssh.data.settings[k] !== undefined).map((k) => [k, <span className="mono">{ssh.data.settings[k]}</span>])} />
              </>
            )}
          </Panel>
        </div>
      </div>
      <Panel title="Authentication activity" actions={<Seg options={[{ id: "1", label: "1h" }, { id: "24", label: "24h" }, { id: "168", label: "7d" }]} value={String(hours) as any} onChange={(v) => setHours(Number(v))} />}>
        <ErrorNote error={auth.error} />
        {!auth.data ? <SkeletonRows /> : auth.data.note ? <Notice>{auth.data.note}</Notice> : (
          <div className="col" style={{ gap: 14 }}>
            <div className="kpis">
              <div className="kpi"><div className="label">Failed SSH</div><div className={"kpi-v " + (auth.data.failed > 50 ? "crit-t" : auth.data.failed ? "warn-t" : "")} style={{ fontSize: 26 }}>{auth.data.failed}</div><div className="kpi-s">{Object.keys(auth.data.failed_by_source).length} sources</div></div>
              <div className="kpi"><div className="label">Accepted logins</div><div className="kpi-v" style={{ fontSize: 26 }}>{auth.data.accepted}</div></div>
              <div className="kpi"><div className="label">sudo events</div><div className="kpi-v" style={{ fontSize: 26 }}>{auth.data.sudo}</div></div>
              <div className="kpi"><div className="label">Source</div><div className="kpi-v" style={{ fontSize: 16 }}>{auth.data.source}</div></div>
            </div>
            <div className="grid g2">
              <TopList title="Top failing sources" data={auth.data.failed_by_source} />
              <TopList title="Top targeted users" data={auth.data.failed_by_user} />
            </div>
            <div className="table-wrap" style={{ maxHeight: 300 }}>
              <table className="t">
                <thead><tr><th>Time</th><th>Event</th><th>User</th><th>Source</th><th>Detail</th></tr></thead>
                <tbody>{auth.data.events.slice(0, 200).map((e: any, i: number) => <tr key={i}><td className="nowrap faint" style={{ fontSize: 12 }}>{time(e.time)}</td><td><span className={"chip " + (e.kind === "ssh_accepted" ? "ok" : e.kind.startsWith("sudo") ? "info" : "warn")}>{e.kind}</span></td><td className="mono">{e.user}</td><td className="mono">{e.source ?? ""}</td><td className="mono faint ellipsis" style={{ maxWidth: 360, fontSize: 11 }}>{e.command ?? e.method ?? ""}</td></tr>)}</tbody>
              </table>
            </div>
          </div>
        )}
      </Panel>
      <Panel title="Local accounts" bodyClass="flush">
        {!users.data ? <SkeletonRows /> : (
          <div className="table-wrap" style={{ maxHeight: 360 }}>
            <table className="t">
              <thead><tr><th>User</th><th className="r">UID</th><th>Shell</th><th>Groups</th><th>Login</th><th>Privileged</th></tr></thead>
              <tbody>{users.data.filter((u) => !u.system || u.can_login).map((u) => <tr key={u.name}><td className="mono" style={{ fontWeight: 500 }}>{u.name}</td><td className={"r mono " + (u.uid === 0 && u.name !== "root" ? "crit-t" : "faint")}>{u.uid}</td><td className="mono faint" style={{ fontSize: 12 }}>{u.shell}</td><td className="dim" style={{ fontSize: 12 }}>{(u.groups ?? []).join(", ")}</td><td>{u.can_login ? <span className="chip info">yes</span> : <span className="faint">no</span>}</td><td>{u.sudo ? <span className="chip warn">sudo</span> : ""}</td></tr>)}</tbody>
            </table>
          </div>
        )}
      </Panel>
    </div>
  );
}

function TopList({ title, data }: { title: string; data: Record<string, number> }) {
  const rows = Object.entries(data).sort((a, b) => b[1] - a[1]).slice(0, 8);
  const max = rows[0]?.[1] ?? 1;
  return (
    <div>
      <div className="label" style={{ marginBottom: 8 }}>{title}</div>
      {rows.length === 0 && <div className="faint">None</div>}
      {rows.map(([k, v]) => (
        <div key={k} className="row" style={{ marginBottom: 6, fontSize: 12 }}>
          <span className="mono ellipsis" style={{ width: 160 }}>{k}</span>
          <div className="grow"><Bar value={(v / max) * 100} cls="warn" /></div>
          <span className="mono" style={{ width: 50, textAlign: "right" }}>{v}</span>
        </div>
      ))}
    </div>
  );
}

// ---------------------------------------------------------------- activity

export function ActivityTab({ s }: { s: Server }) {
  const ex = useFetch<Execution[]>(`/api/executions?server=${s.id}&limit=300`, [s.id], 15000);
  const [open, setOpen] = useState<Execution | null>(null);
  const [onlyChanges, setOnlyChanges] = useState(false);
  const rows = (ex.data ?? []).filter((e) => !onlyChanges || e.risk !== "read");
  return (
    <div className="col" style={{ gap: 12 }}>
      <div className="row">
        <span className="dim">Every tool execution on {s.name}, by you or the AI, with its policy decision.</span>
        <span className="spacer" />
        <Switch on={onlyChanges} onChange={setOnlyChanges} label="Changes only" />
      </div>
      <Panel bodyClass="flush">
        {!ex.data ? <SkeletonRows /> : rows.length === 0 ? <Empty icon="audit" title="No operations yet" /> : <ExecTable rows={rows} onOpen={setOpen} />}
      </Panel>
      {open && <ExecDrawer e={open} onClose={() => setOpen(null)} />}
    </div>
  );
}

export function ExecTable({ rows, onOpen, showServer }: { rows: Execution[]; onOpen: (e: Execution) => void; showServer?: boolean }) {
  const { serverById } = useApp();
  return (
    <div className="table-wrap" style={{ maxHeight: "64vh" }}>
      <table className="t">
        <thead><tr><th>When</th><th>Tool</th>{showServer && <th>Server</th>}<th>Risk</th><th>By</th><th>Policy</th><th>Status</th><th className="r">Duration</th></tr></thead>
        <tbody>
          {rows.map((e) => (
            <tr key={e.id} className="clickable" onClick={() => onOpen(e)}>
              <td className="nowrap faint" style={{ fontSize: 12 }}>{time(e.created_at)}</td>
              <td className="mono" style={{ fontSize: 12 }}>{e.tool}<span className="faint"> {Object.entries(e.args ?? {}).map(([k, v]) => `${k}=${typeof v === "string" ? v : JSON.stringify(v)}`).join(" ")}</span></td>
              {showServer && <td>{serverById(e.server_id)?.name ?? (e.server_id ? e.server_id : "fleet")}</td>}
              <td><RiskChip risk={e.risk} /></td>
              <td className="dim">{e.requested_by}{e.mode ? ` · ${e.mode}` : ""}</td>
              <td className="faint ellipsis" style={{ maxWidth: 260, fontSize: 12 }} title={e.decision_reason}>{e.decision}</td>
              <td><span className={"chip " + ({ succeeded: "ok", failed: "crit", blocked: "crit", pending_approval: "warn", running: "info" } as any)[e.status]}>{e.status.replace("_", " ")}</span></td>
              <td className="r mono faint" style={{ fontSize: 12 }}>{e.started_at && e.finished_at ? `${((Date.parse(e.finished_at) - Date.parse(e.started_at)) / 1000).toFixed(1)}s` : "—"}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

export function ExecDrawer({ e, onClose }: { e: Execution; onClose: () => void }) {
  return (
    <Drawer title={<span className="mono">{e.tool}</span>} sub={`${e.id} · ${time(e.created_at)}`} onClose={onClose}>
      <div className="col" style={{ gap: 14 }}>
        <KV rows={[["Status", e.status], ["Risk", <RiskChip risk={e.risk} />], ["Requested by", `${e.requested_by}${e.mode ? ` (${e.mode} mode)` : ""}`], ["Policy decision", `${e.decision} — ${e.decision_reason}`], ["Started", e.started_at ? time(e.started_at) : "—"], ["Finished", e.finished_at ? time(e.finished_at) : "—"], ["Conversation", e.conversation_id ? <a href={"#/ai/" + e.conversation_id}>open</a> : "—"]]} />
        <div><div className="label" style={{ marginBottom: 6 }}>Arguments</div><JSONView value={e.args} max={200} /></div>
        {e.error && <Notice kind="crit">{e.error}</Notice>}
        {e.result !== undefined && <div><div className="label" style={{ marginBottom: 6 }}>Result</div><JSONView value={e.result} max={600} /></div>}
      </div>
    </Drawer>
  );
}

export { useEffect, useMemo, api };
