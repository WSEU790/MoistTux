import { useEffect, useState } from "react";
import { api } from "../lib/api";
import { useApp, useFetch } from "../lib/state";
import { Icon } from "../components/Icon";
import { ErrorNote, Modal, Notice, SkeletonRows } from "../components/UI";

export type ChatGPTStatus = {
  linked: boolean; plan_enabled: boolean; email?: string; name?: string; registered: boolean; linked_at?: string; welcome_seen: boolean;
  flow?: "waiting" | "done" | "declined" | "failed"; flow_error?: string; auth_url?: string; callback_url?: string; listening: boolean; last_error?: string; usage_limited: boolean; usage_url: string; can_store_credentials: boolean;
};

export const USAGE_URL = "https://chatgpt.com/settings/usage";

/** Opens ChatGPT's usage page in the system browser (the desktop window would open links in an embedded window). */
export function openUsage() {
  api.post<{ opened: boolean; url: string }>("/api/ai/chatgpt/open-usage").then((r) => { if (!r.opened) window.open(r.url, "_blank", "noopener"); }).catch(() => window.open(USAGE_URL, "_blank", "noopener"));
}

export function ManageUsage({ primary }: { primary?: boolean }) {
  return <button type="button" className={"btn sm" + (primary ? " primary" : " ghost")} onClick={openUsage}>Manage usage <Icon name="external" size={12} /></button>;
}

/** "Using ChatGPT plan · Manage usage", shown near the composer when ChatGPT is the engine. */
export function UsingPlan() {
  const { ai } = useApp();
  if (ai?.provider !== "chatgpt") return null;
  return (
    <div className="row faint" style={{ fontSize: 12, gap: 8, marginTop: 6 }}>
      <span className="chip info">Using ChatGPT plan</span>
      <button type="button" className="btn ghost sm" onClick={openUsage}>Manage usage</button>
    </div>
  );
}

