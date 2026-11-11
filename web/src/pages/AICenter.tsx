import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { api } from "../lib/api";
import { navigate, useApp, useFetch } from "../lib/state";
import { Icon } from "../components/Icon";
import { ErrorNote, JSONView, Notice, RiskChip, SkeletonRows } from "../components/UI";
import { Markdown } from "../components/Markdown";
import { ago, bytes, pct, time, until } from "../lib/format";
import type { Approval, Conversation, Execution, Message, Mode } from "../lib/types";
import { ExecDrawer } from "./ServerTabs";

export const MODES: { id: Mode; label: string; desc: string; color: string }[] = [
  { id: "assistant", label: "Assistant", desc: "Answers and explains. Read-only.", color: "var(--info)" },
  { id: "analyst", label: "Analyst", desc: "Investigates and diagnoses. Read-only.", color: "var(--accent)" },
  { id: "administrator", label: "Administrator", desc: "May request changes; approvals per policy.", color: "var(--warn)" },
  { id: "autonomous", label: "Autonomous", desc: "Acts within each server's autonomous allowlist.", color: "var(--crit)" },
];

const EXAMPLES = [
  "Check all my servers and tell me if anything needs attention.",
  "Why is my server running slowly?",
  "Find out what is consuming all my memory.",
  "Review Docker containers and identify anything unhealthy.",
  "Check for security updates.",
  "Find large files consuming disk space under /var.",
  "Analyze the system logs from the last 24 hours for errors.",
  "Check failed SSH logins over the last day.",
];

export function AICenter({ convId }: { convId?: string }) {
  const { ai } = useApp();
  const list = useFetch<Conversation[]>("/api/conversations?limit=200", []);
  const { on } = useApp();
  useEffect(() => on((e) => (e.type === "chat.conversation" || (e.type === "chat.message" && !list.data?.some((c) => c.id === e.data.conversation_id))) && list.reload()), [on, list.data]);
  const create = async (mode: Mode = "analyst", server_id = "") => {
    const c = await api.post<Conversation>("/api/conversations", { mode, server_id });
    list.reload();
    navigate("/ai/" + c.id);
  };
  return (
    <div className="page full" style={{ display: "grid", gridTemplateColumns: "264px minmax(0,1fr)", height: "100%" }}>
      <aside style={{ borderRight: "1px solid var(--line)", background: "var(--chrome)", display: "flex", flexDirection: "column", minHeight: 0 }}>
        <div style={{ padding: 12 }}>
          <button className="btn primary" style={{ width: "100%" }} onClick={() => create()}><Icon name="plus" /> New conversation</button>
        </div>
        <div style={{ overflow: "auto", flex: 1, padding: "0 8px 12px" }}>
          {!list.data && <SkeletonRows n={6} />}
          {list.data?.length === 0 && <div className="faint" style={{ padding: 12, fontSize: "var(--fs-sm)" }}>No conversations yet.</div>}
          {list.data?.map((c) => (
            <a key={c.id} href={"#/ai/" + c.id} className={"nav-item" + (c.id === convId ? " active" : "")} style={{ height: "auto", padding: "8px 10px", alignItems: "flex-start", flexDirection: "column", gap: 2 }}>
              <span className="ellipsis" style={{ width: "100%", color: "var(--text)" }}>
                {c.origin === "monitor" && <Icon name="bolt" size={12} style={{ marginRight: 4, color: "var(--warn)" }} />}
                {c.title}
              </span>
              <span className="row faint" style={{ fontSize: 11, gap: 6 }}>
                <ModeDot mode={c.mode} /> {c.mode} · {ago(c.updated_at)}
              </span>
            </a>
          ))}
        </div>
        <div style={{ padding: 12, borderTop: "1px solid var(--line)", fontSize: "var(--fs-xs)" }} className="faint">
          <span className={"dot " + (ai?.reachable ? "ok" : "crit")} style={{ marginRight: 6 }} />
          {ai?.reachable ? `Ollama ${ai.version} · ${ai.local ? "local" : "remote"} · private` : "Ollama not reachable"}
        </div>
      </aside>
      {convId ? <Thread key={convId} id={convId} onChanged={list.reload} /> : <Welcome onStart={create} />}
    </div>
  );
}

