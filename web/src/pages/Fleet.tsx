import React, { useMemo, useState } from "react";
import { navigate, useApp, useRoute } from "../lib/state";
import { api } from "../lib/api";
import { Icon } from "../components/Icon";
import { Bar, Copy, Empty, ErrorNote, Modal, Notice, Panel, SkeletonRows, StatusDot, Tabs, useSort } from "../components/UI";
import { SSHDeploy } from "./SSHDeploy";
import { Ring, Sparkline } from "../components/Charts";
import { ago, duration, levelClass, maxFS, pct, statusClass } from "../lib/format";
import type { Server } from "../lib/types";

export function Fleet() {
  const { servers, serversLoaded, live, liveTick } = useApp();
  void liveTick;
  const route = useRoute();
  const [adding, setAdding] = useState(route.query.get("add") === "1");
  const [q, setQ] = useState("");
  const [env, setEnv] = useState("");
  const [status, setStatus] = useState("");
  const [group, setGroup] = useState("");
  const [view, setView] = useState<"table" | "grid">("table");
  const groups = useMemo(() => [...new Set(servers.map((s) => s.group).filter(Boolean))].sort(), [servers]);
  const rows = servers.filter(
    (s) =>
      (!q || [s.name, s.address, s.info?.os_name, ...s.tags].join(" ").toLowerCase().includes(q.toLowerCase())) &&
      (!env || s.environment === env) &&
      (!status || s.status === status) &&
      (!group || s.group === group),
  );
  const { sorted, th } = useSort<Server>(rows, { key: "name", dir: 1 }, (s, k) => {
    switch (k) {
      case "cpu":
        return s.latest?.cpu.usage_pct;
      case "mem":
        return s.latest?.mem.used_pct;
      case "disk":
        return maxFS(s.latest?.fs);
      case "score":
        return s.health_score;
      case "alerts":
        return (s.alerts.critical ?? 0) * 100 + (s.alerts.warning ?? 0);
      case "name":
        return s.name.toLowerCase();
    }
    return (s as any)[k];
  });

  return (
    <div className="page">
      <div className="page-head">
        <div className="grow">
          <h1>Fleet</h1>
          <p>Every server connected to this NEXUS core through an authenticated agent.</p>
        </div>
        <button className="btn primary" onClick={() => setAdding(true)}>
          <Icon name="plus" /> Add server
        </button>
      </div>
      <div className="row wrap" style={{ marginBottom: 14 }}>
        <div className="row" style={{ position: "relative" }}>
          <Icon name="search" size={14} style={{ position: "absolute", left: 10, color: "var(--text-3)" }} />
          <input className="input" style={{ paddingLeft: 30, width: 260 }} placeholder="Search name, address, OS, tag" value={q} onChange={(e) => setQ(e.target.value)} />
        </div>
        <select className="select" value={env} onChange={(e) => setEnv(e.target.value)}>
          <option value="">All environments</option>
          {["production", "staging", "development", "lab"].map((e) => <option key={e}>{e}</option>)}
        </select>
        <select className="select" value={status} onChange={(e) => setStatus(e.target.value)}>
          <option value="">Any status</option>
          {["online", "degraded", "offline", "untrusted", "paused", "connecting"].map((e) => <option key={e}>{e}</option>)}
        </select>
        {groups.length > 0 && (
          <select className="select" value={group} onChange={(e) => setGroup(e.target.value)}>
            <option value="">All groups</option>
            {groups.map((g) => <option key={g}>{g}</option>)}
          </select>
        )}
        <span className="spacer" />
        <div className="seg">
          <button className={view === "table" ? "on" : ""} onClick={() => setView("table")}>Table</button>
          <button className={view === "grid" ? "on" : ""} onClick={() => setView("grid")}>Tiles</button>
        </div>
      </div>
      {!serversLoaded && <Panel><SkeletonRows /></Panel>}
      {serversLoaded && servers.length === 0 && (
        <Panel>
          <Empty icon="server" title="No servers yet" action={<button className="btn primary" onClick={() => setAdding(true)}><Icon name="plus" /> Add your first server</button>}>
            Install the NEXUS agent on a Linux host and enroll it with its one-time code.
          </Empty>
        </Panel>
      )}
      {servers.length > 0 && view === "table" && (
        <Panel bodyClass="flush">
          <div className="table-wrap">
            <table className="t">
              <thead>
                <tr>
                  {th("name", "Server")}
                  <th>Environment</th>
                  <th>System</th>
                  {th("score", "Health")}
                  {th("cpu", "CPU")}
                  {th("mem", "Memory")}
                  {th("disk", "Disk")}
                  <th>Uptime</th>
                  {th("alerts", "Alerts", "r")}
                  <th className="r">Last seen</th>
                </tr>
              </thead>
              <tbody>
                {sorted.map((s) => {
                  const l = s.latest;
                  const d = maxFS(l?.fs);
                  return (
                    <tr key={s.id} className="clickable" onClick={() => navigate("/server/" + s.id)}>
                      <td>
                        <div className="row">
                          <StatusDot status={statusClass(s.status)} live={s.status === "online"} />
                          <div>
                            <div style={{ fontWeight: 500 }}>{s.name}</div>
                            <div className="faint mono" style={{ fontSize: 11 }}>{s.address}:{s.port}</div>
                          </div>
                        </div>
                      </td>
                      <td>
                        <div className="row wrap" style={{ gap: 4 }}>
                          <span className={"chip " + (s.environment === "production" ? "accent" : "outline")}>{s.environment}</span>
                          {s.group && <span className="chip outline">{s.group}</span>}
                          {s.tags.slice(0, 2).map((t) => <span key={t} className="chip outline">#{t}</span>)}
                        </div>
                      </td>
                      <td>
                        <div className="ellipsis" style={{ maxWidth: 220 }}>{s.info?.os_name ?? <span className="faint">—</span>}</div>
                        <div className="faint mono ellipsis" style={{ fontSize: 11, maxWidth: 220 }}>{s.info ? `${s.info.kernel} · ${s.info.arch}` : s.status_reason}</div>
                      </td>
                      <td>{s.health_score === null ? <span className="faint">—</span> : <Ring value={s.health_score} size={32} stroke={3} cls={s.health_score >= 85 ? "ok" : s.health_score >= 60 ? "warn" : "crit"} />}</td>
                      <td style={{ width: 150 }}>
                        <div className="row">
                          <span className="num mono" style={{ width: 42, fontSize: 12 }}>{pct(l?.cpu.usage_pct, 0)}</span>
                          <Sparkline values={live(s.id).map((h) => h.cpu.usage_pct)} max={100} width={80} height={20} />
                        </div>
                      </td>
                      <td style={{ width: 120 }}>
                        <div className="num mono" style={{ fontSize: 12 }}>{pct(l?.mem.used_pct, 0)}</div>
                        <Bar value={l?.mem.used_pct} cls={levelClass(l?.mem.used_pct, 85, 95)} />
                      </td>
                      <td style={{ width: 110 }}>
                        <div className="num mono" style={{ fontSize: 12 }}>{pct(d, 0)}</div>
                        <Bar value={d} cls={levelClass(d, 85, 95)} />
                      </td>
                      <td className="mono faint" style={{ fontSize: 12 }}>{duration(l?.uptime_sec)}</td>
                      <td className="r">
                        {s.alerts.critical ? <span className="chip crit">{s.alerts.critical}</span> : null} {s.alerts.warning ? <span className="chip warn">{s.alerts.warning}</span> : null}
                        {!s.alerts.critical && !s.alerts.warning && <span className="faint">—</span>}
                      </td>
                      <td className="r faint" style={{ fontSize: 12 }}>{ago(s.last_seen)}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
            {sorted.length === 0 && <div className="faint" style={{ padding: 20, textAlign: "center" }}>No servers match these filters.</div>}
          </div>
        </Panel>
      )}
      {servers.length > 0 && view === "grid" && (
        <div className="grid g-auto">
          {sorted.map((s) => <ServerTile key={s.id} s={s} />)}
        </div>
      )}
      {adding && <AddServer onClose={() => { setAdding(false); if (route.query.get("add")) navigate("/fleet"); }} />}
    </div>
  );
}

function ServerTile({ s }: { s: Server }) {
  const { live } = useApp();
  const l = s.latest;
  return (
    <div className="panel" style={{ padding: 16, cursor: "pointer" }} onClick={() => navigate("/server/" + s.id)}>
      <div className="row">
        <StatusDot status={statusClass(s.status)} live={s.status === "online"} />
        <b className="grow ellipsis">{s.name}</b>
        <span className={"chip " + (s.environment === "production" ? "accent" : "outline")}>{s.environment}</span>
      </div>
      <div className="faint ellipsis" style={{ fontSize: "var(--fs-xs)", margin: "4px 0 12px" }}>{s.info?.os_name ?? s.status_reason ?? s.address}</div>
      <Sparkline values={live(s.id).map((h) => h.cpu.usage_pct)} max={100} width={260} height={40} />
      <div className="grid g3" style={{ marginTop: 12, gap: 8 }}>
        {[
          ["CPU", l?.cpu.usage_pct],
          ["MEM", l?.mem.used_pct],
          ["DISK", maxFS(l?.fs)],
        ].map(([k, v]) => (
          <div key={k as string}>
            <div className="label">{k}</div>
            <div className="num mono" style={{ fontSize: 15 }}>{pct(v as number, 0)}</div>
            <Bar value={v as number} cls={levelClass(v as number, 85, 95)} />
          </div>
        ))}
      </div>
    </div>
  );
}

export function AddServer({ onClose }: { onClose: () => void }) {
  const [tab, setTab] = useState<"ssh" | "code">("ssh");
  return (
    <Modal title="Add server" sub="Connect a Linux server to NEXUS." onClose={onClose} wide>
      <Tabs tabs={[{ id: "ssh", label: "Install over SSH", icon: "bolt" }, { id: "code", label: "Agent already installed", icon: "key" }]} value={tab} onChange={setTab} />
      {tab === "ssh" ? <SSHDeploy mode="install" onClose={onClose} /> : <ManualEnroll onClose={onClose} />}
    </Modal>
  );
}

function ManualEnroll({ onClose }: { onClose: () => void }) {
  const { reloadServers, toast } = useApp();
  const [f, setF] = useState({ name: "", address: "", port: 7443, code: "", environment: "production", group: "", tags: "" });
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const set = (k: string, v: any) => setF((p) => ({ ...p, [k]: v }));
  const submit = async () => {
    setBusy(true);
    setErr(null);
    try {
      const s = await api.post<Server>("/api/servers", { ...f, port: Number(f.port), tags: f.tags.split(/[,\s]+/).filter(Boolean), code: f.code.trim() });
      toast({ title: "Server enrolled", body: `${s.name} — identity verified (${s.agent_fingerprint.slice(0, 16)}…)`, kind: "ok" });
      reloadServers();
      onClose();
      navigate("/server/" + s.id);
    } catch (e: any) {
      setErr(e.message);
    } finally {
      setBusy(false);
    }
  };
  const install = "sudo ./nexus-agent install --listen :7443";
  return (
    <div className="col" style={{ gap: 16 }}>
      <div className="grid" style={{ gridTemplateColumns: "1fr 1.1fr", gap: 24 }}>
        <div className="col" style={{ gap: 12 }}>
          <div className="label">On the server</div>
          <div className="dim" style={{ fontSize: "var(--fs-sm)" }}>
            Use this when SSH from this PC is not possible. Copy the <span className="mono">nexus-agent</span> binary for the server's architecture (amd64 or arm64) to the host, then run:
          </div>
          <div className="row" style={{ background: "var(--chrome)", border: "1px solid var(--line)", borderRadius: "var(--radius)", padding: "8px 10px" }}>
            <code className="mono grow">{install}</code>
            <Copy text={install} />
          </div>
          <div className="dim" style={{ fontSize: "var(--fs-sm)" }}>
            It creates the <span className="mono">nexus</span> service account, a sudoers rule limited to the validated helper, and a systemd unit, then prints an enrollment code. Add <span className="mono">--docker</span> to grant Docker access (root-equivalent).
          </div>
          <Notice icon="lock">
            The code contains a one-time secret and the agent's certificate fingerprint. NEXUS refuses to connect if the certificate presented does not match, so there is no trust-on-first-use window.
          </Notice>
          <div className="dim" style={{ fontSize: "var(--fs-sm)" }}>
            Need a new code later? Run <span className="mono">sudo nexus-agent enroll-code</span> (codes expire after 24 h).
          </div>
        </div>
        <div className="col" style={{ gap: 12 }}>
          <div className="label">In NEXUS</div>
          <ErrorNote error={err} />
          <div className="grid" style={{ gridTemplateColumns: "1fr 110px", gap: 10 }}>
            <label className="field"><span>Address</span><input className="input mono" placeholder="10.0.0.12 or host.example.net" value={f.address} onChange={(e) => set("address", e.target.value)} autoFocus /></label>
            <label className="field"><span>Port</span><input className="input mono" type="number" value={f.port} onChange={(e) => set("port", e.target.value)} /></label>
          </div>
          <label className="field"><span>Enrollment code</span><textarea className="input mono" rows={2} placeholder="NX1-…" value={f.code} onChange={(e) => set("code", e.target.value)} /></label>
          <label className="field"><span>Display name</span><input className="input" placeholder="Defaults to the server's hostname" value={f.name} onChange={(e) => set("name", e.target.value)} /></label>
          <div className="grid g2" style={{ gap: 10 }}>
            <label className="field"><span>Environment</span>
              <select className="select" value={f.environment} onChange={(e) => set("environment", e.target.value)}>
                {["production", "staging", "development", "lab"].map((x) => <option key={x}>{x}</option>)}
              </select>
            </label>
            <label className="field"><span>Group</span><input className="input" placeholder="e.g. web, data" value={f.group} onChange={(e) => set("group", e.target.value)} /></label>
          </div>
          <label className="field"><span>Tags</span><input className="input" placeholder="comma separated" value={f.tags} onChange={(e) => set("tags", e.target.value)} /></label>
        </div>
      </div>
      <div className="row" style={{ justifyContent: "flex-end" }}>
        <button className="btn ghost" onClick={onClose}>Cancel</button>
        <button className="btn primary" disabled={busy || !f.address || !f.code} onClick={submit}>
          {busy ? <Icon name="refresh" className="spin" /> : <Icon name="lock" />} Verify & enroll
        </button>
      </div>
    </div>
  );
}

export { React };
