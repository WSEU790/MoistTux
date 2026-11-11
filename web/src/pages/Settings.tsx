import { useEffect, useState } from "react";
import { api } from "../lib/api";
import { navigate, useApp, useFetch } from "../lib/state";
import { Icon } from "../components/Icon";
import { Bar, Empty, ErrorNote, KV, Notice, Panel, Seg, SkeletonRows, Switch } from "../components/UI";
import { bytes, duration, time } from "../lib/format";
import type { ModelInfo } from "../lib/types";

const SECTIONS = [
  { id: "appearance", label: "Appearance", icon: "sun" },
  { id: "ai", label: "AI models & Ollama", icon: "ai" },
  { id: "behaviour", label: "AI behaviour", icon: "bolt" },
  { id: "monitoring", label: "Monitoring & alerts", icon: "activity" },
  { id: "notifications", label: "Notifications", icon: "bell" },
  { id: "security", label: "Security & identity", icon: "lock" },
  { id: "updates", label: "Updates", icon: "download" },
  { id: "about", label: "About & data", icon: "info" },
];

export function Settings({ section = "appearance" }: { section?: string }) {
  return (
    <div className="page">
      <div className="page-head"><div className="grow"><h1>Settings</h1><p>All settings are stored locally and validated by the core before they take effect.</p></div></div>
      <div className="grid" style={{ gridTemplateColumns: "220px minmax(0,1fr)", alignItems: "start" }}>
        <div className="panel" style={{ padding: 8, position: "sticky", top: 0 }}>
          {SECTIONS.map((s) => (
            <a key={s.id} href={"#/settings/" + s.id} className={"nav-item" + (section === s.id ? " active" : "")}><Icon name={s.icon} size={15} /> {s.label}</a>
          ))}
        </div>
        <div className="col" style={{ gap: 16, minWidth: 0 }}>
          {section === "appearance" && <Appearance />}
          {section === "ai" && <AIModels />}
          {section === "behaviour" && <AIBehaviour />}
          {section === "monitoring" && <Monitoring />}
          {section === "notifications" && <Notifications />}
          {section === "security" && <Security />}
          {section === "updates" && <Updates />}
          {section === "about" && <About />}
        </div>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------- appearance

const THEMES = [
  { id: "graphite", label: "Graphite", sw: ["#0a0c0f", "#151a1f", "#22d3ee"] },
  { id: "midnight", label: "Midnight", sw: ["#04060c", "#0e1322", "#22d3ee"] },
  { id: "tactical", label: "Tactical", sw: ["#060806", "#121610", "#d6a63c"] },
  { id: "light", label: "Light", sw: ["#f3f5f8", "#ffffff", "#0e7490"] },
] as const;
const ACCENTS = [188, 200, 160, 265, 330, 25, 44];

function Appearance() {
  const { appearance: a, setAppearance } = useApp();
  return (
    <>
      <Panel title="Theme">
        <div className="grid g4">
          {THEMES.map((t) => (
            <button key={t.id} onClick={() => setAppearance({ theme: t.id })} className="panel" style={{ padding: 0, cursor: "pointer", overflow: "hidden", outline: a.theme === t.id ? "2px solid var(--accent)" : "none", outlineOffset: 2, textAlign: "left" }}>
              <div style={{ height: 70, background: t.sw[0], display: "flex", gap: 6, padding: 10 }}>
                <div style={{ width: 22, background: t.sw[1], borderRadius: 3 }} />
                <div style={{ flex: 1, background: t.sw[1], borderRadius: 3, position: "relative" }}><div style={{ position: "absolute", left: 8, right: 30, bottom: 10, height: 3, background: t.sw[2], borderRadius: 2 }} /></div>
              </div>
              <div style={{ padding: "8px 10px", fontWeight: 500 }}>{t.label}</div>
            </button>
          ))}
        </div>
      </Panel>
      <Panel title="Accent colour">
        <div className="row" style={{ gap: 10 }}>
          {ACCENTS.map((h) => (
            <button key={h} aria-label={`Accent hue ${h}`} onClick={() => setAppearance({ accent: h })} style={{ width: 30, height: 30, borderRadius: 8, border: a.accent === h ? "2px solid var(--text)" : "1px solid var(--line-2)", background: `hsl(${h} 80% 55%)`, cursor: "pointer" }} />
          ))}
          <input type="range" min={0} max={359} value={a.accent} onChange={(e) => setAppearance({ accent: Number(e.target.value) })} style={{ width: 200, marginLeft: 12 }} />
          <span className="mono faint">hue {a.accent}</span>
        </div>
      </Panel>
      <Panel title="Layout">
        <div className="col" style={{ gap: 14 }}>
          <div className="row"><span style={{ width: 160 }}>Interface density</span><Seg options={[{ id: "compact", label: "Compact" }, { id: "comfortable", label: "Comfortable" }, { id: "spacious", label: "Spacious" }]} value={a.density} onChange={(v) => setAppearance({ density: v })} /></div>
          <div className="row"><span style={{ width: 160 }}>Motion</span><Seg options={[{ id: "full", label: "Full" }, { id: "reduced", label: "Reduced" }]} value={a.motion} onChange={(v) => setAppearance({ motion: v })} /></div>
          <div className="row"><span style={{ width: 160 }}>Sidebar</span><Seg options={[{ id: "expanded", label: "Expanded" }, { id: "collapsed", label: "Icons only" }]} value={a.sidebar} onChange={(v) => setAppearance({ sidebar: v })} /></div>
          <div className="row"><span style={{ width: 160 }}>Dashboard</span><button className="btn sm" onClick={() => navigate("/")}>Customize on the Command Center</button></div>
        </div>
      </Panel>
    </>
  );
}

// ---------------------------------------------------------------- AI models

function AIModels() {
  const { ai, reloadAI, on, toast } = useApp();
  const settings = useFetch<any>("/api/settings/ai", []);
  const models = useFetch<ModelInfo[]>("/api/ai/models", []);
  const hw = useFetch<any>("/api/ai/hardware", []);
  const roles = useFetch<any[]>("/api/ai/roles", []);
  const [url, setUrl] = useState("");
  const [pulls, setPulls] = useState<Record<string, any>>({});
  const [custom, setCustom] = useState("");
  const [err, setErr] = useState<string | null>(null);
  useEffect(() => { if (settings.data) setUrl(settings.data.ollama_url); }, [settings.data]);
  useEffect(
    () =>
      on((e) => {
        if (e.type !== "model.pull") return;
        setPulls((p) => ({ ...p, [e.data.model]: e.data }));
        if (e.data.done) {
          toast({ title: e.data.error ? `Download of ${e.data.model} failed` : `${e.data.model} installed`, body: e.data.error, kind: e.data.error ? "crit" : "ok" });
          models.reload();
          hw.reload();
          reloadAI();
        }
      }),
    [on],
  );
  const saveSettings = async (patch: any) => {
    setErr(null);
    try {
      await api.put("/api/settings/ai", { ...settings.data, ...patch });
      settings.reload();
      models.reload();
      reloadAI();
      return true;
    } catch (e: any) {
      setErr(e.message);
      return false;
    }
  };
  const pull = (model: string) => api.post("/api/ai/models/pull", { model }).then(() => setPulls((p) => ({ ...p, [model]: { status: "starting" } }))).catch((e) => setErr(e.message));
  const h = hw.data?.hardware;
  return (
    <>
      <Panel title="Ollama connection" actions={ai && <span className={"chip " + (ai.reachable ? "ok" : "crit")}>{ai.reachable ? `connected · v${ai.version}` : "not reachable"}</span>}>
        <ErrorNote error={err} />
        <form className="row" onSubmit={async (e) => { e.preventDefault(); if (await saveSettings({ ollama_url: url })) toast({ title: "Ollama endpoint saved", kind: "ok" }); }}>
          <input className="input mono grow" value={url} onChange={(e) => setUrl(e.target.value)} />
          <button className="btn primary">Save & test</button>
          <button type="button" className="btn" onClick={() => { reloadAI(); models.reload(); }}><Icon name="refresh" /> Re-test</button>
        </form>
        <div className="faint" style={{ fontSize: 12, marginTop: 8 }}>
          {ai?.reachable ? (ai.local ? "Inference runs on this machine; nothing leaves your network." : "This endpoint is not on this machine. Prompts (which can include logs and telemetry) are sent to it.") : `NEXUS could not reach Ollama: ${ai?.error ?? "checking…"}. Install it from ollama.com and run "ollama serve".`}
        </div>
        {ai?.running && ai.running.length > 0 && (
          <div className="row wrap" style={{ marginTop: 10, gap: 6 }}>{ai.running.map((r) => <span key={r.name} className="chip accent">{r.name} loaded · {bytes(r.size_vram)} in VRAM · ctx {r.context_length}</span>)}</div>
        )}
      </Panel>

      <Panel title="Model roles" actions={<span className="faint" style={{ fontSize: 12 }}>Unassigned roles are chosen automatically</span>}>
        {!roles.data || !settings.data ? <SkeletonRows n={4} /> : (
          <table className="t">
            <thead><tr><th>Role</th><th>Assigned model</th><th>Currently resolves to</th></tr></thead>
            <tbody>
              {roles.data.map((r) => {
                const res = ai?.roles?.find((x) => x.role === r.id);
                return (
                  <tr key={r.id}>
                    <td><b style={{ fontWeight: 500 }}>{r.name}</b><div className="faint" style={{ fontSize: 11.5 }}>{r.description}</div></td>
                    <td>
                      <select className="select" value={settings.data.roles?.[r.id] ?? ""} onChange={(e) => { const roles2 = { ...(settings.data.roles ?? {}) }; e.target.value ? (roles2[r.id] = e.target.value) : delete roles2[r.id]; saveSettings({ roles: roles2 }); }}>
                        <option value="">Automatic</option>
                        {(models.data ?? []).filter((m) => !m.capabilities.embedding || m.capabilities.completion).map((m) => <option key={m.name} value={m.name}>{m.name}{m.capabilities.tools ? "" : " (no tools)"}</option>)}
                      </select>
                    </td>
                    <td>{res ? <span className="mono" style={{ fontSize: 12 }}>{res.model}{res.automatic ? <span className="faint"> · auto</span> : ""}{!res.tools && <span className="warn-t"> · JSON protocol</span>}</span> : <span className="faint">{ai?.role_error ?? "—"}</span>}{res?.notice && <div className="warn-t" style={{ fontSize: 11 }}>{res.notice}</div>}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
      </Panel>

      <Panel title="Installed models" bodyClass="flush">
        <ErrorNote error={models.error} />
        {!models.data && !models.error ? <SkeletonRows /> : models.data?.length === 0 ? <Empty icon="ai" title="No models installed">Download one of the recommendations below.</Empty> : (
          <table className="t">
            <thead><tr><th>Model</th><th>Parameters</th><th>Quantization</th><th>Size</th><th>Capabilities</th><th>Context</th><th className="r" /></tr></thead>
            <tbody>
              {models.data?.map((m) => (
                <tr key={m.name}>
                  <td className="mono" style={{ fontWeight: 500 }}>{m.name}<div className="faint" style={{ fontSize: 11 }}>{m.details.family} · modified {time(m.modified_at)}</div></td>
                  <td className="mono">{m.details.parameter_size}</td>
                  <td className="mono faint">{m.details.quantization_level}</td>
                  <td className="mono">{bytes(m.size)}{m.fits_memory === false && <div className="warn-t" style={{ fontSize: 11 }}>exceeds memory budget</div>}</td>
                  <td>
                    <div className="row wrap" style={{ gap: 4 }}>
                      <span className={"chip " + (m.capabilities.tools ? "ok" : "warn")}>{m.capabilities.tools ? "tools" : "no tools"}</span>
                      {m.capabilities.thinking && <span className="chip info">thinking</span>}
                      {m.capabilities.vision && <span className="chip info">vision</span>}
                      {m.capabilities.embedding && <span className="chip">embedding</span>}
                    </div>
                    <div className="faint" style={{ fontSize: 10.5 }}>{m.capabilities.source}{m.capabilities_error ? ` · ${m.capabilities_error}` : ""}</div>
                  </td>
                  <td className="mono faint">{m.capabilities.context_length ? m.capabilities.context_length.toLocaleString() : "—"}</td>
                  <td className="r"><button className="btn ghost sm" onClick={() => { if (confirm(`Delete ${m.name} from Ollama?`)) api.del(`/api/ai/models/${encodeURIComponent(m.name)}`).then(() => { models.reload(); reloadAI(); }).catch((e) => setErr(e.message)); }}><Icon name="trash" size={13} /></button></td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Panel>

      <Panel title="Hardware & recommendations">
        {!h ? <SkeletonRows /> : (
          <div className="col" style={{ gap: 14 }}>
            <div className="grid g3">
              <div><div className="label">CPU</div><div style={{ marginTop: 4 }}>{h.cpu_model || "—"}</div><div className="faint" style={{ fontSize: 12 }}>{h.cpu_threads} threads</div></div>
              <div><div className="label">Memory</div><div style={{ marginTop: 4 }}>{bytes(h.ram_total)}</div><div className="faint" style={{ fontSize: 12 }}>{bytes(h.ram_available)} available</div><Bar value={h.ram_total ? (1 - h.ram_available / h.ram_total) * 100 : 0} /></div>
              <div><div className="label">GPU</div>{(h.gpus ?? []).length === 0 ? <div className="faint" style={{ marginTop: 4 }}>No GPU detected — CPU inference</div> : h.gpus.map((g: any) => <div key={g.name} style={{ marginTop: 4 }}>{g.name}<div className="faint" style={{ fontSize: 12 }}>{g.vram_bytes ? bytes(g.vram_bytes) + " VRAM" : "VRAM unknown"} · {g.source}</div></div>)}</div>
            </div>
            {(h.notes ?? []).map((n: string) => <Notice key={n}>{n}</Notice>)}
            <div className="faint" style={{ fontSize: 12 }}>Model memory budget: <b>{bytes(hw.data.budget_bytes)}</b>. Models are only downloaded when you click Download.</div>
            <table className="t">
              <thead><tr><th>Role</th><th>Suggested model</th><th>Download</th><th>Why</th><th className="r" /></tr></thead>
              <tbody>
                {hw.data.recommendations.map((r: any, i: number) => {
                  const pr = pulls[r.model];
                  return (
                    <tr key={i}>
                      <td>{r.role}</td>
                      <td className="mono">{r.model}</td>
                      <td className="mono faint">{r.approx_download}</td>
                      <td className="dim" style={{ fontSize: 12.5 }}>{r.why}</td>
                      <td className="r" style={{ minWidth: 170 }}>
                        {r.installed ? <span className="chip ok">installed</span> : pr && !pr.done ? <PullProgress p={pr} /> : <button className="btn sm" disabled={!ai?.reachable} onClick={() => pull(r.model)}><Icon name="download" size={12} /> Download</button>}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
            <form className="row" onSubmit={(e) => { e.preventDefault(); if (custom.trim()) { pull(custom.trim()); setCustom(""); } }}>
              <input className="input mono" style={{ width: 300 }} placeholder="Any Ollama library tag, e.g. llama3.1:8b" value={custom} onChange={(e) => setCustom(e.target.value)} />
              <button className="btn" disabled={!ai?.reachable}><Icon name="download" /> Download model</button>
            </form>
            {Object.entries(pulls).filter(([m, p]) => !p.done && !hw.data.recommendations.some((r: any) => r.model === m)).map(([m, p]) => <div key={m} className="row"><span className="mono">{m}</span><PullProgress p={p} /></div>)}
          </div>
        )}
      </Panel>
    </>
  );
}

function PullProgress({ p }: { p: any }) {
  const v = p.total ? (p.completed / p.total) * 100 : 0;
  return (
    <div style={{ width: 170 }}>
      <div className="faint ellipsis" style={{ fontSize: 11 }}>{p.status}{p.total ? ` · ${bytes(p.completed)} / ${bytes(p.total)}` : ""}</div>
      <Bar value={v} />
    </div>
  );
}

function AIBehaviour() {
  const { toast } = useApp();
  const s = useFetch<any>("/api/settings/ai", []);
  const [f, setF] = useState<any>(null);
  const [err, setErr] = useState<string | null>(null);
  useEffect(() => { if (s.data) setF(s.data); }, [s.data]);
  if (!f) return <Panel><SkeletonRows /></Panel>;
  const save = () => api.put("/api/settings/ai", { ...f, temperature: Number(f.temperature), num_ctx: Number(f.num_ctx), max_tool_rounds: Number(f.max_tool_rounds), approval_ttl_minutes: Number(f.approval_ttl_minutes), investigations_per_hour: Number(f.investigations_per_hour) }).then(() => { toast({ title: "AI behaviour saved", kind: "ok" }); s.reload(); }).catch((e) => setErr(e.message));
  const fld = (k: string, label: string, hint: string, props: any = {}) => (
    <label className="field"><span>{label}</span><input className="input mono" value={f[k]} onChange={(e) => setF({ ...f, [k]: e.target.value })} {...props} /><span className="hint">{hint}</span></label>
  );
  return (
    <Panel title="Inference & agent loop" actions={<button className="btn primary" onClick={save}>Save</button>}>
      <ErrorNote error={err} />
      <div className="grid g2" style={{ gap: 16 }}>
        {fld("temperature", "Temperature", "0–2. Low values keep diagnoses deterministic (default 0.2).", { type: "number", step: 0.05, min: 0, max: 2 })}
        {fld("num_ctx", "Context window (tokens)", "Passed to Ollama as num_ctx. Larger windows use more memory.", { type: "number", step: 1024 })}
        {fld("max_tool_rounds", "Max tool rounds per turn", "Upper bound on investigation steps before NEXUS stops and reports.", { type: "number" })}
        {fld("keep_alive", "Model keep-alive", "How long Ollama keeps the model loaded after a request (e.g. 15m).")}
        {fld("approval_ttl_minutes", "Approval expiry (minutes)", "Unanswered approval requests expire and nothing is executed.", { type: "number" })}
        {fld("investigations_per_hour", "Automatic investigations per hour", "Rate limit for monitoring-triggered AI investigations.", { type: "number" })}
      </div>
      <div style={{ marginTop: 14 }}><Notice>External AI providers are not configured. NEXUS's AI runs only through your Ollama endpoint; no paid API is required.</Notice></div>
    </Panel>
  );
}

// ---------------------------------------------------------------- monitoring

function Monitoring() {
  const { toast } = useApp();
  const s = useFetch<any>("/api/settings/monitoring", []);
  const [f, setF] = useState<any>(null);
  const [err, setErr] = useState<string | null>(null);
  useEffect(() => { if (s.data) setF(s.data); }, [s.data]);
  if (!f) return <Panel><SkeletonRows /></Panel>;
  const numFields = Object.fromEntries(Object.entries(f).map(([k, v]) => [k, typeof v === "string" && !isNaN(Number(v)) && k !== "InvestigateMinSeverity" ? Number(v) : v]));
  const save = () => api.put("/api/settings/monitoring", numFields).then(() => { toast({ title: "Monitoring settings saved", kind: "ok" }); s.reload(); }).catch((e) => setErr(e.message));
  const pair = (label: string, w: string, c: string, unit = "%") => (
    <div className="row" style={{ padding: "8px 0", borderBottom: "1px solid var(--line)" }}>
      <span className="grow">{label}</span>
      <span className="warn-t" style={{ fontSize: 12 }}>warning</span><input className="input mono" style={{ width: 80 }} type="number" value={f[w]} onChange={(e) => setF({ ...f, [w]: e.target.value })} /><span className="faint">{unit}</span>
      {c && <><span className="crit-t" style={{ fontSize: 12, marginLeft: 10 }}>critical</span><input className="input mono" style={{ width: 80 }} type="number" value={f[c]} onChange={(e) => setF({ ...f, [c]: e.target.value })} /><span className="faint">{unit}</span></>}
    </div>
  );
  const sec = (k: string, label: string) => (
    <label className="field"><span>{label}</span><input className="input mono" type="number" value={f[k]} onChange={(e) => setF({ ...f, [k]: e.target.value })} /><span className="hint">{duration(Number(f[k]))}</span></label>
  );
  return (
    <>
      <ErrorNote error={err} />
      <Panel title="Alert thresholds" actions={<button className="btn primary" onClick={save}>Save</button>}>
        {pair("CPU utilisation (sustained)", "CPUWarn", "CPUCrit")}
        {pair("Memory utilisation (sustained)", "MemWarn", "MemCrit")}
        {pair("Swap utilisation (sustained)", "SwapWarn", "")}
        {pair("Filesystem capacity", "DiskWarn", "DiskCrit")}
        {pair("Inode usage", "InodeWarn", "InodeCrit")}
        {pair("Load per CPU thread (sustained)", "LoadPerThreadWarn", "", "×")}
        {pair("Hardware temperature", "TempWarn", "TempCrit", "°C")}
        <div className="grid g3" style={{ marginTop: 14, gap: 14 }}>
          {sec("SustainSec", "Sustain window (s)")}
          {sec("NotifyCooldownSec", "Notification cooldown (s)")}
          {sec("IncidentWindowSec", "Incident correlation window (s)")}
        </div>
        <div className="faint" style={{ fontSize: 12, marginTop: 10 }}>Rule-based checks also cover failed systemd units, unhealthy/exited/restarting containers, pending security updates, reboot-required, security findings, unexpected reboots and unreachable agents.</div>
      </Panel>
      <Panel title="AI investigation of incidents">
        <div className="col" style={{ gap: 12 }}>
          <Switch on={!!f.AutoInvestigate} onChange={(v) => setF({ ...f, AutoInvestigate: v })} label="Automatically investigate new incidents with the local AI" />
          <div className="row"><span style={{ width: 220 }}>Minimum incident severity</span><Seg options={[{ id: "warning", label: "Warning" }, { id: "critical", label: "Critical" }]} value={f.InvestigateMinSeverity} onChange={(v) => setF({ ...f, InvestigateMinSeverity: v })} /></div>
          <Notice>Investigations are read-only unless a server's policy is set to restricted autonomous remediation. Servers set to read-only monitoring are never investigated automatically. <a href="#/policies">Policies</a></Notice>
        </div>
      </Panel>
      <Panel title="Collection intervals">
        <div className="grid g3" style={{ gap: 14 }}>
          {sec("PollIntervalSec", "Telemetry poll (s)")}
          {sec("ServicesIntervalSec", "Service inventory (s)")}
          {sec("DockerIntervalSec", "Docker inventory (s)")}
          {sec("UpdatesIntervalSec", "Package update check (s)")}
          {sec("PostureIntervalSec", "Security posture (s)")}
        </div>
        <div className="faint" style={{ fontSize: 12, marginTop: 10 }}>Agents sample every 5 s locally and buffer 20 minutes, so a slower poll never loses samples. Package and posture checks run sparingly to keep servers quiet.</div>
      </Panel>
    </>
  );
}

// ---------------------------------------------------------------- notifications

function Notifications() {
  const { toast } = useApp();
  const s = useFetch<any>("/api/settings/notifications", []);
  const [f, setF] = useState<any>(null);
  const [err, setErr] = useState<string | null>(null);
  useEffect(() => { if (s.data) setF({ ...s.data, discord: { ...s.data.discord, webhook: "" }, telegram: { ...s.data.telegram, token: "" }, email: { ...s.data.email, password: "" } }); }, [s.data]);
  if (!f) return <Panel><SkeletonRows /></Panel>;
  const save = () => api.put("/api/settings/notifications", { ...f, email: { ...f.email, port: Number(f.email.port) || 0 } }).then(() => { toast({ title: "Notification settings saved", kind: "ok" }); s.reload(); }).catch((e) => setErr(e.message));
  const test = (ch: string) => api.post("/api/settings/notifications/test", { channel: ch }).then(() => toast({ title: `Test sent via ${ch}`, kind: "ok" })).catch((e) => toast({ title: `${ch} test failed`, body: e.message, kind: "crit" }));
  const up = (ch: string, k: string, v: any) => setF({ ...f, [ch]: { ...f[ch], [k]: v } });
  const secret = (ch: string, k: string, label: string, set: boolean) => (
    <label className="field"><span>{label}</span><input className="input mono" type="password" placeholder={set ? "•••••••• stored encrypted — leave blank to keep" : "not set"} value={f[ch][k]} onChange={(e) => up(ch, k, e.target.value)} /></label>
  );
  return (
    <>
      <ErrorNote error={err} />
      <Panel title="Desktop" actions={<button className="btn sm" onClick={() => { if ("Notification" in window) Notification.requestPermission(); test("desktop"); }}>Send test</button>}>
        <div className="dim">Alerts, approvals and AI reports always appear in the notification center and as toasts. Native Windows notifications are shown when NEXUS is in the background and permission is granted.</div>
        <div className="row" style={{ marginTop: 12 }}><span style={{ width: 220 }}>External channels notify from</span><Seg options={[{ id: "warning", label: "Warning" }, { id: "critical", label: "Critical only" }]} value={f.min_severity} onChange={(v) => setF({ ...f, min_severity: v })} /></div>
      </Panel>
      <Notice>External channels are opt-in. Alert titles and details (which can include hostnames and log excerpts) are sent to the service you configure. Secrets are encrypted with Windows DPAPI.</Notice>
      <Panel title="Discord webhook" actions={<div className="row"><Switch on={f.discord.enabled} onChange={(v) => up("discord", "enabled", v)} /><button className="btn sm" disabled={!f.discord.set} onClick={() => test("discord")}>Test</button></div>}>
        {secret("discord", "webhook", "Webhook URL", f.discord.set)}
      </Panel>
      <Panel title="Telegram bot" actions={<div className="row"><Switch on={f.telegram.enabled} onChange={(v) => up("telegram", "enabled", v)} /><button className="btn sm" disabled={!f.telegram.set} onClick={() => test("telegram")}>Test</button></div>}>
        <div className="grid g2" style={{ gap: 10 }}>{secret("telegram", "token", "Bot token", f.telegram.set)}<label className="field"><span>Chat ID</span><input className="input mono" value={f.telegram.chat_id} onChange={(e) => up("telegram", "chat_id", e.target.value)} /></label></div>
      </Panel>
      <Panel title="E-mail (SMTP)" actions={<div className="row"><Switch on={f.email.enabled} onChange={(v) => up("email", "enabled", v)} /><button className="btn sm" disabled={!f.email.host} onClick={() => test("email")}>Test</button></div>}>
        <div className="grid g3" style={{ gap: 10 }}>
          <label className="field"><span>SMTP host</span><input className="input mono" value={f.email.host} onChange={(e) => up("email", "host", e.target.value)} /></label>
          <label className="field"><span>Port</span><input className="input mono" type="number" value={f.email.port || ""} placeholder="587" onChange={(e) => up("email", "port", e.target.value)} /></label>
          <label className="field"><span>Username</span><input className="input mono" value={f.email.username} onChange={(e) => up("email", "username", e.target.value)} /></label>
          {secret("email", "password", "Password", f.email.set)}
          <label className="field"><span>From</span><input className="input mono" value={f.email.from} onChange={(e) => up("email", "from", e.target.value)} /></label>
          <label className="field"><span>To (comma separated)</span><input className="input mono" value={f.email.to} onChange={(e) => up("email", "to", e.target.value)} /></label>
        </div>
        <div className="faint" style={{ fontSize: 12, marginTop: 8 }}>STARTTLS is used when the server offers it; credentials are never sent over an unencrypted connection.</div>
      </Panel>
      <div><button className="btn primary" onClick={save}>Save notification settings</button></div>
    </>
  );
}

// ---------------------------------------------------------------- security & about

function Security() {
  const { bootstrap } = useApp();
  const v = useFetch<any>("/api/audit/verify", []);
  return (
    <>
      <Panel title="Core identity">
        <KV
          rows={[
            ["Client certificate", <span className="mono" style={{ fontSize: 12 }}>{bootstrap?.core_fingerprint?.match(/.{2}/g)?.join(":")}</span>],
            ["Expires", bootstrap ? time(bootstrap.core_cert_expires) : "—"],
            ["Private key storage", "Sealed with Windows DPAPI (current user) — or an AES-256-GCM key file on other platforms"],
            ["Agent authentication", "Mutual TLS 1.3 with pinned certificate fingerprints in both directions"],
          ]}
        />
      </Panel>
      <Panel title="Local console">
        <KV rows={[["Listening", "127.0.0.1 only (random port per launch)"], ["Session", "256-bit token per launch, HttpOnly SameSite=Strict cookie"], ["Cross-site protection", "Host and Origin headers must match the loopback origin"]]} />
      </Panel>
      <Panel title="Audit trail">
        {!v.data ? <SkeletonRows n={2} /> : v.data.intact ? <Notice icon="check">Hash chain intact across {v.data.verified_records} records.</Notice> : <Notice kind="crit">Hash chain broken at record {v.data.first_broken_id}.</Notice>}
        <div style={{ marginTop: 10 }}><a className="btn sm" href="#/activity">Open audit log</a></div>
      </Panel>
      <Panel title="AI safety guarantees">
        <ul className="dim" style={{ margin: 0, paddingLeft: 18, lineHeight: 1.8 }}>
          <li>The AI can only call typed tools; there is no shell or arbitrary command tool.</li>
          <li>Every call passes the execution policy; high-risk operations always require explicit approval with an impact preview.</li>
          <li>Firewall, SSH and user-permission changes are never made automatically.</li>
          <li>Credentials and secret values are never placed in prompts; container environment values are not collected.</li>
          <li>Every non-read operation and every approval decision is written to the hash-chained audit log.</li>
        </ul>
      </Panel>
    </>
  );
}

function About() {
  const { bootstrap } = useApp();
  const sys = useFetch<any>("/api/system", [], 30000);
  return (
    <Panel title="About NEXUS">
      <KV
        rows={[
          ["Version", bootstrap?.version],
          ["Data directory", <span className="mono">{bootstrap?.data_dir}</span>],
          ["Database schema", `v${bootstrap?.schema}`],
          ["Database size", sys.data ? bytes(sys.data.db_bytes) : "—"],
          ["Telemetry", sys.data ? `${sys.data.telemetry_rows.toLocaleString()} raw samples (48 h) · ${sys.data.rollup_rows.toLocaleString()} 5-minute rollups (30 days)` : "—"],
          ["Core uptime", sys.data ? duration(sys.data.uptime_sec) : "—"],
          ["Live event stream", sys.data ? `${sys.data.events_dropped} events dropped for slow clients` : "—"],
        ]}
      />
    </Panel>
  );
}

// ---------------------------------------------------------------- updates

export type UpdateStatus = {
  enabled: boolean;
  disabled_reason?: string;
  current: string;
  latest?: string;
  state: string;
  notes?: string;
  released?: string;
  progress?: number;
  error?: string;
  last_check?: string;
  can_install: boolean;
  release_url?: string;
  settings: { auto_check: boolean; auto_install: boolean };
};

export function useUpdateStatus() {
  const { on } = useApp();
  const [s, setS] = useState<UpdateStatus | null>(null);
  useEffect(() => {
    api.get<UpdateStatus>("/api/update").then(setS).catch(() => {});
    return on((e) => e.type === "update" && setS(e.data));
  }, [on]);
  return [s, setS] as const;
}

export async function restartToUpdate(toast: (t: any) => void) {
  try {
    await api.post("/api/update/apply");
    toast({ title: "Installing update", body: "NEXUS will close and reopen in a few seconds.", kind: "ok" });
  } catch (e: any) {
    toast({ title: "Could not install the update", body: e.message, kind: "crit" });
  }
}

function Updates() {
  const { toast } = useApp();
  const [s, setS] = useUpdateStatus();
  if (!s) return <Panel><SkeletonRows n={4} /></Panel>;
  const label: Record<string, string> = {
    idle: "Not checked yet", no_release: "No release published yet", checking: "Checking GitHub for a new release…", up_to_date: "NEXUS is up to date", available: "A new version is available",
    downloading: "Downloading the update…", ready: "Update downloaded and verified", error: "Update check failed",
  };
  const saveSettings = (p: Partial<UpdateStatus["settings"]>) =>
    api.put<UpdateStatus["settings"]>("/api/settings/updates", { ...s.settings, ...p }).then((settings) => setS({ ...s, settings })).catch((e) => toast({ title: "Could not save", body: e.message, kind: "crit" }));
  return (
    <>
      <Panel title="NEXUS version">
        <div className="row" style={{ gap: 18, alignItems: "flex-start" }}>
          <div>
            <div className="label">Installed</div>
            <div className="mono" style={{ fontSize: 22, marginTop: 4 }}>{s.current}</div>
          </div>
          <div>
            <div className="label">Latest release</div>
            <div className="mono" style={{ fontSize: 22, marginTop: 4 }}>{s.latest ?? "—"}</div>
            {s.released && <div className="faint" style={{ fontSize: 12 }}>published {time(s.released)}</div>}
          </div>
          <div className="grow" />
          <div className="col" style={{ alignItems: "flex-end", gap: 8 }}>
            <span className={"chip " + (s.state === "ready" ? "accent" : s.state === "up_to_date" ? "ok" : s.state === "error" ? "crit" : "")}>{label[s.state] ?? s.state}</span>
            <div className="row">
              <button className="btn" disabled={!s.enabled || s.state === "checking" || s.state === "downloading"} onClick={() => api.post<UpdateStatus>("/api/update/check").then(setS).catch((e) => toast({ title: "Check failed", body: e.message, kind: "crit" }))}>
                <Icon name="refresh" className={s.state === "checking" ? "spin" : ""} /> Check now
              </button>
              {s.state === "ready" && s.can_install && <button className="btn primary" onClick={() => restartToUpdate(toast)}><Icon name="download" /> Restart &amp; update</button>}
              {s.state === "available" && !s.can_install && s.release_url && <a className="btn primary" href={s.release_url} target="_blank" rel="noreferrer">Open release</a>}
            </div>
          </div>
        </div>
        {s.state === "downloading" && <div style={{ marginTop: 12 }}><Bar value={(s.progress ?? 0) * 100} thick /></div>}
        {!s.enabled && <div style={{ marginTop: 12 }}><Notice>{s.disabled_reason}</Notice></div>}
        {s.error && <div style={{ marginTop: 12 }}><Notice kind="crit">{s.error}</Notice></div>}
        {s.last_check && <div className="faint" style={{ fontSize: 12, marginTop: 10 }}>Last checked {time(s.last_check)}</div>}
      </Panel>
      {s.notes && s.latest && (
        <Panel title={`What's new in ${s.latest}`}>
          <pre className="code" style={{ maxHeight: 320, whiteSpace: "pre-wrap" }}>{s.notes}</pre>
        </Panel>
      )}
      <Panel title="Update behaviour">
        <div className="col" style={{ gap: 12 }}>
          <Switch on={s.settings.auto_check} onChange={(v) => saveSettings({ auto_check: v })} label="Check for updates automatically (at start-up and every 6 hours)" />
          <Switch on={s.settings.auto_install} onChange={(v) => saveSettings({ auto_install: v })} label="Install downloaded updates automatically the next time NEXUS starts" />
          <Notice icon="lock">Updates come from this project's GitHub releases. NEXUS installs one only if its manifest is signed with the project's release key (built into this app) and the installer matches the signed SHA-256. Your servers, history and keys in %LOCALAPPDATA%\NEXUS are kept.</Notice>
        </div>
      </Panel>
    </>
  );
}