function ModeDot({ mode }: { mode: string }) {
  const m = MODES.find((x) => x.id === mode);
  return <span style={{ width: 7, height: 7, borderRadius: 2, background: m?.color ?? "var(--idle)", display: "inline-block" }} />;
}

function Welcome({ onStart }: { onStart: (m?: Mode) => void }) {
  const { ai } = useApp();
  return (
    <div className="ambient-grid" style={{ overflow: "auto" }}>
      <div style={{ maxWidth: 860, margin: "8vh auto", padding: "0 24px" }}>
        <div className="label accent-t">AI Command Center</div>
        <h1 style={{ fontFamily: "var(--font-display)", fontSize: 34, fontWeight: 600, margin: "8px 0 8px", letterSpacing: "-0.02em" }}>Talk to your infrastructure.</h1>
        <p className="dim" style={{ fontSize: 15, maxWidth: 640 }}>
          NEXUS investigates with typed, audited tools before answering, separates facts from hypotheses, and routes every change through your execution policy. Inference runs locally in Ollama.
        </p>
        {ai && !ai.reachable && <div style={{ margin: "16px 0" }}><Notice kind="warn">Ollama is not reachable at {ai.url}: {ai.error}. <a href="#/settings/ai">Configure the local AI</a>.</Notice></div>}
        {ai?.reachable && ai.models === 0 && <div style={{ margin: "16px 0" }}><Notice kind="warn">Ollama is running but has no models. <a href="#/settings/ai">Download a recommended model</a>.</Notice></div>}
        <div className="grid g2" style={{ marginTop: 24 }}>
          {MODES.map((m) => (
            <button key={m.id} className="panel" style={{ textAlign: "left", padding: 16, cursor: "pointer", borderColor: "var(--line)" }} onClick={() => onStart(m.id)}>
              <div className="row"><span style={{ width: 10, height: 10, borderRadius: 3, background: m.color }} /><b>{m.label} mode</b></div>
              <div className="dim" style={{ fontSize: "var(--fs-sm)", marginTop: 6 }}>{m.desc}</div>
            </button>
          ))}
        </div>
      </div>
    </div>
  );
}

type Draft = { content: string; thinking: string };