/** Settings card for linking the operator's ChatGPT subscription. */
export function ChatGPTCard({ active, onActivate }: { active: boolean; onActivate: () => Promise<boolean> }) {
  const { on, toast, reloadAI } = useApp();
  const st = useFetch<ChatGPTStatus>("/api/ai/chatgpt", []);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState<string | null>(null);
  useEffect(() => on((e) => { if (e.type === "ai.chatgpt") { st.reload(); reloadAI(); } }), [on]);

  // Once a sign-in completes with plan permission, switch the AI engine to ChatGPT.
  const s = st.data;
  const [switched, setSwitched] = useState(false);
  useEffect(() => {
    if (s?.flow === "done" && s.linked && s.plan_enabled && !active && !switched) {
      setSwitched(true);
      onActivate().then((ok) => ok && toast({ title: "AI engine: ChatGPT", body: s.email ? `Signed in as ${s.email}` : undefined, kind: "ok" }));
    }
  }, [s?.flow, s?.linked, s?.plan_enabled, active]);

  const signIn = async (opts: { new_account?: boolean; reconsent?: boolean } = {}) => {
    setErr(null);
    setNote(null);
    setSwitched(false);
    setBusy(true);
    try {
      const r = await api.post<{ auth_url: string; opened: boolean }>("/api/ai/chatgpt/signin", opts);
      if (!r.opened) window.open(r.auth_url, "_blank", "noopener");
      st.reload();
    } catch (e: any) {
      setErr(e.message);
    } finally {
      setBusy(false);
    }
  };
  const signOut = async () => {
    if (!confirm("Sign out of ChatGPT? NEXUS stops using your ChatGPT plan; choose another AI engine to keep using the AI.")) return;
    setErr(null);
    try {
      const r = await api.post<{ revoked: boolean; note?: string }>("/api/ai/chatgpt/signout");
      setNote(r.note ?? null);
      toast({ title: "Signed out of ChatGPT", kind: "ok" });
      st.reload();
      reloadAI();
    } catch (e: any) {
      setErr(e.message);
    }
  };

  if (!s) return <SkeletonRows n={3} />;
  const waiting = s.flow === "waiting";
  return (
    <div className="col" style={{ gap: 10 }}>
      <ErrorNote error={err} />
      {note && <Notice kind="warn">{note}</Notice>}
      {!s.linked ? (
        <div className="card" style={{ padding: 16 }}>
          <b>Use your ChatGPT plan</b>
          <p className="dim" style={{ margin: "6px 0 12px", fontSize: 13 }}>
            Run NEXUS's AI on ChatGPT models with the usage included in your ChatGPT plan. This is separate from NEXUS, which stays free; OpenAI counts the requests against your plan's limits.
          </p>
          {waiting ? (
            <div className="col" style={{ gap: 8 }}>
              <div className="row" style={{ gap: 8 }}><Icon name="refresh" size={12} className="spin" /> Finish signing in in your browser…</div>
              <div className="row" style={{ gap: 8 }}>
                {s.auth_url && <a className="btn sm" href={s.auth_url} target="_blank" rel="noreferrer noopener">Open the sign-in page again</a>}
                <button className="btn ghost sm" onClick={() => api.post("/api/ai/chatgpt/cancel").then(st.reload)}>Cancel</button>
              </div>
              <PasteFinish s={s} onDone={() => { st.reload(); reloadAI(); }} />
            </div>
          ) : (
            <button className="btn" style={{ background: "#000", color: "#fff", borderColor: "#000" }} disabled={busy || !s.can_store_credentials} onClick={() => signIn()}>
              Continue with ChatGPT
            </button>
          )}
          {!s.can_store_credentials && <div className="warn-t" style={{ fontSize: 12, marginTop: 8 }}>NEXUS cannot store credentials securely on this system.</div>}
        </div>
      ) : (
        <div className="card" style={{ padding: 16 }}>
          <div className="row" style={{ justifyContent: "space-between", flexWrap: "wrap", gap: 8 }}>
            <div>
              <div className="faint" style={{ fontSize: 12 }}>ChatGPT account</div>
              <b>{s.email || s.name || "Signed in"}</b>
              {s.linked_at && <div className="faint" style={{ fontSize: 11.5 }}>Linked {new Date(s.linked_at).toLocaleDateString()}</div>}
            </div>
            <div className="row wrap" style={{ gap: 6 }}>
              {s.plan_enabled && <span className="chip info">Using ChatGPT plan</span>}
              <ManageUsage />
              <button className="btn ghost sm" disabled={busy || waiting} onClick={() => signIn({ new_account: true })}>Use a different account</button>
              <button className="btn ghost sm" onClick={signOut}>Sign out</button>
            </div>
          </div>
          {s.plan_enabled && !active && (
            <div className="row" style={{ marginTop: 12, gap: 8 }}>
              <button className="btn primary sm" onClick={() => onActivate()}>Use ChatGPT as the AI engine</button>
            </div>
          )}
          {waiting && (
            <div className="col" style={{ marginTop: 10, gap: 8 }}>
              <div className="row faint" style={{ gap: 8, fontSize: 12 }}><Icon name="refresh" size={12} className="spin" /> Finish signing in in your browser… <button className="btn ghost sm" onClick={() => api.post("/api/ai/chatgpt/cancel").then(st.reload)}>Cancel</button></div>
              <PasteFinish s={s} onDone={() => { st.reload(); reloadAI(); }} />
            </div>
          )}
        </div>
      )}
      {s.linked && !s.plan_enabled && (
        <Notice kind="warn">
          You're signed in, but permission to use your ChatGPT plan was not granted, so NEXUS cannot run the AI on ChatGPT.{" "}
          <button className="btn sm" onClick={() => signIn({ reconsent: true })}>Enable ChatGPT plan use</button> or choose Ollama or Colibri above.
        </Notice>
      )}
      {s.usage_limited && (
        <Notice kind="warn">
          ChatGPT usage limit reached. NEXUS paused requests that use your plan (the limit may be your plan's or the one you set for NEXUS).{" "}
          <ManageUsage primary /> <button className="btn ghost sm" onClick={() => api.post("/api/ai/chatgpt/resume").then(() => { st.reload(); reloadAI(); })}>Resume</button>
        </Notice>
      )}
      {s.flow === "declined" && <Notice>You declined access in the browser, so ChatGPT was not linked. NEXUS keeps using its current engine.</Notice>}
      {s.flow === "failed" && s.flow_error && <Notice kind="crit">{s.flow_error}</Notice>}
      {s.last_error && <Notice kind="warn">{s.last_error}</Notice>}
      <Notice icon="info">
        With ChatGPT as the engine, your prompts and everything the AI's tools return (server names, logs, command output, telemetry) are sent to OpenAI. Automatic incident investigations use your plan too. Approvals and command policies still apply: ChatGPT can only use NEXUS's typed tools. Sign-in tokens are stored encrypted with your Windows account and are never shown to the model.
        {" "}Plus plans share a 5-hour usage limit across all apps that use your ChatGPT plan.
      </Notice>
    </div>
  );
}

/** Fallback when the browser cannot reach NEXUS's loopback callback. */
function PasteFinish({ s, onDone }: { s: ChatGPTStatus; onDone: () => void }) {
  const { toast } = useApp();
  const [open, setOpen] = useState(false);
  const [addr, setAddr] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  if (!open) {
    return (
      <div className="faint" style={{ fontSize: 12 }}>
        Browser says “This site can't be reached” after you approve?{" "}
        <button className="btn ghost sm" onClick={() => setOpen(true)}>Finish with the address instead</button>
      </div>
    );
  }
  const submit = async () => {
    setErr(null);
    setBusy(true);
    try {
      await api.post("/api/ai/chatgpt/complete", { url: addr });
      toast({ title: "ChatGPT is linked", kind: "ok" });
      setAddr("");
      setOpen(false);
      onDone();
    } catch (e: any) {
      setErr(e.message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="col" style={{ gap: 6 }}>
      <div className="dim" style={{ fontSize: 12.5 }}>
        In the browser tab that shows the error, copy the whole address from the address bar{s.callback_url ? <> (it starts with <span className="mono">{s.callback_url.replace(/\/auth\/callback$/, "")}</span>)</> : null} and paste it here. It works once and only for this sign-in.
      </div>
      <form className="row" style={{ gap: 6 }} onSubmit={(e) => { e.preventDefault(); submit(); }}>
        <input className="input mono grow" autoFocus placeholder="http://127.0.0.1:1455/auth/callback?code=…" value={addr} onChange={(e) => setAddr(e.target.value)} />
        <button className="btn primary sm" disabled={busy || !addr.trim()}>Finish sign-in</button>
      </form>
      <ErrorNote error={err} />
      {!s.listening && <div className="warn-t" style={{ fontSize: 12 }}>NEXUS is not listening for the browser right now, so pasting the address is the way to finish.</div>}
    </div>
  );
}

/** First sign-in confirmation, shown once. */
export function ChatGPTWelcome() {
  const { ai } = useApp();
  const st = useFetch<ChatGPTStatus>("/api/ai/chatgpt", [ai?.provider]);
  const [closed, setClosed] = useState(false);
  const s = st.data;
  if (closed || !s || !s.linked || !s.plan_enabled || s.welcome_seen || ai?.provider !== "chatgpt") return null;
  const done = () => { setClosed(true); api.post("/api/ai/chatgpt/welcome-seen").catch(() => {}); };
  return (
    <Modal title="You're using your ChatGPT plan" onClose={done} footer={<><button className="btn ghost" onClick={openUsage}>Manage usage</button><button className="btn primary" onClick={done}>Got it</button></>}>
      <p style={{ marginTop: 0 }}>NEXUS's AI requests now use your ChatGPT plan{s.email ? ` (${s.email})` : ""}. You can review usage and set a limit for NEXUS in ChatGPT settings.</p>
      <p className="dim" style={{ marginBottom: 0 }}>Prompts and tool results, including logs and telemetry from your servers, are sent to OpenAI. Switch back to Ollama or Colibri in Settings → AI engine at any time.</p>
    </Modal>
  );
}
