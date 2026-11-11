import { useEffect, useRef, useState } from "react";
import { api } from "../lib/api";
import { navigate, useApp } from "../lib/state";
import { Icon } from "../components/Icon";
import { ErrorNote, Notice, Seg } from "../components/UI";
import type { Server } from "../lib/types";

type Scan = { host: string; port: number; fingerprint: string; key_type: string; known_hosts: string; known_hosts_file?: string };
type Step = { id: string; label: string; status: string; detail?: string };
type Job = { id: string; state: string; error?: string; hint?: string; server_id?: string; steps: Step[]; facts: Record<string, string>; can_retry_enroll?: boolean };

/**
 * Installs (or upgrades) the agent over SSH. Credentials stay in this form
 * and the single request that starts the job; NEXUS does not store them.
 */
export function SSHDeploy({ mode, server, onDone, onClose }: { mode: "install" | "upgrade"; server?: Server; onDone?: (j: Job) => void; onClose: () => void }) {
  const { on, reloadServers, toast } = useApp();
  const [f, setF] = useState({
    host: server?.address ?? "",
    ssh_port: 22,
    user: "",
    auth: "password" as "password" | "key",
    password: "",
    private_key: "",
    passphrase: "",
    sudo_password: "",
    agent_port: server?.port ?? 7443,
    docker: false,
    name: "",
    environment: "production",
    group: "",
    tags: "",
  });
  const [scan, setScan] = useState<Scan | null>(null);
  const [verified, setVerified] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [job, setJob] = useState<Job | null>(null);
  const [agents, setAgents] = useState<Record<string, string> | null>(null);
  const fileRef = useRef<HTMLInputElement>(null);
  const set = (k: string, v: any) => {
    setF((p) => ({ ...p, [k]: v }));
    if (k === "host" || k === "ssh_port") {
      setScan(null);
      setVerified(false);
    }
  };

  useEffect(() => {
    api.get<Record<string, string>>("/api/deploy/agents").then(setAgents).catch(() => setAgents({}));
  }, []);
  useEffect(() => {
    if (!job) return;
    const off = on((e) => {
      if (e.type === "deploy" && e.data.id === job.id) setJob(e.data);
    });
    const t = setInterval(() => api.get<Job>(`/api/deploy/${job.id}`).then(setJob).catch(() => {}), 2500);
    return () => {
      off();
      clearInterval(t);
    };
  }, [job?.id, on]);
  useEffect(() => {
    if (job && job.state !== "running") {
      clearSecrets();
      if (job.state === "succeeded") {
        reloadServers();
        toast({ title: mode === "install" ? "Server added over SSH" : "Agent upgraded", body: job.facts.hostname, kind: "ok" });
        onDone?.(job);
      }
    }
  }, [job?.state]);

  const clearSecrets = () => setF((p) => ({ ...p, password: "", private_key: "", passphrase: "", sudo_password: "" }));

  const doScan = async () => {
    setBusy(true);
    setErr(null);
    setVerified(false);
    try {
      const s = await api.post<Scan>("/api/deploy/scan", { host: f.host.trim(), port: Number(f.ssh_port) });
      setScan(s);
      if (s.known_hosts === "match") setVerified(true);
    } catch (e: any) {
      setErr(e.message);
    } finally {
      setBusy(false);
    }
  };
  const start = async () => {
    setBusy(true);
    setErr(null);
    try {
      const j = await api.post<Job>("/api/deploy", {
        mode,
        server_id: server?.id,
        host: f.host.trim(),
        ssh_port: Number(f.ssh_port),
        user: f.user.trim(),
        password: f.auth === "password" ? f.password : "",
        private_key: f.auth === "key" ? f.private_key : "",
        passphrase: f.auth === "key" ? f.passphrase : "",
        sudo_password: f.sudo_password,
        host_key: scan!.fingerprint,
        agent_port: Number(f.agent_port),
        docker: f.docker,
        name: f.name.trim(),
        environment: f.environment,
        group: f.group.trim(),
        tags: f.tags.split(/[,\s]+/).filter(Boolean),
      });
      setJob(j);
    } catch (e: any) {
      setErr(e.message);
    } finally {
      setBusy(false);
    }
  };
  const loadKey = (file: File) => {
    const r = new FileReader();
    r.onload = () => set("private_key", String(r.result ?? ""));
    r.readAsText(file);
  };

  if (job) return <Progress job={job} mode={mode} onClose={onClose} onRetry={() => setJob(null)} onRetryEnroll={() => api.post<Job>(`/api/deploy/${job.id}/retry-enroll`).then(setJob).catch((e) => toast({ title: "Retry failed", body: e.message, kind: "crit" }))} />;

  const credsOK = f.user.trim() && (f.auth === "password" ? f.password : f.private_key.includes("PRIVATE KEY"));
  const noAgents = agents !== null && Object.keys(agents).length === 0;
  return (
    <div className="col" style={{ gap: 14 }}>
      {noAgents && <Notice kind="warn">This build of NEXUS does not include the Linux agent binaries, so it cannot install over SSH. Use the release build, or put <span className="mono">linux-amd64/nexus-agent</span> next to NEXUS.exe.</Notice>}
      <ErrorNote error={err} />
      <div className="grid" style={{ gridTemplateColumns: "1fr 90px", gap: 10 }}>
        <label className="field"><span>Server address</span><input className="input mono" placeholder="203.0.113.10 or server.example.com" value={f.host} onChange={(e) => set("host", e.target.value)} disabled={mode === "upgrade"} autoFocus={mode === "install"} /></label>
        <label className="field"><span>SSH port</span><input className="input mono" type="number" value={f.ssh_port} onChange={(e) => set("ssh_port", e.target.value)} /></label>
      </div>

      {!scan ? (
        <div className="row">
          <button className="btn" onClick={doScan} disabled={busy || !f.host.trim()}>{busy ? <Icon name="refresh" className="spin" /> : <Icon name="key" />} Check server identity</button>
          <span className="faint" style={{ fontSize: 12 }}>Reads the SSH host key only. No credentials are sent.</span>
        </div>
      ) : (
        <div style={{ padding: 12, borderRadius: "var(--radius)", background: scan.known_hosts === "mismatch" ? "var(--crit-soft)" : verified ? "var(--ok-soft)" : "var(--surface-2)", border: "1px solid var(--line)" }}>
          <div className="row" style={{ marginBottom: 6 }}>
            <Icon name={scan.known_hosts === "mismatch" ? "alert" : "lock"} />
            <b className="grow">SSH host key · {scan.key_type}</b>
            <button className="btn ghost sm" onClick={doScan}>Re-check</button>
          </div>
          <div className="mono" style={{ fontSize: 13, wordBreak: "break-all" }}>{scan.fingerprint}</div>
          {scan.known_hosts === "match" && <div className="ok-t" style={{ fontSize: 12, marginTop: 6 }}>✓ Matches the key in your known_hosts file ({scan.known_hosts_file}).</div>}
          {scan.known_hosts === "mismatch" && <div className="crit-t" style={{ fontSize: 12, marginTop: 6 }}>Your known_hosts file has a different key for this server. This can mean the server was reinstalled — or that someone is intercepting the connection. Do not continue unless you know why.</div>}
          {scan.known_hosts !== "match" && (
            <>
              <div className="dim" style={{ fontSize: 12, marginTop: 6 }}>
                Compare it with the server's real key, for example from your hosting provider's console:&nbsp;
                <span className="mono">ssh-keygen -lf /etc/ssh/ssh_host_{scan.key_type.replace("ssh-", "").replace("ecdsa-sha2-nistp256", "ecdsa")}_key.pub</span>
              </div>
              <label className="check" style={{ marginTop: 8 }}>
                <input type="checkbox" checked={verified} onChange={(e) => setVerified(e.target.checked)} />
                <span>I have verified this fingerprint belongs to my server</span>
              </label>
            </>
          )}
        </div>
      )}

      <div className="grid g2" style={{ gap: 10, opacity: scan ? 1 : 0.5 }}>
        <label className="field"><span>SSH user</span><input className="input mono" placeholder="root, ubuntu, admin…" value={f.user} onChange={(e) => set("user", e.target.value)} disabled={!scan} /></label>
        <div className="field"><span>Sign in with</span><Seg options={[{ id: "password", label: "Password" }, { id: "key", label: "Private key" }]} value={f.auth} onChange={(v) => set("auth", v)} /></div>
      </div>
      {f.auth === "password" ? (
        <label className="field"><span>SSH password</span><input className="input" type="password" autoComplete="off" value={f.password} onChange={(e) => set("password", e.target.value)} disabled={!scan} /></label>
      ) : (
        <div className="col" style={{ gap: 8 }}>
          <label className="field">
            <span className="row">Private key <span className="spacer" /><button type="button" className="btn ghost sm" onClick={() => fileRef.current?.click()} disabled={!scan}><Icon name="download" size={12} /> Load key file…</button></span>
            <textarea className="input mono" rows={4} placeholder="-----BEGIN OPENSSH PRIVATE KEY-----" value={f.private_key} onChange={(e) => set("private_key", e.target.value)} disabled={!scan} style={{ fontSize: 11 }} />
          </label>
          <input ref={fileRef} type="file" style={{ display: "none" }} onChange={(e) => e.target.files?.[0] && loadKey(e.target.files[0])} />
          <label className="field"><span>Key passphrase (if the key is encrypted)</span><input className="input" type="password" autoComplete="off" value={f.passphrase} onChange={(e) => set("passphrase", e.target.value)} disabled={!scan} /></label>
        </div>
      )}
      <label className="field"><span>sudo password <span className="faint">(only if different from the SSH password, or when signing in with a key and sudo asks for a password)</span></span><input className="input" type="password" autoComplete="off" value={f.sudo_password} onChange={(e) => set("sudo_password", e.target.value)} disabled={!scan} /></label>

      {mode === "install" && (
        <details>
          <summary className="faint" style={{ cursor: "pointer" }}>Name, environment and agent options</summary>
          <div className="col" style={{ gap: 10, marginTop: 10 }}>
            <div className="grid g2" style={{ gap: 10 }}>
              <label className="field"><span>Display name</span><input className="input" placeholder="Defaults to the server's hostname" value={f.name} onChange={(e) => set("name", e.target.value)} /></label>
              <label className="field"><span>Environment</span><select className="select" value={f.environment} onChange={(e) => set("environment", e.target.value)}>{["production", "staging", "development", "lab"].map((x) => <option key={x}>{x}</option>)}</select></label>
              <label className="field"><span>Group</span><input className="input" value={f.group} onChange={(e) => set("group", e.target.value)} /></label>
              <label className="field"><span>Tags</span><input className="input" placeholder="comma separated" value={f.tags} onChange={(e) => set("tags", e.target.value)} /></label>
            </div>
            <div className="grid g2" style={{ gap: 10 }}>
              <label className="field"><span>Agent port</span><input className="input mono" type="number" value={f.agent_port} onChange={(e) => set("agent_port", e.target.value)} /></label>
              <label className="check" style={{ alignSelf: "end", paddingBottom: 6 }}><input type="checkbox" checked={f.docker} onChange={(e) => set("docker", e.target.checked)} /> Grant Docker access</label>
            </div>
            {f.docker && <Notice kind="warn">Docker access adds the agent's service user to the docker group, which is equivalent to root on that server.</Notice>}
          </div>
        </details>
      )}
      <Notice icon="lock">Your SSH login is used once for this installation and is not saved. NEXUS uploads its built-in agent, verifies its checksum, installs it as a least-privilege service, and then talks to it only through its pinned certificate.</Notice>
      <div className="row" style={{ justifyContent: "flex-end" }}>
        <button className="btn ghost" onClick={onClose}>Cancel</button>
        <button className="btn primary" disabled={busy || !scan || !verified || !credsOK || noAgents || scan.known_hosts === "mismatch"} onClick={start}>
          <Icon name="bolt" /> {mode === "install" ? "Install agent & add server" : "Upgrade agent"}
        </button>
      </div>
    </div>
  );
}

function Progress({ job, mode, onClose, onRetry, onRetryEnroll }: { job: Job; mode: string; onClose: () => void; onRetry: () => void; onRetryEnroll: () => void }) {
  const icon = (s: string) =>
    s === "done" ? <Icon name="check" className="ok-t" /> : s === "failed" ? <Icon name="x" className="crit-t" /> : s === "warning" ? <Icon name="alert" className="warn-t" /> : s === "running" ? <Icon name="refresh" className="spin accent-t" /> : <span style={{ width: 16, height: 16, display: "inline-grid", placeItems: "center" }}><span className="dot" /></span>;
  return (
    <div className="col" style={{ gap: 14 }}>
      <div className="col" style={{ gap: 2 }}>
        {job.steps.map((s) => (
          <div key={s.id} className="row" style={{ alignItems: "flex-start", padding: "8px 0", borderBottom: "1px solid var(--line)", opacity: s.status === "pending" || s.status === "skipped" ? 0.5 : 1 }}>
            <span style={{ width: 20, marginTop: 1 }}>{icon(s.status)}</span>
            <div className="grow">
              <div style={{ fontWeight: 500 }}>{s.label}</div>
              {s.detail && <div className={"mono " + (s.status === "failed" ? "crit-t" : s.status === "warning" ? "warn-t" : "faint")} style={{ fontSize: 11.5, whiteSpace: "pre-wrap", marginTop: 2 }}>{s.detail}</div>}
            </div>
          </div>
        ))}
      </div>
      {job.state === "failed" && (
        <Notice kind="crit">
          <b>{job.error}</b>
          {job.hint && <div style={{ marginTop: 4 }}>{job.hint}</div>}
        </Notice>
      )}
      {job.state === "succeeded" && <Notice icon="check">{mode === "install" ? "The server is enrolled. Telemetry starts within a few seconds." : "The agent was upgraded and answered with its pinned identity."}</Notice>}
      <div className="row" style={{ justifyContent: "flex-end" }}>
        {job.state === "failed" && !job.can_retry_enroll && <button className="btn" onClick={onRetry}>Back</button>}
        {job.state === "failed" && job.can_retry_enroll && <button className="btn primary" onClick={onRetryEnroll}><Icon name="refresh" /> Retry enrollment</button>}
        {job.state === "succeeded" && job.server_id ? (
          <button className="btn primary" onClick={() => { onClose(); navigate("/server/" + job.server_id); }}>Open server <Icon name="chevron" size={12} /></button>
        ) : (
          <button className="btn ghost" onClick={onClose} disabled={job.state === "running"}>{job.state === "running" ? "Working…" : "Close"}</button>
        )}
      </div>
    </div>
  );
}