function Thread({ id, onChanged }: { id: string; onChanged: () => void }) {
  const { servers, on, toast, serverById, live } = useApp();
  const conv = useFetch<{ conversation: Conversation; messages: Message[]; executions: Execution[] }>(`/api/conversations/${id}`, [id]);
  const [msgs, setMsgs] = useState<Message[]>([]);
  const [exes, setExes] = useState<Execution[]>([]);
  const [c, setC] = useState<Conversation | null>(null);
  const [state, setState] = useState<{ state: string; model?: string; round?: number; error?: string }>({ state: "idle" });
  const [draft, setDraft] = useState<Draft | null>(null);
  const [text, setText] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [openExe, setOpenExe] = useState<Execution | null>(null);
  const scroller = useRef<HTMLDivElement>(null);
  const stick = useRef(true);

  useEffect(() => {
    if (conv.data) {
      setMsgs(conv.data.messages);
      setExes(conv.data.executions);
      setC(conv.data.conversation);
      const last = conv.data.messages[conv.data.messages.length - 1];
      if (last?.role === "user" || last?.role === "tool") setState({ state: "thinking" });
    }
  }, [conv.data]);

  useEffect(
    () =>
      on((e) => {
        const d = e.data;
        if (e.type === "chat.message" && d.conversation_id === id) {
          setMsgs((m) => (m.some((x) => x.id === d.id) ? m : [...m, d]));
          if (d.role === "assistant") setDraft(null);
        } else if (e.type === "chat.delta" && d.conversation_id === id) {
          setDraft((p) => ({ content: (p?.content ?? "") + (d.content ?? ""), thinking: (p?.thinking ?? "") + (d.thinking ?? "") }));
        } else if (e.type === "chat.state" && d.conversation_id === id) {
          setState(d);
          if (d.state !== "thinking") setDraft(null);
          if (d.state === "idle" || d.state === "awaiting_approval") onChanged();
        } else if (e.type === "execution" && d.conversation_id === id) {
          setExes((x) => {
            const i = x.findIndex((y) => y.id === d.id);
            if (i < 0) return [d, ...x];
            const n = [...x];
            n[i] = d;
            return n;
          });
        } else if (e.type === "chat.conversation" && d.id === id) {
          setC(d);
        }
      }),
    [on, id, onChanged],
  );

  useEffect(() => {
    const el = scroller.current;
    if (el && stick.current) el.scrollTop = el.scrollHeight;
  }, [msgs, draft]);

  const send = async (t: string) => {
    const content = t.trim();
    if (!content) return;
    setErr(null);
    try {
      await api.post(`/api/conversations/${id}/messages`, { content });
      setText("");
      stick.current = true;
      setState({ state: "thinking" });
    } catch (e: any) {
      setErr(e.message);
    }
  };
  const patch = async (p: Partial<Conversation>) => {
    try {
      const n = await api.patch<Conversation>(`/api/conversations/${id}`, p);
      setC(n);
      onChanged();
    } catch (e: any) {
      toast({ title: "Could not update conversation", body: e.message, kind: "crit" });
    }
  };
  const busy = state.state === "thinking";
  const exeById = useMemo(() => new Map(exes.map((e) => [e.id, e])), [exes]);
  const focused = c?.server_id ? serverById(c.server_id) : undefined;
  const mode = MODES.find((m) => m.id === c?.mode);

  if (conv.error) return <div style={{ padding: 24 }}><ErrorNote error={conv.error} /></div>;
  if (!c) return <div style={{ padding: 24 }}><SkeletonRows /></div>;
  return (
    <div style={{ display: "grid", gridTemplateColumns: "minmax(0,1fr) 320px", minHeight: 0 }}>
      <section style={{ display: "flex", flexDirection: "column", minHeight: 0 }}>
        <header className="row" style={{ padding: "10px 18px", borderBottom: "1px solid var(--line)", gap: 12 }}>
          <div className="grow" style={{ minWidth: 0 }}>
            <div className="ellipsis" style={{ fontWeight: 600 }}>{c.title}</div>
            <div className="faint" style={{ fontSize: "var(--fs-xs)" }}>{c.model ? `Model ${c.model}` : "Model chosen on first message"} · {c.origin === "monitor" ? "started by the monitoring engine" : "operator conversation"}</div>
          </div>
          <select className="select" value={c.server_id} onChange={(e) => patch({ server_id: e.target.value })} title="Focus server">
            <option value="">Whole fleet</option>
            {servers.map((s) => <option key={s.id} value={s.id}>{s.name}</option>)}
          </select>
          <div className="seg" role="radiogroup" aria-label="Operational mode">
            {MODES.map((m) => (
              <button key={m.id} className={c.mode === m.id ? "on" : ""} onClick={() => patch({ mode: m.id })} title={m.desc} style={c.mode === m.id ? { color: m.color, boxShadow: `inset 0 -2px 0 ${m.color}` } : undefined}>
                {m.label}
              </button>
            ))}
          </div>
          <button className="btn ghost icon" title="Delete conversation" onClick={() => api.del(`/api/conversations/${id}`).then(() => { onChanged(); navigate("/ai"); })}><Icon name="trash" /></button>
        </header>
        <div className="row" style={{ padding: "6px 18px", background: `color-mix(in srgb, ${mode?.color} 9%, transparent)`, borderBottom: "1px solid var(--line)", fontSize: "var(--fs-xs)" }}>
          <span style={{ width: 8, height: 8, borderRadius: 2, background: mode?.color }} />
          <b style={{ color: mode?.color, letterSpacing: ".08em", textTransform: "uppercase" }}>{mode?.label} mode</b>
          <span className="dim">{mode?.desc}</span>
          <span className="spacer" />
          {c.mode === "autonomous" && <a href={c.server_id ? `#/policies/${c.server_id}` : "#/policies"}>Review autonomous policy</a>}
        </div>
        <div ref={scroller} onScroll={(e) => { const el = e.currentTarget; stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 80; }} style={{ flex: 1, overflow: "auto", padding: "20px 0" }}>
          <div style={{ maxWidth: 880, margin: "0 auto", padding: "0 22px" }} className="col">
            {msgs.length === 0 && (
              <div>
                <div className="faint" style={{ marginBottom: 10 }}>Try one of these, or ask anything about {focused ? focused.name : "your fleet"}:</div>
                <div className="row wrap" style={{ gap: 8 }}>
                  {EXAMPLES.map((e) => <button key={e} className="btn sm" style={{ height: "auto", padding: "6px 10px", whiteSpace: "normal", textAlign: "left" }} onClick={() => send(e)}>{e}</button>)}
                </div>
              </div>
            )}
            {msgs.map((m) => <MessageView key={m.id} m={m} exe={m.execution_id ? exeById.get(m.execution_id) : undefined} onOpenExe={setOpenExe} />)}
            {busy && (
              <div className="row scan" style={{ padding: "10px 14px", borderRadius: "var(--radius)", background: "var(--surface)", border: "1px solid var(--line)" }}>
                <Icon name="ai" className="spin" style={{ color: "var(--accent)", animationDuration: "2.4s" }} />
                <span className="dim">{draft?.content ? "Writing" : "Investigating"}{state.round && state.round > 1 ? ` · step ${state.round}` : ""}{state.model ? ` · ${state.model}` : ""}</span>
                <span className="spacer" />
                <button className="btn sm ghost" onClick={() => api.post(`/api/conversations/${id}/stop`)}>Stop</button>
              </div>
            )}
            {draft && (draft.content || draft.thinking) && (
              <div className="msg-ai" style={{ opacity: 0.9 }}>
                {draft.thinking && !draft.content && <div className="faint mono" style={{ fontSize: 11, whiteSpace: "pre-wrap", maxHeight: 120, overflow: "hidden" }}>{draft.thinking.slice(-600)}</div>}
                {draft.content && <Markdown text={draft.content} />}
              </div>
            )}
          </div>
        </div>
        <div style={{ borderTop: "1px solid var(--line)", padding: "12px 18px", background: "var(--chrome)" }}>
          <div style={{ maxWidth: 880, margin: "0 auto" }}>
            <ErrorNote error={err} />
            <form className="row" style={{ alignItems: "flex-end" }} onSubmit={(e) => { e.preventDefault(); send(text); }}>
              <textarea
                className="input grow"
                rows={Math.min(6, Math.max(1, text.split("\n").length))}
                placeholder={`Ask NEXUS about ${focused ? focused.name : "your infrastructure"}…  (Enter to send, Shift+Enter for a new line)`}
                value={text}
                onChange={(e) => setText(e.target.value)}
                onKeyDown={(e) => { if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); send(text); } }}
                disabled={busy}
                style={{ minHeight: 40 }}
              />
              <button className="btn primary icon" style={{ height: 40, width: 40 }} disabled={busy || !text.trim()} aria-label="Send"><Icon name="send" /></button>
            </form>
          </div>
        </div>
      </section>
      <aside style={{ borderLeft: "1px solid var(--line)", overflow: "auto", background: "var(--chrome)" }}>
        <div style={{ padding: 16 }}>
          <div className="label" style={{ marginBottom: 10 }}>Investigation trace</div>
          {exes.length === 0 && <div className="faint" style={{ fontSize: "var(--fs-sm)" }}>Tools the AI calls appear here with their policy decision and outcome.</div>}
          <div className="col" style={{ gap: 0 }}>
            {[...exes].reverse().map((e, i) => (
              <button key={e.id} onClick={() => setOpenExe(e)} className="row" style={{ border: 0, background: "none", textAlign: "left", padding: "6px 0", cursor: "pointer", alignItems: "flex-start", gap: 10 }}>
                <div className="col" style={{ alignItems: "center", gap: 0, width: 12 }}>
                  <span className={"dot " + ({ succeeded: "ok", failed: "crit", blocked: "crit", pending_approval: "warn", running: "info" } as any)[e.status]} style={{ marginTop: 4 }} />
                  {i < exes.length - 1 && <span style={{ width: 1, height: 26, background: "var(--line-2)", marginTop: 4 }} />}
                </div>
                <div className="grow" style={{ minWidth: 0 }}>
                  <div className="mono ellipsis" style={{ fontSize: 12, color: "var(--text)" }}>{e.tool}</div>
                  <div className="faint" style={{ fontSize: 11 }}>{e.status.replace("_", " ")} · {e.risk}{e.started_at && e.finished_at ? ` · ${((Date.parse(e.finished_at) - Date.parse(e.started_at)) / 1000).toFixed(1)}s` : ""}</div>
                </div>
              </button>
            ))}
          </div>
        </div>
        {focused && (
          <div style={{ padding: 16, borderTop: "1px solid var(--line)" }}>
            <div className="row" style={{ marginBottom: 10 }}><div className="label grow">Focused server</div><a href={"#/server/" + focused.id} style={{ fontSize: 12 }}>Open</a></div>
            <div style={{ fontWeight: 600 }}>{focused.name}</div>
            <div className="faint" style={{ fontSize: 12, marginBottom: 10 }}>{focused.info?.os_name} · {focused.status}</div>
            {focused.latest && (
              <div className="grid g2" style={{ gap: 10 }}>
                <MiniStat label="CPU" v={pct(focused.latest.cpu.usage_pct)} />
                <MiniStat label="Memory" v={pct(focused.latest.mem.used_pct)} />
                <MiniStat label="Load" v={focused.latest.load[0].toFixed(2)} />
                <MiniStat label="Available" v={bytes(focused.latest.mem.available)} />
              </div>
            )}
            <div className="faint" style={{ fontSize: 11, marginTop: 8 }}>{live(focused.id).length} live samples this session</div>
          </div>
        )}
        <div style={{ padding: 16, borderTop: "1px solid var(--line)" }} className="faint">
          <div className="label" style={{ marginBottom: 6 }}>Privacy</div>
          <div style={{ fontSize: 12 }}>Prompts, logs and telemetry are sent only to your Ollama endpoint. Credentials are never included in prompts.</div>
        </div>
      </aside>
      {openExe && <ExecDrawer e={openExe} onClose={() => setOpenExe(null)} />}
    </div>
  );
}

