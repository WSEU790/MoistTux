import { useEffect, useMemo, useState } from "react";
import { api } from "../lib/api";
import { navigate, useApp, useFetch } from "../lib/state";
import { Icon } from "../components/Icon";
import { ErrorNote, Notice, Panel, RiskChip, SkeletonRows, Switch } from "../components/UI";
import type { Policy, ToolInfo } from "../lib/types";

const LEVELS = [
  { id: "read_only", label: "Read-only monitoring", desc: "Collect telemetry and raise alerts. The AI never investigates automatically." },
  { id: "recommend", label: "Recommendation only", desc: "The AI may investigate incidents and recommend fixes. Nothing is changed." },
  { id: "auto_maintenance", label: "Approved maintenance", desc: "Routine maintenance from the allowlist may run inside maintenance windows when an operator starts it in Autonomous mode." },
  { id: "auto_remediate", label: "Restricted autonomous remediation", desc: "Monitoring-triggered investigations run in Autonomous mode and may use allowlisted tools. Everything else becomes an approval request." },
];
const DAYS = ["mon", "tue", "wed", "thu", "fri", "sat", "sun"];

// Mirrors ai.Decide in the core for display; enforcement happens server-side.
function preview(p: Policy, t: ToolInfo, mode: "administrator" | "autonomous", env: string): { d: string; why: string } {
  if (t.risk === "read") return { d: "allow", why: "read-only" };
  if (t.internal) return { d: "allow", why: "internal" };
  if (p.tool_overrides?.[t.name] === "deny") return { d: "deny", why: "override" };
  if (t.risk === "high") {
    if (p.high === "deny") return { d: "deny", why: "high risk denied" };
    if (t.name === "reboot_server" && !p.allow_reboot) return { d: "deny", why: "reboots not allowed" };
    return { d: "approve", why: "always approved by a human" };
  }
  let base = t.risk === "low" ? p.low : p.medium;
  if (p.tool_overrides?.[t.name]) base = p.tool_overrides[t.name];
  if (base === "deny") return { d: "deny", why: `${t.risk} denied` };
  if (mode === "autonomous") {
    if (!p.autonomous_enabled) return { d: "approve", why: "autonomy off" };
    if (!p.autonomous_tools.includes(t.name)) return { d: "approve", why: "not allowlisted" };
    if (env === "production" && t.risk === "medium" && base !== "allow") return { d: "approve", why: "production medium-risk" };
    return { d: p.maintenance_windows.length ? "allow*" : "allow", why: p.maintenance_windows.length ? "inside windows" : "allowlisted" };
  }
  return base === "allow" ? { d: "allow", why: "pre-authorised" } : { d: "approve", why: `${t.risk} needs approval` };
}

