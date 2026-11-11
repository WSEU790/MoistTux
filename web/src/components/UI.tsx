import React, { useEffect, useMemo, useState } from "react";
import { Icon } from "./Icon";
import { api } from "../lib/api";
import { useApp } from "../lib/state";
import type { Impact, Risk } from "../lib/types";
import { riskClass } from "../lib/format";

export function Panel({ title, actions, children, className, bodyClass, style }: { title?: React.ReactNode; actions?: React.ReactNode; children: React.ReactNode; className?: string; bodyClass?: string; style?: React.CSSProperties }) {
  return (
    <section className={"panel " + (className ?? "")} style={style}>
      {(title || actions) && (
        <div className="panel-head">
          {typeof title === "string" ? <div className="panel-title">{title}</div> : title}
          <div className="spacer" />
          {actions}
        </div>
      )}
      <div className={"panel-body " + (bodyClass ?? "")}>{children}</div>
    </section>
  );
}

export function Modal({ title, sub, onClose, children, footer, wide }: { title: string; sub?: React.ReactNode; onClose: () => void; children: React.ReactNode; footer?: React.ReactNode; wide?: boolean }) {
  useEffect(() => {
    const k = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", k);
    return () => window.removeEventListener("keydown", k);
  }, [onClose]);
  return (
    <div className="overlay" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className={"modal" + (wide ? " wide" : "")} role="dialog" aria-modal="true" aria-label={title}>
        <div className="modal-head">
          <div className="grow">
            <h2>{title}</h2>
            {sub && <p>{sub}</p>}
          </div>
          <button className="btn ghost icon" onClick={onClose} aria-label="Close">
            <Icon name="x" />
          </button>
        </div>
        <div className="modal-body">{children}</div>
        {footer && <div className="modal-foot">{footer}</div>}
      </div>
    </div>
  );
}

export function Drawer({ title, sub, onClose, children, actions }: { title: React.ReactNode; sub?: React.ReactNode; onClose: () => void; children: React.ReactNode; actions?: React.ReactNode }) {
  useEffect(() => {
    const k = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", k);
    return () => window.removeEventListener("keydown", k);
  }, [onClose]);
  return (
    <>
      <div className="overlay" style={{ background: "rgba(3,5,8,.35)", backdropFilter: "none" }} onMouseDown={onClose} />
      <aside className="drawer" role="dialog" aria-modal="true">
        <div className="drawer-head">
          <div className="grow">
            <div style={{ fontSize: "var(--fs-lg)", fontWeight: 600 }}>{title}</div>
            {sub && <div className="faint" style={{ fontSize: "var(--fs-sm)" }}>{sub}</div>}
          </div>
          {actions}
          <button className="btn ghost icon" onClick={onClose} aria-label="Close">
            <Icon name="x" />
          </button>
        </div>
        <div className="drawer-body">{children}</div>
      </aside>
    </>
  );
}

export function Tabs<T extends string>({ tabs, value, onChange }: { tabs: { id: T; label: string; icon?: string; badge?: React.ReactNode }[]; value: T; onChange: (t: T) => void }) {
  return (
    <div className="tabs" role="tablist">
      {tabs.map((t) => (
        <button key={t.id} role="tab" aria-selected={t.id === value} className={"tab" + (t.id === value ? " on" : "")} onClick={() => onChange(t.id)}>
          {t.icon && <Icon name={t.icon} size={15} />}
          {t.label}
          {t.badge}
        </button>
      ))}
    </div>
  );
}

export function Seg<T extends string>({ options, value, onChange }: { options: { id: T; label: string }[]; value: T; onChange: (v: T) => void }) {
  return (
    <div className="seg">
      {options.map((o) => (
        <button key={o.id} className={o.id === value ? "on" : ""} onClick={() => onChange(o.id)}>
          {o.label}
        </button>
      ))}
    </div>
  );
}

export function Switch({ on, onChange, label, disabled }: { on: boolean; onChange: (v: boolean) => void; label?: React.ReactNode; disabled?: boolean }) {
  return (
    <label className="check" style={{ opacity: disabled ? 0.5 : 1 }}>
      <span className={"switch" + (on ? " on" : "")} role="switch" aria-checked={on} tabIndex={0} onClick={() => !disabled && onChange(!on)} onKeyDown={(e) => (e.key === " " || e.key === "Enter") && !disabled && onChange(!on)} />
      {label}
    </label>
  );
}

export function Bar({ value, cls, thick }: { value: number | null | undefined; cls?: string; thick?: boolean }) {
  const v = value ?? 0;
  return (
    <div className={"bar " + (cls ?? "") + (thick ? " thick" : "")}>
      <i style={{ width: `${Math.max(0, Math.min(100, v))}%` }} />
    </div>
  );
}

export function StatusDot({ status, live }: { status: string; live?: boolean }) {
  return <span className={"dot " + status + (live ? " live" : "")} />;
}

export function Empty({ icon = "info", title, children, action }: { icon?: string; title: string; children?: React.ReactNode; action?: React.ReactNode }) {
  return (
    <div className="empty">
      <Icon name={icon} size={28} stroke={1.3} />
      <h3>{title}</h3>
      {children && <p>{children}</p>}
      {action && <div style={{ marginTop: 16 }}>{action}</div>}
    </div>
  );
}