function MiniStat({ label, v }: { label: string; v: string }) {
  return (
    <div>
      <div className="label">{label}</div>
      <div className="mono num" style={{ fontSize: 15 }}>{v}</div>
    </div>
  );
}

function MessageView({ m, exe, onOpenExe }: { m: Message; exe?: Execution; onOpenExe: (e: Execution) => void }) {
  if (m.role === "user") {
    const fromMonitor = m.meta?.origin === "monitor";
    return (
      <div style={{ alignSelf: fromMonitor ? "stretch" : "flex-end", maxWidth: fromMonitor ? "100%" : "78%", background: fromMonitor ? "var(--warn-soft)" : "var(--accent-soft)", border: "1px solid var(--line)", borderRadius: "12px 12px 3px 12px", padding: "10px 14px", whiteSpace: "pre-wrap" }}>
        {fromMonitor && <div className="label warn-t" style={{ marginBottom: 6 }}>Monitoring engine</div>}
        {m.content}
      </div>
    );
  }
  if (m.role === "assistant") {
    const calls = m.tool_calls ?? [];
    return (
      <div className="msg-ai" style={{ padding: "2px 0" }}>
        <div className="row" style={{ marginBottom: 6 }}>
          <span style={{ width: 20, height: 20, borderRadius: 6, background: "var(--accent-soft)", color: "var(--accent)", display: "grid", placeItems: "center" }}><Icon name="ai" size={12} /></span>
          <b style={{ fontSize: "var(--fs-sm)" }}>NEXUS</b>
          <span className="faint" style={{ fontSize: 11 }}>{time(m.created_at)}{m.meta?.tokens?.tps ? ` · ${m.meta.tokens.tps.toFixed(1)} tok/s` : ""}{m.meta?.protocol === "json" ? " · JSON tool protocol" : ""}</span>
        </div>
        {m.meta?.thinking && (
          <details style={{ marginBottom: 6 }}>
            <summary className="faint" style={{ cursor: "pointer", fontSize: 12 }}>Model reasoning</summary>
            <div className="faint mono" style={{ fontSize: 11.5, whiteSpace: "pre-wrap", padding: "6px 0" }}>{m.meta.thinking}</div>
          </details>
        )}
        {m.content && <Markdown text={m.content} />}
        {calls.length > 0 && (
          <div className="row wrap" style={{ gap: 6, marginTop: m.content ? 8 : 0 }}>
            {calls.map((c, i) => (
              <span key={i} className="chip outline mono" style={{ fontSize: 11 }}>
                <Icon name="bolt" size={11} /> {c.function.name}({Object.entries(c.function.arguments ?? {}).map(([k, v]) => `${k}: ${JSON.stringify(v)}`).join(", ")})
              </span>
            ))}
          </div>
        )}
      </div>
    );
  }
  if (m.role === "tool") return <ToolCard m={m} exe={exe} onOpen={onOpenExe} />;
  const kind = m.meta?.kind;
  if (kind === "approval") return <ApprovalCard id={m.meta!.approval_id} reason={m.meta!.reason} />;
  if (kind === "mode") return <div className="row faint" style={{ fontSize: 11, gap: 10 }}><span className="grow" style={{ height: 1, background: "var(--line)" }} />{m.content}<span className="grow" style={{ height: 1, background: "var(--line)" }} /></div>;
  return <Notice kind={kind === "error" ? "crit" : kind === "model_notice" ? "warn" : "info"}>{m.content}</Notice>;
}

