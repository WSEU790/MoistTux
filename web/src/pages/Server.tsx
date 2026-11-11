import React, { useMemo, useState } from "react";
import { navigate, useApp, useFetch } from "../lib/state";
import { api } from "../lib/api";
import { Icon } from "../components/Icon";
import { Bar, Empty, ErrorNote, KV, Modal, Notice, Panel, Skeleton, StatusDot, Switch, Tabs } from "../components/UI";
import { CoreStrip, Legend, Ring, TimeSeriesChart } from "../components/Charts";
import { ago, bytes, duration, levelClass, maxFS, netTotals, num, pct, rate, sevClass, statusClass, time } from "../lib/format";
import type { Alert, Point, Server } from "../lib/types";
import { SSHDeploy } from "./SSHDeploy";
import { DockerTab, LogsTab, NetworkTab, PackagesTab, ProcessesTab, SecurityTab, ServicesTab, StorageTab, ActivityTab } from "./ServerTabs";

const TABS = [
  { id: "overview", label: "Overview", icon: "activity" },
  { id: "processes", label: "Processes", icon: "process" },
  { id: "services", label: "Services", icon: "service" },
  { id: "logs", label: "Logs", icon: "logs" },
  { id: "docker", label: "Docker", icon: "docker" },
  { id: "storage", label: "Storage", icon: "disk" },
  { id: "network", label: "Network", icon: "network" },
  { id: "packages", label: "Packages", icon: "package" },
  { id: "security", label: "Security", icon: "shield" },
  { id: "activity", label: "Activity", icon: "audit" },
] as const;

