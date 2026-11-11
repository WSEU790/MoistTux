import { useState } from "react";
import { api } from "../lib/api";
import { useApp, useFetch } from "../lib/state";
import { Icon } from "../components/Icon";
import { Empty, ErrorNote, Modal, Notice, Panel, SkeletonRows } from "../components/UI";
import { ago } from "../lib/format";
import type { Memory } from "../lib/types";

const KINDS = ["role", "service", "dependency", "config_path", "constraint", "maintenance_window", "incident", "note"];

export function MemoryPage() {
  const { servers, serverById } = useApp();
  const [server, setServer] = useState("");
  const list = useFetch<Memory[]>(`/api/memory${server ? `?server=${server}` : ""}`, [server]);
  const [edit, setEdit] = useState<Partial<Memory> | null>(null);
  const [kind, setKind] = useState("");
  const rows = (list.data ?? []).filter((m) => !kind || m.kind === kind);
  return (
    <div className="page">
      <div className="page-head">
        <div className="grow">
          <h1>AI Memory</h1>
          <p>Durable infrastructure knowledge the AI uses as context. It is stored separately from conversations, labelled with its source and age, and never treated as current state.</p>
        </div>
        <button className="btn primary" onClick={() => setEdit({ kind: "note", server_id: server, content: "" })}><Icon name="plus" /> Add knowledge</button>
      </div>
      <div className="row" style={{ marginBottom: 12 }}>
        <select className="select" value={server} onChange={(e) => setServer(e.target.value)}>
          <option value="">All scopes</option>
          {servers.map((s) => <option key={s.id} value={s.id}>{s.name}</option>)}
        </select>
        <select className="select" value={kind} onChange={(e) => setKind(e.target.value)}>
          <option value="">All kinds</option>
          {KINDS.map((k) => <option key={k} value={k}>{k.replace("_", " ")}</option>)}
        </select>
      </div>
      <Panel bodyClass="flush">
        {!list.data ? <SkeletonRows /> : rows.length === 0 ? (
          <Empty icon="memory" title="No stored knowledge">Add facts such as "web-01 is the primary Nginx reverse proxy for app.example.com" or "never restart postgresql on db-01 during business hours". The AI can also propose memories with its remember_fact tool.</Empty>
        ) : (
          <table className="t">
            <thead><tr><th>Scope</th><th>Kind</th><th>Knowledge</th><th>Source</th><th className="r">Recorded</th><th className="r" /></tr></thead>
            <tbody>
              {rows.map((m) => (
                <tr key={m.id}>
                  <td>{m.server_id ? serverById(m.server_id)?.name ?? m.server_id : <span className="chip outline">fleet</span>}</td>
                  <td><span className="chip">{m.kind.replace("_", " ")}</span></td>
                  <td style={{ whiteSpace: "pre-wrap", padding: "8px 12px" }}>{m.content}</td>
                  <td><span className={"chip " + (m.source === "ai" ? "accent" : "outline")}>{m.source}</span></td>
                  <td className="r faint" style={{ fontSize: 12 }}>{ago(m.observed_at)}</td>
                  <td className="r nowrap">
                    <button className="btn ghost sm icon" title="Edit" onClick={() => setEdit(m)}><Icon name="edit" size={13} /></button>
                    <button className="btn ghost sm icon" title="Delete" onClick={() => api.del(`/api/memory/${m.id}`).then(list.reload)}><Icon name="trash" size={13} /></button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Panel>
      {edit && <EditMemory m={edit} onClose={() => setEdit(null)} onSaved={() => { setEdit(null); list.reload(); }} />}
    </div>
  );
}

function EditMemory({ m, onClose, onSaved }: { m: Partial<Memory>; onClose: () => void; onSaved: () => void }) {
  const { servers } = useApp();
  const [f, setF] = useState({ server_id: m.server_id ?? "", kind: m.kind ?? "note", content: m.content ?? "" });
  const [err, setErr] = useState<string | null>(null);
  const save = () => (m.id ? api.put(`/api/memory/${m.id}`, f) : api.post("/api/memory", f)).then(onSaved).catch((e) => setErr(e.message));
  return (
    <Modal title={m.id ? "Correct knowledge" : "Add knowledge"} onClose={onClose} footer={<><button className="btn ghost" onClick={onClose}>Cancel</button><button className="btn primary" onClick={save} disabled={!f.content.trim()}>Save</button></>}>
      <ErrorNote error={err} />
      <div className="col" style={{ gap: 12 }}>
        <div className="grid g2" style={{ gap: 10 }}>
          <label className="field"><span>Scope</span><select className="select" value={f.server_id} onChange={(e) => setF({ ...f, server_id: e.target.value })}><option value="">Whole fleet</option>{servers.map((s) => <option key={s.id} value={s.id}>{s.name}</option>)}</select></label>
          <label className="field"><span>Kind</span><select className="select" value={f.kind} onChange={(e) => setF({ ...f, kind: e.target.value })}>{KINDS.map((k) => <option key={k} value={k}>{k.replace("_", " ")}</option>)}</select></label>
        </div>
        <label className="field"><span>Knowledge</span><textarea className="input" rows={4} value={f.content} onChange={(e) => setF({ ...f, content: e.target.value })} /></label>
        {m.source === "ai" && <Notice>Editing an AI-proposed memory marks it as operator-provided.</Notice>}
      </div>
    </Modal>
  );
}
