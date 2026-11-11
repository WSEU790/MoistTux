import React, { useState } from "react";
import { useApp, useFetch } from "../lib/state";
import { Empty, Panel, RiskChip, Seg, SkeletonRows } from "../components/UI";
import { ApprovalCard } from "./AICenter";
import { ago, time } from "../lib/format";
import type { Approval } from "../lib/types";

export function Approvals() {
  const { serverById, on } = useApp();
  const [status, setStatus] = useState<"pending" | "">("pending");
  const list = useFetch<Approval[]>(`/api/approvals?status=${status}&limit=200`, [status]);
  React.useEffect(() => on((e) => e.type === "approval" && list.reload()), [on]);
  return (
    <div className="page">
      <div className="page-head">
        <div className="grow">
          <h1>Approvals</h1>
          <p>Operations the AI requested that your execution policy routes to a human. Each includes an impact preview gathered with read-only tools.</p>
        </div>
        <Seg options={[{ id: "pending", label: "Pending" }, { id: "", label: "History" }]} value={status} onChange={setStatus} />
      </div>
      {!list.data && <Panel><SkeletonRows /></Panel>}
      {list.data?.length === 0 && <Panel><Empty icon="approval" title={status === "pending" ? "Nothing waiting for you" : "No approvals yet"}>When the AI needs to change something and the policy requires approval, the request appears here and in the conversation.</Empty></Panel>}
      {status === "pending" ? (
        <div className="col" style={{ gap: 14, maxWidth: 920 }}>
          {list.data?.map((a) => (
            <div key={a.id}>
              <div className="row faint" style={{ fontSize: 12, marginBottom: 6 }}>
                <span>{serverById(a.server_id)?.name}</span>·<span>requested {ago(a.requested_at)}</span>
                {a.execution?.conversation_id && <a href={"#/ai/" + a.execution.conversation_id}>open conversation</a>}
              </div>
              <ApprovalCard id={a.id} reason={a.execution?.decision_reason} />
            </div>
          ))}
        </div>
      ) : (
        list.data && list.data.length > 0 && (
          <Panel bodyClass="flush">
            <table className="t">
              <thead><tr><th>Requested</th><th>Operation</th><th>Risk</th><th>Server</th><th>Decision</th><th>By</th><th>Note</th></tr></thead>
              <tbody>
                {list.data.map((a) => (
                  <tr key={a.id}>
                    <td className="nowrap faint" style={{ fontSize: 12 }}>{time(a.requested_at)}</td>
                    <td>{a.summary}</td>
                    <td><RiskChip risk={a.risk} /></td>
                    <td>{serverById(a.server_id)?.name}</td>
                    <td><span className={"chip " + (a.status === "approved" ? "ok" : a.status === "denied" ? "crit" : a.status === "pending" ? "warn" : "")}>{a.status}</span></td>
                    <td className="dim">{a.decided_by || "—"}</td>
                    <td className="faint">{a.reason || ""}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </Panel>
        )
      )}
    </div>
  );
}