function ToolCard({ m, exe, onOpen }: { m: Message; exe?: Execution; onOpen: (e: Execution) => void }) {
  const [open, setOpen] = useState(false);
  let data: any = null;
  try {
    data = JSON.parse(m.content);
  } catch {
    data = m.content;
  }
  const err = data?.error;
  const status = data?.status;
  const verification: string | undefined = data?.verification ?? data?.result?.verification;
  let cls = "ok";
  let summary = "completed";
  if (err) {
    cls = "crit";
    summary = String(err);
  } else if (status === "blocked_by_policy") {
    cls = "crit";
    summary = "blocked by policy: " + data.reason;
  } else if (status === "denied_by_operator") {
    cls = "warn";
    summary = "denied by operator" + (data.reason ? `: ${data.reason}` : "");
  } else if (status === "not_executed" || status === "expired") {
    cls = "";
    summary = data.reason;
  } else if (verification) {
    cls = verification.startsWith("verified") ? "ok" : "warn";
    summary = verification;
  } else if (exe) {
    summary = `${exe.status}${exe.started_at && exe.finished_at ? ` in ${((Date.parse(exe.finished_at) - Date.parse(exe.started_at)) / 1000).toFixed(1)}s` : ""}`;
  }
  return (
    <div style={{ border: "1px solid var(--line)", borderRadius: "var(--radius)", background: "var(--surface)", overflow: "hidden" }}>
      <button onClick={() => setOpen(!open)} className="row" style={{ width: "100%", border: 0, background: "none", padding: "7px 12px", cursor: "pointer", textAlign: "left" }}>
        <span className={"dot " + cls} />
        <span className="mono" style={{ fontSize: 12 }}>{m.tool_name}</span>
        {exe && <RiskChip risk={exe.risk} />}
        <span className="faint ellipsis grow" style={{ fontSize: 12 }}>{summary}</span>
        <Icon name={open ? "chevdown" : "chevron"} size={13} />
      </button>
      {open && (
        <div style={{ padding: "0 12px 12px" }}>
          {exe && (
            <div className="row faint" style={{ fontSize: 11, marginBottom: 8 }}>
              <span>policy: {exe.decision} — {exe.decision_reason}</span>
              <span className="spacer" />
              <button className="btn ghost sm" onClick={() => onOpen(exe)}>Execution record</button>
            </div>
          )}
          <JSONView value={data} max={360} />
        </div>
      )}
    </div>
  );
}