export function Skeleton({ h = 14, w = "100%", style }: { h?: number; w?: number | string; style?: React.CSSProperties }) {
  return <div className="skel" style={{ height: h, width: w, ...style }} />;
}

export function SkeletonRows({ n = 6 }: { n?: number }) {
  return (
    <div className="col" style={{ gap: 10, padding: 16 }}>
      {Array.from({ length: n }).map((_, i) => (
        <Skeleton key={i} h={14} w={`${70 + ((i * 37) % 30)}%`} />
      ))}
    </div>
  );
}

export function Notice({ kind = "", icon, children }: { kind?: string; icon?: string; children: React.ReactNode }) {
  return (
    <div className={"notice " + kind}>
      <Icon name={icon ?? (kind === "crit" || kind === "warn" ? "alert" : "info")} size={15} />
      <div className="grow">{children}</div>
    </div>
  );
}

export function ErrorNote({ error }: { error: string | null | undefined }) {
  if (!error) return null;
  return <Notice kind="crit">{error}</Notice>;
}

export function RiskChip({ risk }: { risk: Risk | string }) {
  return <span className={"chip sq " + riskClass(risk)}>{risk}</span>;
}

export function Tip({ text, children }: { text: React.ReactNode; children: React.ReactElement }) {
  const [pos, setPos] = useState<{ x: number; y: number } | null>(null);
  return (
    <>
      {React.cloneElement(children, {
        onMouseEnter: (e: React.MouseEvent) => setPos({ x: e.clientX, y: e.clientY }),
        onMouseMove: (e: React.MouseEvent) => setPos({ x: e.clientX, y: e.clientY }),
        onMouseLeave: () => setPos(null),
      })}
      {pos && (
        <div className="tip" style={{ left: Math.min(pos.x + 12, window.innerWidth - 330), top: pos.y + 16 }}>
          {text}
        </div>
      )}
    </>
  );
}

export function KV({ rows }: { rows: [React.ReactNode, React.ReactNode][] }) {
  return (
    <div style={{ display: "grid", gridTemplateColumns: "minmax(120px, max-content) 1fr", columnGap: 18, rowGap: 8, fontSize: "var(--fs-sm)" }}>
      {rows.map(([k, v], i) => (
        <React.Fragment key={i}>
          <div className="faint">{k}</div>
          <div style={{ minWidth: 0, overflowWrap: "anywhere" }}>{v ?? <span className="faint">—</span>}</div>
        </React.Fragment>
      ))}
    </div>
  );
}

export function JSONView({ value, max = 400 }: { value: unknown; max?: number }) {
  const text = useMemo(() => {
    try {
      return JSON.stringify(value, null, 2);
    } catch {
      return String(value);
    }
  }, [value]);
  return (
    <pre className="code" style={{ maxHeight: max }}>
      {text}
    </pre>
  );
}

export function Copy({ text }: { text: string }) {
  const [ok, setOk] = useState(false);
  return (
    <button
      className="btn ghost sm icon"
      title="Copy"
      onClick={() => {
        navigator.clipboard?.writeText(text).then(() => {
          setOk(true);
          setTimeout(() => setOk(false), 1200);
        });
      }}
    >
      <Icon name={ok ? "check" : "copy"} size={13} />
    </button>
  );
}

export function useSort<T>(rows: T[], initial: { key: keyof T | string; dir: 1 | -1 }, get?: (r: T, k: string) => any) {
  const [s, setS] = useState(initial);
  const sorted = useMemo(() => {
    const out = [...rows];
    out.sort((a: any, b: any) => {
      const av = get ? get(a, s.key as string) : a[s.key];
      const bv = get ? get(b, s.key as string) : b[s.key];
      if (av === bv) return 0;
      if (av === undefined || av === null) return 1;
      if (bv === undefined || bv === null) return -1;
      return (av > bv ? 1 : -1) * s.dir;
    });
    return out;
  }, [rows, s, get]);
  const th = (key: string, label: React.ReactNode, cls = "") => (
    <th className={"sortable " + cls} onClick={() => setS((p) => ({ key, dir: p.key === key ? ((-p.dir) as 1 | -1) : -1 }))}>
      {label}
      {s.key === key && <span style={{ marginLeft: 4 }}>{s.dir === 1 ? "▲" : "▼"}</span>}
    </th>
  );
  return { sorted, th };
}

// ---------------------------------------------------------------- action dialog

/**
 * ActionDialog previews an operation through the same policy and impact
 * analysis the AI uses, then executes it as the operator and shows the
 * verified outcome. High-risk operations require typing a confirmation.
 */
