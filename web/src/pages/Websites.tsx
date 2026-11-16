import { Fragment, useEffect, useState } from "react";
import { api } from "../lib/api";
import { useApp, useFetch } from "../lib/state";
import { Icon } from "../components/Icon";
import { Empty, ErrorNote, Modal, Notice, Panel, SkeletonRows, StatusDot, Switch } from "../components/UI";
import { TimeSeriesChart } from "../components/Charts";
import { ago } from "../lib/format";

type Result = { ts: string; ok: boolean; status: number; latency_ms: number; error?: string; cert_expires?: string };
type Check = {
  id: string; server_id: string; name: string; url: string; expect_status: string; expect_text: string; interval_sec: number; timeout_sec: number;
  fail_threshold: number; slow_ms: number; cert_warn_days: number; enabled: boolean;
  state: string; last_result?: Result; uptime_24h?: number; uptime_7d?: number; avg_ms_24h?: number; cert_days_left?: number;
};

const blank = { server_id: "", name: "", url: "https://", expect_status: "200-399", expect_text: "", interval_sec: 60, timeout_sec: 10, fail_threshold: 2, slow_ms: 0, cert_warn_days: 14, enabled: true };

/** Website and certificate checks run from this PC (the visitor's view). */
export function Websites() {
  const { serverById, on, toast } = useApp();
  const list = useFetch<Check[]>("/api/checks", [], 30000);
  const [edit, setEdit] = useState<Partial<Check> | null>(null);
  const [open, setOpen] = useState<string | null>(null);
  useEffect(() => on((e) => e.type === "sitecheck" && list.reload()), [on]);
  const stateCls = (s: string) => (s === "up" ? "ok" : s === "down" ? "crit" : s === "disabled" ? "" : "warn");
  return (
    <div className="page">
      <div className="page-head">
        <div className="grow">
          <h1>Websites</h1>
          <p>Checks run from this PC, so they show what visitors see: availability, response time and certificate expiry. Failures raise alerts on the hosting server.</p>
        </div>
        <button className="btn primary" onClick={() => setEdit({ ...blank })}><Icon name="plus" /> Add website</button>
      </div>
      {!list.data && <Panel><SkeletonRows /></Panel>}
      {list.data?.length === 0 && (
        <Panel><Empty icon="globe" title="No websites checked yet" action={<button className="btn primary" onClick={() => setEdit({ ...blank })}><Icon name="plus" /> Add website</button>}>Add the sites your servers host. NEXUS checks them every minute and warns before certificates expire.</Empty></Panel>
      )}
      {!!list.data?.length && (
        <Panel bodyClass="flush">
          <table className="t">
            <thead><tr><th></th><th>Website</th><th>Server</th><th>Uptime 24 h / 7 d</th><th>Response</th><th>Certificate</th><th>Last check</th><th></th></tr></thead>
            <tbody>
              {list.data.map((c) => (
                <Fragment key={c.id}>
                  <tr style={{ cursor: "pointer" }} onClick={() => setOpen(open === c.id ? null : c.id)}>
                    <td><StatusDot status={stateCls(c.state)} /></td>
                    <td><b>{c.name}</b><div className="faint mono" style={{ fontSize: 12 }}>{c.url}</div></td>
                    <td>{serverById(c.server_id)?.name ?? "—"}</td>
                    <td className="mono">{c.uptime_24h != null ? `${c.uptime_24h}%` : "—"} / {c.uptime_7d != null ? `${c.uptime_7d}%` : "—"}</td>
                    <td className="mono">{c.avg_ms_24h != null ? `${Math.round(c.avg_ms_24h)} ms` : "—"}</td>
                    <td>{c.cert_days_left != null ? <span className={c.cert_days_left <= 3 ? "crit" : c.cert_days_left <= c.cert_warn_days ? "warn" : ""}>{c.cert_days_left} days</span> : <span className="faint">{c.url.startsWith("https") ? "—" : "no TLS"}</span>}</td>
                    <td>
                      {c.last_result ? (
                        <>
                          <span className="faint">{ago(c.last_result.ts)}</span>
                          {!c.last_result.ok && <div style={{ fontSize: 12, color: "var(--crit)" }}>{c.last_result.error}</div>}
                        </>
                      ) : <span className="faint">pending</span>}
                    </td>
                    <td onClick={(e) => e.stopPropagation()} style={{ whiteSpace: "nowrap" }}>
                      <button className="btn ghost sm icon" title="Check now" onClick={() => api.post(`/api/checks/${c.id}/run`).then(list.reload)}><Icon name="refresh" size={13} /></button>
                      <button className="btn ghost sm icon" title="Edit" onClick={() => setEdit(c)}><Icon name="edit" size={13} /></button>
                      <button className="btn ghost sm icon" title="Delete" onClick={() => confirm(`Delete the check for ${c.name}?`) && api.del(`/api/checks/${c.id}`).then(() => { toast({ title: "Check deleted", kind: "ok" }); list.reload(); })}><Icon name="trash" size={13} /></button>
                    </td>
                  </tr>
                  {open === c.id && <tr><td colSpan={8}><History id={c.id} slowMs={c.slow_ms} /></td></tr>}
                </Fragment>
              ))}
            </tbody>
          </table>
        </Panel>
      )}
      {edit && <EditCheck check={edit} onClose={() => setEdit(null)} onSaved={() => { setEdit(null); list.reload(); }} />}
    </div>
  );
}