export function Policies({ scope }: { scope?: string }) {
  const { servers, serverById, toast } = useApp();
  const sc = scope ?? "global";
  const tools = useFetch<ToolInfo[]>("/api/ai/tools", []);
  const pol = useFetch<{ policy: Policy; scope: string; explicit: boolean }>(`/api/policies/${sc}`, [sc]);
  const [p, setP] = useState<Policy | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [dirty, setDirty] = useState(false);
  const [protectInput, setProtectInput] = useState("");
  useEffect(() => {
    if (pol.data) {
      setP({ ...pol.data.policy, tool_overrides: pol.data.policy.tool_overrides ?? {}, autonomous_tools: pol.data.policy.autonomous_tools ?? [], maintenance_windows: pol.data.policy.maintenance_windows ?? [], protected: pol.data.policy.protected ?? [] });
      setDirty(false);
    }
  }, [pol.data]);
  const set = (patch: Partial<Policy>) => {
    setP((x) => ({ ...(x as Policy), ...patch }));
    setDirty(true);
  };
  const srv = sc !== "global" ? serverById(sc) : undefined;
  const mutating = useMemo(() => (tools.data ?? []).filter((t) => t.risk !== "read" && !t.internal), [tools.data]);
  const save = async () => {
    setErr(null);
    try {
      await api.put(`/api/policies/${sc}`, p);
      toast({ title: "Policy saved", body: `${srv ? srv.name : "Global default"} — change recorded in the audit log`, kind: "ok" });
      pol.reload();
    } catch (e: any) {
      setErr(e.message);
    }
  };
  const reset = async () => {
    await api.del(`/api/policies/${sc}`);
    toast({ title: srv ? `${srv.name} now inherits the global policy` : "Global policy reset to defaults", kind: "ok" });
    pol.reload();
  };
  return (
    <div className="page">
      <div className="page-head">
        <div className="grow">
          <h1>Automation Policies</h1>
          <p>What the AI may do on each server. Every tool call passes through this policy; high-risk operations always need a human.</p>
        </div>
      </div>
      <div className="grid" style={{ gridTemplateColumns: "240px minmax(0,1fr)" }}>
        <Panel title="Scope" bodyClass="flush">
          <div style={{ padding: 8 }}>
            <a href="#/policies" className={"nav-item" + (sc === "global" ? " active" : "")}><Icon name="globe" size={15} /> Global default</a>
            {servers.map((s) => (
              <a key={s.id} href={"#/policies/" + s.id} className={"nav-item" + (sc === s.id ? " active" : "")}><Icon name="server" size={15} /> <span className="ellipsis">{s.name}</span></a>
            ))}
          </div>
        </Panel>
        <div className="col" style={{ gap: 16 }}>
          <ErrorNote error={err} />
          {!p || !tools.data ? <Panel><SkeletonRows /></Panel> : (
            <>
              <div className="row">
                <div className="grow">
                  <b style={{ fontSize: "var(--fs-lg)" }}>{srv ? srv.name : "Global default"}</b>
                  <div className="faint" style={{ fontSize: "var(--fs-sm)" }}>{srv ? (pol.data?.explicit ? "Server-specific policy" : "Inheriting the global default — saving creates a server-specific policy") : "Applies to servers without their own policy"}</div>
                </div>
                {(pol.data?.explicit || !srv) && <button className="btn ghost" onClick={reset}>{srv ? "Revert to global" : "Reset defaults"}</button>}
                <button className="btn primary" onClick={save} disabled={!dirty}><Icon name="check" /> Save policy</button>
              </div>
              <Panel title="Risk matrix (Administrator mode)">
                <div className="grid g3">
                  {(["low", "medium", "high"] as const).map((r) => (
                    <div key={r} style={{ padding: 14, borderRadius: "var(--radius)", background: "var(--surface-2)" }}>
                      <div className="row" style={{ marginBottom: 10 }}><RiskChip risk={r} /><span className="faint" style={{ fontSize: 12 }}>{r === "low" ? "reloads, list refresh" : r === "medium" ? "restarts, updates, containers" : "reboots, data and access changes"}</span></div>
                      <div className="seg">
                        {(r === "high" ? ["approve", "deny"] : ["allow", "approve", "deny"]).map((d) => (
                          <button key={d} className={(p as any)[r] === d ? "on" : ""} onClick={() => set({ [r]: d } as any)}>{d}</button>
                        ))}
                      </div>
                    </div>
                  ))}
                </div>
                <div className="faint" style={{ fontSize: 12, marginTop: 10 }}>Assistant and Analyst modes are always read-only. High-risk operations can never be set to allow.</div>
              </Panel>
              <Panel title="Continuous monitoring behaviour">
                <div className="col" style={{ gap: 8 }}>
                  {LEVELS.map((l) => (
                    <label key={l.id} className="row" style={{ padding: "10px 12px", borderRadius: "var(--radius)", background: p.monitoring_level === l.id ? "var(--accent-soft)" : "var(--surface-2)", cursor: "pointer", alignItems: "flex-start" }}>
                      <input type="radio" checked={p.monitoring_level === l.id} onChange={() => set({ monitoring_level: l.id })} style={{ marginTop: 3 }} />
                      <div><b>{l.label}</b><div className="dim" style={{ fontSize: 12.5 }}>{l.desc}</div></div>
                    </label>
                  ))}
                </div>
              </Panel>
              <Panel title="Autonomous operation" actions={<Switch on={p.autonomous_enabled} onChange={(v) => set({ autonomous_enabled: v })} label={p.autonomous_enabled ? "Enabled" : "Disabled"} />}>
                {!p.autonomous_enabled && <Notice>Autonomous mode is disabled{srv ? ` for ${srv.name}` : ""}. Even in an Autonomous conversation every change will require approval.</Notice>}
                <div className="label" style={{ margin: "12px 0 8px" }}>Allowlisted tools (low and medium risk only)</div>
                <div className="grid g3" style={{ gap: 6 }}>
                  {mutating.filter((t) => t.risk !== "high").map((t) => (
                    <label key={t.name} className="check" style={{ padding: "6px 8px", borderRadius: "var(--radius-sm)", background: "var(--surface-2)", opacity: p.autonomous_enabled ? 1 : 0.55 }}>
                      <input type="checkbox" checked={p.autonomous_tools.includes(t.name)} onChange={(e) => set({ autonomous_tools: e.target.checked ? [...p.autonomous_tools, t.name] : p.autonomous_tools.filter((x) => x !== t.name) })} />
                      <span className="mono" style={{ fontSize: 12 }}>{t.name}</span> <RiskChip risk={t.risk} />
                    </label>
                  ))}
                </div>
                <div className="label" style={{ margin: "16px 0 8px" }}>Maintenance windows (local time)</div>
                {p.maintenance_windows.length === 0 && <div className="faint" style={{ fontSize: 12.5, marginBottom: 8 }}>No windows: allowlisted actions may run at any time.</div>}
                {p.maintenance_windows.map((w, i) => (
                  <div key={i} className="row" style={{ marginBottom: 6 }}>
                    <div className="seg">{DAYS.map((d) => <button key={d} className={w.days.includes(d) ? "on" : ""} onClick={() => { const ws = [...p.maintenance_windows]; ws[i] = { ...w, days: w.days.includes(d) ? w.days.filter((x) => x !== d) : [...w.days, d] }; set({ maintenance_windows: ws }); }}>{d}</button>)}</div>
                    <input className="input mono" type="time" value={w.start} onChange={(e) => { const ws = [...p.maintenance_windows]; ws[i] = { ...w, start: e.target.value }; set({ maintenance_windows: ws }); }} />
                    <span className="faint">to</span>
                    <input className="input mono" type="time" value={w.end} onChange={(e) => { const ws = [...p.maintenance_windows]; ws[i] = { ...w, end: e.target.value }; set({ maintenance_windows: ws }); }} />
                    <button className="btn ghost sm icon" onClick={() => set({ maintenance_windows: p.maintenance_windows.filter((_, j) => j !== i) })}><Icon name="x" size={13} /></button>
                  </div>
                ))}
                <button className="btn sm" onClick={() => set({ maintenance_windows: [...p.maintenance_windows, { days: ["sun"], start: "02:00", end: "04:00" }] })}><Icon name="plus" size={12} /> Add window</button>
              </Panel>
              <Panel title="Safeguards">
                <div className="col" style={{ gap: 14 }}>
                  <Switch on={p.allow_reboot} onChange={(v) => set({ allow_reboot: v })} label={<span>Allow reboot requests <span className="faint">— reboots still need explicit approval with a typed hostname</span></span>} />
                  <div>
                    <div className="label" style={{ marginBottom: 8 }}>Protected services and containers</div>
                    <div className="row wrap" style={{ gap: 6, marginBottom: 8 }}>
                      {p.protected.map((x) => <span key={x} className="chip warn mono">{x} <button className="btn ghost sm icon" style={{ width: 16, height: 16 }} onClick={() => set({ protected: p.protected.filter((y) => y !== x) })}><Icon name="x" size={10} /></button></span>)}
                      {p.protected.length === 0 && <span className="faint" style={{ fontSize: 12.5 }}>None. Protected targets always require approval, regardless of the rules above.</span>}
                    </div>
                    <form className="row" onSubmit={(e) => { e.preventDefault(); if (protectInput.trim()) { set({ protected: [...new Set([...p.protected, protectInput.trim()])] }); setProtectInput(""); } }}>
                      <input className="input mono" placeholder="e.g. postgresql or app-db" value={protectInput} onChange={(e) => setProtectInput(e.target.value)} />
                      <button className="btn sm">Add</button>
                    </form>
                  </div>
                </div>
              </Panel>
              <Panel title="Per-tool overrides & effective decisions" bodyClass="flush">
                <table className="t">
                  <thead><tr><th>Tool</th><th>Risk</th><th>Override</th><th>Administrator mode</th><th>Autonomous mode</th></tr></thead>
                  <tbody>
                    {mutating.map((t) => {
                      const a = preview(p, t, "administrator", srv?.environment ?? "production");
                      const b = preview(p, t, "autonomous", srv?.environment ?? "production");
                      const chip = (x: { d: string; why: string }) => <span className={"chip " + (x.d.startsWith("allow") ? "ok" : x.d === "deny" ? "crit" : "warn")} title={x.why}>{x.d} <span className="faint" style={{ fontSize: 10 }}>· {x.why}</span></span>;
                      return (
                        <tr key={t.name}>
                          <td><div className="mono" style={{ fontSize: 12 }}>{t.name}</div><div className="faint ellipsis" style={{ fontSize: 11, maxWidth: 380 }}>{t.description}</div></td>
                          <td><RiskChip risk={t.risk} /></td>
                          <td>
                            <select className="select" style={{ height: 26, fontSize: 12 }} value={p.tool_overrides?.[t.name] ?? ""} onChange={(e) => { const o = { ...(p.tool_overrides ?? {}) }; e.target.value ? (o[t.name] = e.target.value) : delete o[t.name]; set({ tool_overrides: o }); }}>
                              <option value="">inherit</option>
                              {(t.risk === "high" ? ["approve", "deny"] : ["allow", "approve", "deny"]).map((d) => <option key={d}>{d}</option>)}
                            </select>
                          </td>
                          <td>{chip(a)}</td>
                          <td>{chip(b)}</td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
                <div className="faint" style={{ fontSize: 11, padding: "8px 16px" }}>{srv ? `Evaluated for a ${srv.environment} server.` : "Evaluated for a production server."} allow* = only inside maintenance windows. The core enforces these rules; this table is a preview.</div>
              </Panel>
            </>
          )}
        </div>
      </div>
    </div>
  );
}

export { navigate };
