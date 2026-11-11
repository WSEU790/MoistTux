import { useState } from "react";
import { api } from "../lib/api";
import { useApp, useFetch } from "../lib/state";
import { Icon } from "../components/Icon";
import { Empty, ErrorNote, JSONView, Notice, Panel, Seg, SkeletonRows } from "../components/UI";
import { Markdown } from "../components/Markdown";
import { ago, sevClass, time } from "../lib/format";
import type { Alert, Incident } from "../lib/types";

export function Incidents({ id }: { id?: string }) {
  const { serverById } = useApp();
  const [status, setStatus] = useState<"open" | "resolved" | "">("open");
  const inc = useFetch<Incident[]>(`/api/incidents?status=${status}&limit=200`, [status], 15000);
  const [astatus, setAStatus] = useState<"firing" | "resolved" | "">("firing");
  const alerts = useFetch<Alert[]>(`/api/alerts?status=${astatus}&limit=500`, [astatus], 15000);
  if (id) return <IncidentDetail id={id} />;
  return (
    <div className="page">
      <div className="page-head">
        <div className="grow">
          <h1>Incidents & Alerts</h1>
          <p>Deterministic rules raise alerts; related alerts on one server are grouped into incidents, which the AI can investigate.</p>
        </div>
        <a className="btn" href="#/settings/monitoring"><Icon name="settings" /> Alert rules</a>
      </div>
      <div className="grid" style={{ gridTemplateColumns: "minmax(0,1fr) minmax(0,1.25fr)" }}>
        <Panel title="Incidents" actions={<Seg options={[{ id: "open", label: "Open" }, { id: "resolved", label: "Resolved" }, { id: "", label: "All" }]} value={status} onChange={setStatus} />} bodyClass="flush">
          {!inc.data ? <SkeletonRows /> : inc.data.length === 0 ? <Empty icon="check" title={status === "open" ? "No open incidents" : "No incidents"} /> : inc.data.map((i) => (
            <a key={i.id} href={"#/incidents/" + i.id} className="row" style={{ padding: "12px 16px", borderTop: "1px solid var(--line)", color: "var(--text)", textDecoration: "none" }}>
              <span className={"dot " + sevClass(i.severity)} />
              <div className="grow" style={{ minWidth: 0 }}>
                <div className="ellipsis" style={{ fontWeight: 500 }}>{i.title}</div>
                <div className="faint" style={{ fontSize: "var(--fs-xs)" }}>{serverById(i.server_id)?.name} · opened {ago(i.opened_at)}{i.resolved_at ? ` · resolved ${ago(i.resolved_at)}` : ""}</div>
              </div>
              {i.ai_status && <span className={"chip " + (i.ai_status === "complete" ? "accent" : i.ai_status.startsWith("failed") ? "crit" : "info")}>AI {i.ai_status.split(":")[0]}</span>}
              <span className={"chip " + (i.status === "open" ? "warn" : "")}>{i.status}</span>
            </a>
          ))}
        </Panel>
        <Panel title="Alerts" actions={<Seg options={[{ id: "firing", label: "Firing" }, { id: "resolved", label: "Resolved" }, { id: "", label: "All" }]} value={astatus} onChange={setAStatus} />} bodyClass="flush">
          {!alerts.data ? <SkeletonRows /> : alerts.data.length === 0 ? <Empty icon="check" title="No alerts" /> : (
            <div className="table-wrap" style={{ maxHeight: "70vh" }}>
              <table className="t">
                <thead><tr><th>Severity</th><th>Alert</th><th>Server</th><th className="r">Seen</th><th className="r" /></tr></thead>
                <tbody>
                  {alerts.data.map((a) => (
                    <tr key={a.id}>
                      <td><span className={"chip " + sevClass(a.severity)}>{a.severity}</span></td>
                      <td>
                        <div style={{ fontWeight: 500 }}>{a.title}</div>
                        <div className="faint" style={{ fontSize: 12 }}>{a.detail}</div>
                      </td>
                      <td><a href={"#/server/" + a.server_id}>{serverById(a.server_id)?.name ?? a.server_id}</a></td>
                      <td className="r faint nowrap" style={{ fontSize: 12 }}>{ago(a.first_seen)}<div>×{a.count}</div></td>
                      <td className="r">{a.status === "firing" && !a.acknowledged_at ? <button className="btn sm ghost" onClick={() => api.post(`/api/alerts/${a.id}/ack`).then(alerts.reload)}>Ack</button> : a.acknowledged_at ? <span className="faint" style={{ fontSize: 11 }}>acked</span> : null}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </Panel>
      </div>
    </div>
  );
}

function IncidentDetail({ id }: { id: string }) {
  const { serverById, ai, toast } = useApp();
  const inc = useFetch<Incident>(`/api/incidents/${id}`, [id], 5000);
  const [busy, setBusy] = useState(false);
  const i = inc.data;
  const investigate = async () => {
    setBusy(true);
    try {
      await api.post(`/api/incidents/${id}/investigate`);
      toast({ title: "AI investigation started", kind: "ok" });
      inc.reload();
    } catch (e: any) {
      toast({ title: "Could not start investigation", body: e.message, kind: "crit" });
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="page">
      <div className="crumbs" style={{ marginBottom: 10 }}><a href="#/incidents">Incidents</a> <Icon name="chevron" size={11} /> <span>{id}</span></div>
      <ErrorNote error={inc.error} />
      {!i ? <SkeletonRows /> : (
        <>
          <div className="page-head" style={{ alignItems: "center" }}>
            <div className="grow">
              <div className="row"><span className={"chip " + sevClass(i.severity)}>{i.severity}</span><span className={"chip " + (i.status === "open" ? "warn" : "")}>{i.status}</span></div>
              <h1 style={{ marginTop: 8 }}>{i.title}</h1>
              <p>{serverById(i.server_id)?.name} · opened {time(i.opened_at)}{i.resolved_at ? ` · resolved ${time(i.resolved_at)}` : ""}</p>
            </div>
            {i.ai_conversation_id && <a className="btn" href={"#/ai/" + i.ai_conversation_id}><Icon name="ai" /> Open investigation</a>}
            <button className="btn primary" onClick={investigate} disabled={busy || i.ai_status === "investigating" || !ai?.reachable} title={!ai?.reachable ? "Ollama is not reachable" : ""}>
              {i.ai_status === "investigating" ? <Icon name="refresh" className="spin" /> : <Icon name="ai" />} {i.ai_status === "investigating" ? "Investigating…" : i.ai_status ? "Investigate again" : "Investigate with AI"}
            </button>
          </div>
          <div className="grid" style={{ gridTemplateColumns: "minmax(0,1.3fr) minmax(0,1fr)" }}>
            <Panel title="AI diagnostic report">
              {i.ai_report ? <Markdown text={i.ai_report} /> : i.ai_status === "investigating" ? <div className="scan" style={{ padding: 20 }}><span className="dim">The AI is collecting evidence with read-only tools…</span></div> : (
                <Empty icon="ai" title="No AI report yet">{i.ai_status ? i.ai_status : "Start an investigation, or enable automatic investigation in Settings → Monitoring."}</Empty>
              )}
            </Panel>
            <Panel title={`Correlated alerts (${i.alerts?.length ?? 0})`} bodyClass="flush">
              <div style={{ padding: "0 16px 12px" }}>
                <Notice>Alerts on the same server within the correlation window are grouped, because one underlying problem often triggers several rules.</Notice>
              </div>
              {(i.alerts ?? []).map((a) => (
                <div key={a.id} style={{ padding: "10px 16px", borderTop: "1px solid var(--line)" }}>
                  <div className="row"><span className={"dot " + sevClass(a.severity)} /><b style={{ fontWeight: 500 }} className="grow">{a.title}</b><span className={"chip " + (a.status === "firing" ? "warn" : "")}>{a.status}</span></div>
                  <div className="dim" style={{ fontSize: 12.5, margin: "4px 0" }}>{a.detail}</div>
                  <div className="faint" style={{ fontSize: 11 }}>first {time(a.first_seen)} · last {time(a.last_seen)} · ×{a.count} · rule {a.rule}</div>
                  {a.evidence !== undefined && a.evidence !== null && (
                    <details><summary className="faint" style={{ cursor: "pointer", fontSize: 11 }}>Evidence</summary><JSONView value={a.evidence} max={200} /></details>
                  )}
                </div>
              ))}
            </Panel>
          </div>
        </>
      )}
    </div>
  );
}