function History({ id, slowMs }: { id: string; slowMs: number }) {
  const [hours, setHours] = useState(24);
  const res = useFetch<Result[]>(`/api/checks/${id}/results?hours=${hours}`, [id, hours]);
  if (!res.data) return <SkeletonRows n={3} />;
  const times = res.data.map((r) => new Date(r.ts).getTime());
  const lat = res.data.map((r) => (r.ok ? r.latency_ms : null));
  const fails = res.data.filter((r) => !r.ok);
  return (
    <div className="col" style={{ gap: 8, padding: "6px 4px" }}>
      <div className="row" style={{ gap: 6 }}>
        {[24, 24 * 7, 24 * 30].map((h) => <button key={h} className={"btn sm" + (h === hours ? " primary" : "")} onClick={() => setHours(h)}>{h === 24 ? "24 h" : h === 168 ? "7 days" : "30 days"}</button>)}
      </div>
      <TimeSeriesChart times={times} series={[{ label: "Response time", color: "--c1", values: lat, fill: true }]} height={150} format={(v) => `${v.toFixed(0)} ms`} thresholds={slowMs ? [{ value: slowMs, color: "--warn", label: "slow" }] : undefined} emptyText="No results in this range yet" />
      {fails.length > 0 && (
        <div className="faint" style={{ fontSize: 12 }}>
          {fails.length} failed check{fails.length === 1 ? "" : "s"}; latest: {new Date(fails[fails.length - 1].ts).toLocaleString()} — {fails[fails.length - 1].error}
        </div>
      )}
    </div>
  );
}

function EditCheck({ check, onClose, onSaved }: { check: Partial<Check>; onClose: () => void; onSaved: () => void }) {
  const { servers } = useApp();
  const [f, setF] = useState<any>({ ...blank, ...check, server_id: check.server_id || servers[0]?.id || "" });
  const [err, setErr] = useState<string | null>(null);
  const set = (k: string, v: any) => setF((x: any) => ({ ...x, [k]: v }));
  const save = () => {
    setErr(null);
    // Only the editable fields (the API rejects unknown ones such as "state").
    const body = {
      server_id: f.server_id, name: f.name, url: f.url, expect_status: f.expect_status, expect_text: f.expect_text, enabled: f.enabled,
      interval_sec: Number(f.interval_sec), timeout_sec: Number(f.timeout_sec), fail_threshold: Number(f.fail_threshold), slow_ms: Number(f.slow_ms), cert_warn_days: Number(f.cert_warn_days),
    };
    (check.id ? api.put(`/api/checks/${check.id}`, body) : api.post("/api/checks", body)).then(onSaved).catch((e) => setErr(e.message));
  };
  return (
    <Modal title={check.id ? "Edit website check" : "Add website"} onClose={onClose} footer={<><button className="btn ghost" onClick={onClose}>Cancel</button><button className="btn primary" onClick={save}>Save</button></>}>
      <div className="col" style={{ gap: 10 }}>
        <label className="field"><span>URL</span><input className="input mono" value={f.url} onChange={(e) => set("url", e.target.value)} autoFocus /></label>
        <div className="grid g2" style={{ gap: 10 }}>
          <label className="field"><span>Name</span><input className="input" placeholder="Defaults to the host name" value={f.name} onChange={(e) => set("name", e.target.value)} /></label>
          <label className="field"><span>Hosted on (alerts go to this server)</span>
            <select className="select" value={f.server_id} onChange={(e) => set("server_id", e.target.value)}>{servers.map((s) => <option key={s.id} value={s.id}>{s.name}</option>)}</select>
          </label>
          <label className="field"><span>Expected status</span><input className="input mono" value={f.expect_status} onChange={(e) => set("expect_status", e.target.value)} /></label>
          <label className="field"><span>Page must contain (optional)</span><input className="input" value={f.expect_text} onChange={(e) => set("expect_text", e.target.value)} /></label>
          <label className="field"><span>Check every (seconds)</span><input className="input mono" type="number" value={f.interval_sec} onChange={(e) => set("interval_sec", e.target.value)} /></label>
          <label className="field"><span>Timeout (seconds)</span><input className="input mono" type="number" value={f.timeout_sec} onChange={(e) => set("timeout_sec", e.target.value)} /></label>
          <label className="field"><span>Alert after N failures in a row</span><input className="input mono" type="number" value={f.fail_threshold} onChange={(e) => set("fail_threshold", e.target.value)} /></label>
          <label className="field"><span>Slow above (ms, 0 = off)</span><input className="input mono" type="number" value={f.slow_ms} onChange={(e) => set("slow_ms", e.target.value)} /></label>
          <label className="field"><span>Warn days before certificate expiry</span><input className="input mono" type="number" value={f.cert_warn_days} onChange={(e) => set("cert_warn_days", e.target.value)} /></label>
          <div style={{ alignSelf: "end", paddingBottom: 8 }}><Switch on={f.enabled} onChange={(v) => set("enabled", v)} label="Enabled" /></div>
        </div>
        {!servers.length && <Notice kind="warn">Add a server first: website alerts belong to the server that hosts the site.</Notice>}
        <ErrorNote error={err} />
      </div>
    </Modal>
  );
}