function ApprovalCard({ id, reason }: { id: string; reason?: string }) {
  const { on, toast } = useApp();
  const [a, setA] = useState<Approval | null>(null);
  const [confirm, setConfirm] = useState("");
  const [why, setWhy] = useState("");
  const [busy, setBusy] = useState(false);
  const load = useCallback(() => {
    api.get<Approval[]>("/api/approvals?limit=200").then((l) => setA(l.find((x) => x.id === id) ?? null)).catch(() => {});
  }, [id]);
  useEffect(load, [load]);
  useEffect(() => on((e) => e.type === "approval" && e.data.id === id && setA((p) => ({ ...(p as Approval), ...e.data }))), [on, id]);
  if (!a) return <div className="skel" style={{ height: 80 }} />;
  const imp = a.impact;
  const decide = async (approve: boolean) => {
    setBusy(true);
    try {
      await api.post(`/api/approvals/${id}/decide`, { approve, reason: why, confirm });
      load();
    } catch (e: any) {
      toast({ title: "Decision failed", body: e.message, kind: "crit" });
    } finally {
      setBusy(false);
    }
  };
  const pending = a.status === "pending";
  return (
    <div style={{ border: `1px solid ${pending ? "color-mix(in srgb, var(--warn) 45%, transparent)" : "var(--line)"}`, borderRadius: "var(--radius-lg)", background: pending ? "var(--warn-soft)" : "var(--surface)", padding: 14 }}>
      <div className="row" style={{ marginBottom: 8 }}>
        <Icon name="approval" style={{ color: pending ? "var(--warn)" : "var(--text-3)" }} />
        <b className="grow">{pending ? "Approval required" : ({ approved: "Approved", denied: "Denied", expired: "Expired — nothing was executed" } as Record<string, string>)[a.status] ?? a.status}</b>
        <RiskChip risk={a.risk} />
        <span className="faint" style={{ fontSize: 11 }}>{pending ? `expires ${until(a.expires_at)}` : a.decided_at ? `${a.decided_by} · ${ago(a.decided_at)}` : ""}</span>
      </div>
      <div style={{ fontSize: "var(--fs-lg)", marginBottom: 6 }}>{imp?.summary ?? a.summary}</div>
      {reason && <div className="faint" style={{ fontSize: 12, marginBottom: 8 }}>Policy: {reason}</div>}
      {imp?.effects && imp.effects.length > 0 && <ul style={{ margin: "0 0 8px", paddingLeft: 18 }} className="dim">{imp.effects.slice(0, 12).map((e, i) => <li key={i} style={{ fontSize: 12.5 }}>{e}</li>)}</ul>}
      {imp?.risks?.map((r, i) => <div key={i} className="row warn-t" style={{ fontSize: 12.5, marginBottom: 3 }}><Icon name="alert" size={12} /> {r}</div>)}
      {imp?.rollback && <div className="faint" style={{ fontSize: 12, marginTop: 6 }}>Rollback: {imp.rollback}</div>}
      {pending && (
        <div className="col" style={{ gap: 8, marginTop: 12 }}>
          {imp?.confirm && <input className="input mono" placeholder={`Type ${imp.confirm} to confirm`} value={confirm} onChange={(e) => setConfirm(e.target.value)} />}
          <div className="row">
            <input className="input grow" placeholder="Note (optional, stored in the audit log)" value={why} onChange={(e) => setWhy(e.target.value)} />
            <button className="btn" disabled={busy} onClick={() => decide(false)}>Deny</button>
            <button className={"btn " + (a.risk === "high" ? "danger" : "primary")} disabled={busy || (!!imp?.confirm && confirm !== imp.confirm)} onClick={() => decide(true)}><Icon name="check" /> Approve & execute</button>
          </div>
        </div>
      )}
    </div>
  );
}

export { ApprovalCard };
