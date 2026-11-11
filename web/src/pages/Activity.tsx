import React, { useState } from "react";
import { api } from "../lib/api";
import { useFetch } from "../lib/state";
import { Icon } from "../components/Icon";
import { Empty, JSONView, Notice, Panel, SkeletonRows, Tabs } from "../components/UI";
import { time } from "../lib/format";
import type { AuditEntry, Execution } from "../lib/types";
import { ExecDrawer, ExecTable } from "./ServerTabs";

export function Activity() {
  const [tab, setTab] = useState<"executions" | "audit">("executions");
  const ex = useFetch<Execution[]>(tab === "executions" ? "/api/executions?limit=500" : null, [tab], 15000);
  const [filter, setFilter] = useState("");
  const audit = useFetch<AuditEntry[]>(tab === "audit" ? `/api/audit?limit=500&action=${encodeURIComponent(filter)}` : null, [tab, filter]);
  const [verify, setVerify] = useState<any>(null);
  const [open, setOpen] = useState<Execution | null>(null);
  const [openA, setOpenA] = useState<number | null>(null);
  return (
    <div className="page">
      <div className="page-head">
        <div className="grow">
          <h1>Activity & Audit</h1>
          <p>Every tool execution and every security-relevant decision. The audit log is hash-chained so tampering is detectable.</p>
        </div>
      </div>
      <Tabs tabs={[{ id: "executions", label: "Tool executions", icon: "bolt" }, { id: "audit", label: "Audit log", icon: "audit" }]} value={tab} onChange={setTab} />
      {tab === "executions" && (
        <Panel bodyClass="flush">
          {!ex.data ? <SkeletonRows /> : ex.data.length === 0 ? <Empty icon="bolt" title="No executions yet" /> : <ExecTable rows={ex.data} onOpen={setOpen} showServer />}
        </Panel>
      )}
      {tab === "audit" && (
        <div className="col" style={{ gap: 12 }}>
          <div className="row">
            <select className="select" value={filter} onChange={(e) => setFilter(e.target.value)}>
              <option value="">All actions</option>
              {["tool.", "approval.", "policy.", "server.", "settings.", "ai.", "memory.", "conversation.", "core.", "recovery."].map((p) => <option key={p} value={p}>{p}*</option>)}
            </select>
            <span className="spacer" />
            <button className="btn" onClick={() => api.get("/api/audit/verify").then(setVerify)}><Icon name="lock" /> Verify integrity</button>
          </div>
          {verify && (verify.intact ? <Notice icon="check">Audit chain intact — {verify.verified_records} records verified.</Notice> : <Notice kind="crit">Audit chain broken at record {verify.first_broken_id}. Records may have been modified or deleted.</Notice>)}
          <Panel bodyClass="flush">
            {!audit.data ? <SkeletonRows /> : (
              <div className="table-wrap" style={{ maxHeight: "66vh" }}>
                <table className="t">
                  <thead><tr><th>#</th><th>Time</th><th>Actor</th><th>Action</th><th>Target</th><th>Hash</th></tr></thead>
                  <tbody>
                    {audit.data.map((a) => (
                      <React.Fragment key={a.id}>
                        <tr className="clickable" onClick={() => setOpenA(openA === a.id ? null : a.id)}>
                          <td className="mono faint">{a.id}</td>
                          <td className="nowrap faint" style={{ fontSize: 12 }}>{time(a.time)}</td>
                          <td>{a.actor}</td>
                          <td className="mono" style={{ fontSize: 12 }}>{a.action}</td>
                          <td className="mono ellipsis" style={{ maxWidth: 260, fontSize: 12 }}>{a.target}</td>
                          <td className="mono faint" style={{ fontSize: 11 }}>{a.hash.slice(0, 12)}…</td>
                        </tr>
                        {openA === a.id && <tr><td colSpan={6} style={{ padding: 12 }}><JSONView value={a.detail} max={300} /></td></tr>}
                      </React.Fragment>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </Panel>
        </div>
      )}
      {open && <ExecDrawer e={open} onClose={() => setOpen(null)} />}
    </div>
  );
}