export function ActionDialog({ serverId, tool, args, title, onClose, onDone }: { serverId: string; tool: string; args: Record<string, unknown>; title: string; onClose: () => void; onDone?: (result: any) => void }) {
  const { toast } = useApp();
  const [preview, setPreview] = useState<{ risk: Risk; policy: { decision: string; reason: string }; impact?: Impact } | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [confirm, setConfirm] = useState("");
  const [result, setResult] = useState<any>(null);
  useEffect(() => {
    api.post<any>(`/api/servers/${serverId}/actions/preview`, { tool, args }).then(setPreview).catch((e) => setErr(e.message));
  }, [serverId, tool, JSON.stringify(args)]);
  const blocked = preview?.policy.decision === "deny";
  const needConfirm = preview?.risk === "high" ? preview.impact?.confirm : undefined;
  const run = async () => {
    setBusy(true);
    setErr(null);
    try {
      const r = await api.post<any>(`/api/servers/${serverId}/actions`, { tool, args, confirmed: true, confirm });
      setResult(r);
      const v = r.result?.verification as string | undefined;
      toast({ title: `${title}: ${r.status}`, body: v ?? r.result?.error, kind: r.status === "succeeded" && (!v || v.startsWith("verified")) ? "ok" : "warn" });
      onDone?.(r);
    } catch (e: any) {
      setErr(e.message);
    } finally {
      setBusy(false);
    }
  };
  const imp = preview?.impact;
  return (
    <Modal
      title={title}
      sub={preview ? <span className="row">Risk <RiskChip risk={preview.risk} /> <span className="faint">· {preview.policy.reason}</span></span> : "Analysing impact…"}
      onClose={onClose}
      footer={
        result ? (
          <button className="btn primary" onClick={onClose}>Done</button>
        ) : (
          <>
            <button className="btn ghost" onClick={onClose}>Cancel</button>
            <button className={"btn " + (preview?.risk === "high" ? "danger" : "primary")} disabled={!preview || blocked || busy || (!!needConfirm && confirm !== needConfirm)} onClick={run}>
              {busy ? <Icon name="refresh" className="spin" /> : <Icon name="check" />}
              {blocked ? "Blocked by policy" : "Execute"}
            </button>
          </>
        )
      }
    >
      <ErrorNote error={err} />
      {!preview && !err && <SkeletonRows n={4} />}
      {imp && !result && (
        <div className="col" style={{ gap: 14 }}>
          <div style={{ fontSize: "var(--fs-lg)" }}>{imp.summary}</div>
          {imp.effects && imp.effects.length > 0 && (
            <div>
              <div className="label" style={{ marginBottom: 6 }}>What will happen</div>
              <ul style={{ margin: 0, paddingLeft: 18 }} className="dim">
                {imp.effects.map((e, i) => <li key={i} className="mono" style={{ fontSize: 12 }}>{e}</li>)}
              </ul>
            </div>
          )}
          {imp.risks && imp.risks.length > 0 && (
            <div>
              <div className="label" style={{ marginBottom: 6 }}>Risks</div>
              <div className="col" style={{ gap: 6 }}>
                {imp.risks.map((r, i) => <Notice key={i} kind="warn">{r}</Notice>)}
              </div>
            </div>
          )}
          {imp.rollback && (
            <div>
              <div className="label" style={{ marginBottom: 6 }}>Rollback</div>
              <div className="dim">{imp.rollback}</div>
            </div>
          )}
          {imp.evidence !== undefined && imp.evidence !== null && (
            <details>
              <summary className="faint" style={{ cursor: "pointer" }}>Current state used for this preview</summary>
              <JSONView value={imp.evidence} max={220} />
            </details>
          )}
          {needConfirm && (
            <label className="field">
              <span>Type <b className="mono">{needConfirm}</b> to confirm</span>
              <input className="input mono" value={confirm} onChange={(e) => setConfirm(e.target.value)} autoFocus />
            </label>
          )}
        </div>
      )}
      {result && <ActionResultView r={result} />}
    </Modal>
  );
}

export function ActionResultView({ r }: { r: any }) {
  const res = r.result ?? {};
  const v: string | undefined = res.verification;
  const kind = r.status !== "succeeded" ? "crit" : v?.startsWith("verified") ? "" : v ? "warn" : "";
  return (
    <div className="col" style={{ gap: 12 }}>
      <Notice kind={kind} icon={kind === "" ? "check" : undefined}>
        <b>{r.status === "succeeded" ? "Executed" : "Failed"}</b> — {v ?? res.error ?? res.message ?? "completed"}
      </Notice>
      {res.state_after && <KV rows={Object.entries(res.state_after).map(([k, val]) => [k, typeof val === "object" ? JSON.stringify(val) : String(val)])} />}
      {res.stderr && <pre className="code">{res.stderr}</pre>}
      <details>
        <summary className="faint" style={{ cursor: "pointer" }}>Full result · execution {r.execution_id}</summary>
        <JSONView value={res} />
      </details>
    </div>
  );
}

export function Toasts() {
  const { toasts, dismissToast } = useApp();
  return (
    <div className="toasts" aria-live="polite">
      {toasts.map((t) => (
        <div key={t.id} className={"toast " + (t.kind ?? "")} onClick={() => dismissToast(t.id)}>
          <b>{t.title}</b>
          {t.body && <div className="dim" style={{ maxHeight: 80, overflow: "hidden" }}>{t.body}</div>}
        </div>
      ))}
    </div>
  );
}