export function ServerWorkspace({ id, tab = "overview" }: { id: string; tab?: string }) {
  const { serverById, serversLoaded } = useApp();
  const s = serverById(id);
  const [editing, setEditing] = useState(false);
  if (!s) {
    return (
      <div className="page">
        {serversLoaded ? <Empty icon="server" title="Server not found" action={<a href="#/fleet" className="btn">Back to fleet</a>}>It may have been removed.</Empty> : <Skeleton h={200} />}
      </div>
    );
  }
  const caps = new Set(s.health?.capabilities ?? []);
  const reachable = s.status === "online" || s.status === "degraded";
  return (
    <div className="page">
      <div className="crumbs" style={{ marginBottom: 10 }}>
        <a href="#/fleet">Fleet</a> <Icon name="chevron" size={11} /> <span>{s.name}</span>
      </div>
      <div className="page-head" style={{ alignItems: "center" }}>
        <div className="grow" style={{ minWidth: 0 }}>
          <div className="row" style={{ gap: 12 }}>
            <h1>{s.name}</h1>
            <span className={"chip " + statusClass(s.status)}>
              <StatusDot status={statusClass(s.status)} live={s.status === "online"} /> {s.status}
            </span>
            <span className={"chip " + (s.environment === "production" ? "accent" : "outline")}>{s.environment}</span>
            {s.group && <span className="chip outline">{s.group}</span>}
            {s.tags.map((t) => <span key={t} className="chip outline">#{t}</span>)}
          </div>
          <p className="mono" style={{ fontSize: 12 }}>
            {s.info ? `${s.info.hostname} · ${s.info.os_name} · ${s.info.kernel} · ${s.info.arch}` : s.address} · agent {s.health?.agent_version ?? "?"} at {s.address}:{s.port}
          </p>
        </div>
        <button className="btn" onClick={() => api.post("/api/conversations", { server_id: s.id, mode: "analyst", title: `${s.name} investigation` }).then((c: any) => navigate("/ai/" + c.id))}>
          <Icon name="ai" /> Ask AI about this server
        </button>
        <button className="btn ghost icon" title="Server settings" onClick={() => setEditing(true)}><Icon name="settings" /></button>
      </div>
      {!reachable && (
        <div style={{ marginBottom: 16 }}>
          <Notice kind={s.status === "untrusted" ? "crit" : "warn"}>
            <b>{s.status === "untrusted" ? "Agent identity mismatch." : s.status === "paused" ? "Monitoring paused." : "Server unreachable."}</b> {s.status_reason} {s.last_seen && `Last contact ${ago(s.last_seen)}.`} Live views are unavailable; history below is from stored telemetry.
          </Notice>
        </div>
      )}
      {s.health && !s.health.helper_ok && reachable && (
        <div style={{ marginBottom: 16 }}>
          <Notice kind="warn">Privileged helper unavailable: {s.health.helper_error}. Read-only views that need root (some sockets, firewall, sshd) and all service actions will fail until <span className="mono">nexus-agent install</span> is re-run as root.</Notice>
        </div>
      )}
      <Tabs
        tabs={TABS.map((t) => ({ ...t, badge: t.id === "docker" && !caps.has("docker") ? <span className="faint" style={{ fontSize: 10 }}>n/a</span> : undefined }))}
        value={tab as any}
        onChange={(t) => navigate(`/server/${s.id}/${t}`)}
      />
      {tab === "overview" && <Overview s={s} />}
      {tab === "processes" && <ProcessesTab s={s} />}
      {tab === "services" && <ServicesTab s={s} />}
      {tab === "logs" && <LogsTab s={s} />}
      {tab === "docker" && <DockerTab s={s} />}
      {tab === "storage" && <StorageTab s={s} />}
      {tab === "network" && <NetworkTab s={s} />}
      {tab === "packages" && <PackagesTab s={s} />}
      {tab === "security" && <SecurityTab s={s} />}
      {tab === "activity" && <ActivityTab s={s} />}
      {editing && <EditServer s={s} onClose={() => setEditing(false)} />}
    </div>
  );
}

const RANGES = ["15m", "1h", "6h", "24h", "7d", "30d"];

function Overview({ s }: { s: Server }) {
  const { live, liveTick } = useApp();
  void liveTick;
  const [range, setRange] = useState("1h");
  const tel = useFetch<{ points: Point[]; resolution: string }>(`/api/servers/${s.id}/telemetry?range=${range}&points=500`, [], 20000);
  const alerts = useFetch<Alert[]>(`/api/alerts?status=firing&server=${s.id}`, [s.id], 15000);
  const l = s.latest;
  const hist = live(s.id);
  const pts = tel.data?.points ?? [];
  const times = pts.map((p) => p.t);
  const series = useMemo(
    () => ({
      cpu: [
        { label: "CPU", color: "--c1", values: pts.map((p) => p.cpu), fill: true },
        ...(tel.data?.resolution === "5m" ? [{ label: "CPU peak", color: "--c2", values: pts.map((p) => p.cpu_max ?? null), dashed: true }] : []),
        { label: "I/O wait", color: "--c4", values: pts.map((p) => p.iowait) },
      ],
      mem: [
        { label: "Memory", color: "--c2", values: pts.map((p) => p.mem), fill: true },
        { label: "Swap", color: "--c4", values: pts.map((p) => p.swap) },
      ],
      net: [
        { label: "Receive", color: "--c1", values: pts.map((p) => p.rx_bps), fill: true },
        { label: "Transmit", color: "--c3", values: pts.map((p) => p.tx_bps) },
      ],
      disk: [
        { label: "Read", color: "--c2", values: pts.map((p) => p.read_bps), fill: true },
        { label: "Write", color: "--c4", values: pts.map((p) => p.write_bps) },
      ],
      load: [
        { label: "1 min", color: "--c1", values: pts.map((p) => p.load1) },
        { label: "5 min", color: "--c2", values: pts.map((p) => p.load5) },
        { label: "15 min", color: "--c3", values: pts.map((p) => p.load15) },
      ],
    }),
    [pts, tel.data?.resolution],
  );
  const n = netTotals(l?.net);
  const d = maxFS(l?.fs);
  const temps = l?.temps ?? [];
  const tmax = temps.length ? Math.max(...temps.map((t) => t.celsius)) : null;
  return (
    <div className="col" style={{ gap: 16 }}>
      <div className="kpis">
        <Kpi label="CPU" value={pct(l?.cpu.usage_pct, 1)} sub={l ? `user ${pct(l.cpu.user_pct, 0)} · sys ${pct(l.cpu.system_pct, 0)} · iowait ${pct(l.cpu.iowait_pct, 0)}${l.cpu.steal_pct > 0.5 ? ` · steal ${pct(l.cpu.steal_pct, 0)}` : ""}` : "—"} cls={levelClass(l?.cpu.usage_pct)} spark={hist.map((h) => h.cpu.usage_pct)} />
        <Kpi label="Memory" value={pct(l?.mem.used_pct, 1)} sub={l ? `${bytes(l.mem.used)} of ${bytes(l.mem.total)} · ${bytes(l.mem.available)} available` : "—"} cls={levelClass(l?.mem.used_pct, 85, 95)} spark={hist.map((h) => h.mem.used_pct)} />
        <Kpi label="Swap" value={l?.mem.swap_total ? pct(l.mem.swap_pct, 1) : "none"} sub={l?.mem.swap_total ? `${bytes(l.mem.swap_used)} of ${bytes(l.mem.swap_total)}` : "no swap configured"} cls={levelClass(l?.mem.swap_pct, 40, 70)} />
        <Kpi label="Load" value={l ? num(l.load[0]) : "—"} sub={l ? `${num(l.load[1])} · ${num(l.load[2])} on ${l.cpu.threads} threads` : "—"} cls={l ? levelClass((l.load[0] / Math.max(1, l.cpu.threads)) * 50, 80, 100) : ""} spark={hist.map((h) => h.load[0])} />
        <Kpi label="Fullest disk" value={pct(d, 1)} sub={l?.fs?.length ? `${l.fs.length} filesystems` : "—"} cls={levelClass(d, 85, 95)} />
        <Kpi label="Network" value={rate(n.rx)} sub={`↑ ${rate(n.tx)} · ${l?.tcp_established ?? "—"} TCP`} spark={hist.map((h) => netTotals(h.net).rx)} />
        <Kpi label="Uptime" value={duration(l?.uptime_sec)} sub={`${l?.procs.total ?? "—"} processes · ${l?.procs.running ?? "—"} running`} />
        {tmax !== null && <Kpi label="Temperature" value={`${tmax.toFixed(0)}°C`} sub={`${temps.length} sensors`} cls={levelClass(tmax, 75, 88)} />}
      </div>

      {alerts.data && alerts.data.length > 0 && (
        <Panel title={`Firing alerts (${alerts.data.length})`} bodyClass="flush">
          {alerts.data.map((a) => (
            <div key={a.id} className="row" style={{ padding: "10px 16px", borderTop: "1px solid var(--line)" }}>
              <span className={"dot " + sevClass(a.severity)} />
              <b style={{ fontWeight: 500 }}>{a.title}</b>
              <span className="dim grow ellipsis">{a.detail}</span>
              <span className="faint nowrap" style={{ fontSize: "var(--fs-xs)" }}>since {ago(a.first_seen)} · ×{a.count}</span>
            </div>
          ))}
        </Panel>
      )}

      <div className="row">
        <div className="label">Telemetry history</div>
        <span className="faint" style={{ fontSize: "var(--fs-xs)" }}>{tel.data ? `${pts.length} points · ${tel.data.resolution === "5m" ? "5-minute rollups" : "raw samples"}` : ""}</span>
        <span className="spacer" />
        <div className="seg">{RANGES.map((r) => <button key={r} className={range === r ? "on" : ""} onClick={() => setRange(r)}>{r}</button>)}</div>
      </div>
      <div className="grid g2">
        <Panel title="CPU" actions={<Legend series={series.cpu} />}>
          <TimeSeriesChart times={times} series={series.cpu} yMax={100} format={(v) => v.toFixed(0) + "%"} thresholds={[{ value: 85, color: "--warn" }]} />
        </Panel>
        <Panel title="Memory & swap" actions={<Legend series={series.mem} />}>
          <TimeSeriesChart times={times} series={series.mem} yMax={100} format={(v) => v.toFixed(0) + "%"} thresholds={[{ value: 90, color: "--warn" }]} />
        </Panel>
        <Panel title="Network throughput" actions={<Legend series={series.net} />}>
          <TimeSeriesChart times={times} series={series.net} format={(v) => rate(v)} />
        </Panel>
        <Panel title="Disk I/O" actions={<Legend series={series.disk} />}>
          <TimeSeriesChart times={times} series={series.disk} format={(v) => rate(v)} />
        </Panel>
        <Panel title="Load average" actions={<Legend series={series.load} />}>
          <TimeSeriesChart times={times} series={series.load} format={(v) => v.toFixed(1)} thresholds={l ? [{ value: l.cpu.threads, color: "--warn" }] : undefined} />
        </Panel>
        <Panel title="CPU cores (live)">
          {l?.cpu.per_core ? (
            <>
              <CoreStrip cores={l.cpu.per_core} />
              <div className="faint" style={{ fontSize: "var(--fs-xs)", marginTop: 10 }}>{l.cpu.per_core.length} logical CPUs · {s.info?.cpu_model}</div>
              <div className="sep" />
              <div className="label" style={{ marginBottom: 8 }}>Disk devices</div>
              {(l.disk_io ?? []).map((dv) => (
                <div key={dv.device} className="row" style={{ fontSize: 12, marginBottom: 6 }}>
                  <span className="mono" style={{ width: 90 }}>{dv.device}</span>
                  <Bar value={dv.util_pct} cls={levelClass(dv.util_pct, 70, 90)} />
                  <span className="mono faint" style={{ width: 170, textAlign: "right" }}>R {rate(dv.read_bps)} · W {rate(dv.write_bps)}</span>
                </div>
              ))}
            </>
          ) : (
            <Skeleton h={60} />
          )}
        </Panel>
      </div>

      <div className="grid g2">
        <Panel title="Filesystems" bodyClass="flush">
          <table className="t">
            <thead>
              <tr><th>Mount</th><th>Type</th><th style={{ width: "35%" }}>Used</th><th className="r">Free</th><th className="r">Inodes</th></tr>
            </thead>
            <tbody>
              {(l?.fs ?? []).map((f) => (
                <tr key={f.mount}>
                  <td><div className="mono">{f.mount}</div><div className="faint mono" style={{ fontSize: 11 }}>{f.device}{f.read_only ? " · ro" : ""}</div></td>
                  <td className="faint">{f.fstype}</td>
                  <td>
                    <div className="row"><Bar value={f.used_pct} cls={levelClass(f.used_pct, 85, 95)} thick /><span className="num mono" style={{ width: 52, textAlign: "right", fontSize: 12 }}>{pct(f.used_pct, 1)}</span></div>
                  </td>
                  <td className="r mono" style={{ fontSize: 12 }}>{bytes(f.avail)}<div className="faint">of {bytes(f.total)}</div></td>
                  <td className="r mono" style={{ fontSize: 12 }}>{f.inodes_total ? pct(f.inodes_pct, 0) : "n/a"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </Panel>
        <Panel title="System">
          {s.info ? (
            <KV
              rows={[
                ["Hostname", <span className="mono">{s.info.hostname}</span>],
                ["Operating system", `${s.info.os_name}${s.info.os_codename ? ` (${s.info.os_codename})` : ""}`],
                ["Kernel", <span className="mono">{s.info.kernel}</span>],
                ["Architecture", s.info.arch],
                ["CPU", `${s.info.cpu_model} · ${s.info.cpu_cores} cores / ${s.info.cpu_threads} threads`],
                ["Memory", `${bytes(s.info.mem_total)}${s.info.swap_total ? ` + ${bytes(s.info.swap_total)} swap` : ""}`],
                ["Virtualization", s.info.virtualization || "bare metal / undetected"],
                ["Booted", `${time(s.info.boot_time)} (${ago(s.info.boot_time)})`],
                ["Init · packages", `${s.info.init_system} · ${s.info.package_manager || "none detected"}`],
                ["Last package update", s.info.last_update ? `${time(s.info.last_update)} (${ago(s.info.last_update)})` : "no apt history"],
                ["Reboot required", s.info.reboot_required ? <span className="warn-t">yes — {s.info.reboot_required_pkgs?.join(", ")}</span> : "no"],
                ["Addresses", <span className="mono" style={{ fontSize: 12 }}>{(s.info.interfaces ?? []).filter((i) => !i.loopback && i.addrs?.length).map((i) => `${i.name} ${i.addrs?.join(", ")}`).join(" · ") || "—"}</span>],
                ["Timezone", s.info.timezone],
                ["Agent", s.health ? `${s.health.agent_version} · ${bytes(s.health.agent_rss_bytes)} RSS · ${s.health.running_as_root ? "running as root" : "least-privilege user"} · samples every ${s.health.interval_sec}s` : "—"],
                ["Capabilities", (s.health?.capabilities ?? []).join(" · ")],
                ["Agent fingerprint", <span className="mono" style={{ fontSize: 11 }}>{s.agent_fingerprint.match(/.{1,2}/g)?.slice(0, 16).join(":")}…</span>],
              ]}
            />
          ) : (
            <Skeleton h={200} />
          )}
        </Panel>
      </div>
      {temps.length > 0 && (
        <Panel title="Temperatures">
          <div className="row wrap" style={{ gap: 18 }}>
            {temps.map((t, i) => (
              <div key={i} className="col" style={{ alignItems: "center", gap: 2 }}>
                <Ring value={Math.min(100, t.celsius)} size={54} cls={levelClass(t.celsius, 75, 88)} />
                <span className="faint" style={{ fontSize: 11 }}>{t.sensor} {t.label}</span>
              </div>
            ))}
          </div>
        </Panel>
      )}
    </div>
  );
}

function Kpi({ label, value, sub, cls, spark }: { label: string; value: React.ReactNode; sub: React.ReactNode; cls?: string; spark?: number[] }) {
  return (
    <div className="kpi">
      <div className="label">{label}</div>
      <div className={"kpi-v " + (cls === "crit" ? "crit-t" : cls === "warn" ? "warn-t" : "")} style={{ fontSize: 26 }}>{value}</div>
      <div className="kpi-s ellipsis">{sub}</div>
      {spark && spark.length > 1 && (
        <div style={{ position: "absolute", right: 10, top: 14, opacity: 0.8 }}>
          <MiniSpark values={spark} cls={cls} />
        </div>
      )}
    </div>
  );
}

function MiniSpark({ values, cls }: { values: number[]; cls?: string }) {
  const w = 64,
    h = 22;
  const v = values.slice(-40);
  const m = Math.max(...v, 1e-9) * 1.15;
  const d = v.map((x, i) => `${i ? "L" : "M"}${((i / Math.max(1, v.length - 1)) * w).toFixed(1)},${(h - 2 - (x / m) * (h - 4)).toFixed(1)}`).join("");
  return (
    <svg width={w} height={h}>
      <path d={d} fill="none" stroke={cls === "crit" ? "var(--crit)" : cls === "warn" ? "var(--warn)" : "var(--accent)"} strokeWidth="1.3" />
    </svg>
  );
}

function EditServer({ s, onClose }: { s: Server; onClose: () => void }) {
  const { reloadServers, toast } = useApp();
  const [f, setF] = useState({ name: s.name, address: s.address, port: s.port, environment: s.environment, group: s.group, tags: s.tags.join(", "), notes: s.notes, monitoring_enabled: s.monitoring_enabled });
  const [err, setErr] = useState<string | null>(null);
  const [confirmRemove, setConfirmRemove] = useState(false);
  const [rotating, setRotating] = useState(false);
  const [upgrading, setUpgrading] = useState(false);
  const save = async () => {
    try {
      await api.patch(`/api/servers/${s.id}`, { ...f, port: Number(f.port), tags: f.tags.split(/[,\s]+/).filter(Boolean) });
      reloadServers();
      toast({ title: "Server updated", kind: "ok" });
      onClose();
    } catch (e: any) {
      setErr(e.message);
    }
  };
  const remove = async () => {
    try {
      const r = await api.del<any>(`/api/servers/${s.id}`);
      toast({ title: "Server removed", body: r.warning ?? "The agent revoked this core's certificate.", kind: r.warning ? "warn" : "ok" });
      reloadServers();
      navigate("/fleet");
    } catch (e: any) {
      setErr(e.message);
    }
  };
  const rotate = async () => {
    setRotating(true);
    try {
      const r = await api.post<any>(`/api/servers/${s.id}/rotate-certificate`);
      toast({ title: "Agent certificate rotated", body: `New fingerprint ${r.fingerprint.slice(0, 16)}…`, kind: "ok" });
      reloadServers();
    } catch (e: any) {
      setErr(e.message);
    } finally {
      setRotating(false);
    }
  };
  const set = (k: string, v: any) => setF((p) => ({ ...p, [k]: v }));
  if (upgrading)
    return (
      <Modal title={`Upgrade agent · ${s.name}`} sub={`Currently ${s.health?.agent_version ?? "unknown"}`} onClose={() => setUpgrading(false)} wide>
        <SSHDeploy mode="upgrade" server={s} onClose={() => setUpgrading(false)} />
      </Modal>
    );
  return (
    <Modal title={`Server settings · ${s.name}`} onClose={onClose} footer={<><button className="btn ghost" onClick={onClose}>Cancel</button><button className="btn primary" onClick={save}>Save</button></>}>
      <ErrorNote error={err} />
      <div className="col" style={{ gap: 12, marginTop: 8 }}>
        <label className="field"><span>Display name</span><input className="input" value={f.name} onChange={(e) => set("name", e.target.value)} /></label>
        <div className="grid" style={{ gridTemplateColumns: "1fr 110px", gap: 10 }}>
          <label className="field"><span>Address</span><input className="input mono" value={f.address} onChange={(e) => set("address", e.target.value)} /></label>
          <label className="field"><span>Port</span><input className="input mono" type="number" value={f.port} onChange={(e) => set("port", e.target.value)} /></label>
        </div>
        <div className="grid g2" style={{ gap: 10 }}>
          <label className="field"><span>Environment</span><select className="select" value={f.environment} onChange={(e) => set("environment", e.target.value)}>{["production", "staging", "development", "lab"].map((x) => <option key={x}>{x}</option>)}</select></label>
          <label className="field"><span>Group</span><input className="input" value={f.group} onChange={(e) => set("group", e.target.value)} /></label>
        </div>
        <label className="field"><span>Tags</span><input className="input" value={f.tags} onChange={(e) => set("tags", e.target.value)} /></label>
        <label className="field"><span>Notes</span><textarea className="input" rows={3} value={f.notes} onChange={(e) => set("notes", e.target.value)} /></label>
        <Switch on={f.monitoring_enabled} onChange={(v) => set("monitoring_enabled", v)} label="Monitoring enabled (pausing stops polling, alerts and AI investigations)" />
        <div className="sep" />
        <div className="row">
          <div className="grow">
            <b>Rotate agent certificate</b>
            <div className="faint" style={{ fontSize: "var(--fs-xs)" }}>The agent generates a new key; NEXUS re-pins it over the authenticated channel.</div>
          </div>
          <button className="btn" onClick={rotate} disabled={rotating || s.status !== "online"}>{rotating ? <Icon name="refresh" className="spin" /> : <Icon name="key" />} Rotate</button>
        </div>
        <div className="row">
          <div className="grow">
            <b>Upgrade agent over SSH</b>
            <div className="faint" style={{ fontSize: "var(--fs-xs)" }}>Installs the agent bundled with this NEXUS build; identity and enrollment are kept.</div>
          </div>
          <button className="btn" onClick={() => setUpgrading(true)}><Icon name="download" /> Upgrade…</button>
        </div>
        <div className="row">
          <div className="grow">
            <b className="crit-t">Remove server</b>
            <div className="faint" style={{ fontSize: "var(--fs-xs)" }}>Stops monitoring, deletes its telemetry, and asks the agent to revoke this core.</div>
          </div>
          {confirmRemove ? <button className="btn danger" onClick={remove}>Confirm removal</button> : <button className="btn danger" onClick={() => setConfirmRemove(true)}><Icon name="trash" /> Remove</button>}
        </div>
      </div>
    </Modal>
  );
}
