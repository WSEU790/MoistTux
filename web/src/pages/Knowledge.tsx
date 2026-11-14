import { useEffect, useMemo, useState } from "react";
import { navigate, useFetch } from "../lib/state";
import { Empty, Panel, RiskChip, Seg, SkeletonRows } from "../components/UI";
import { Icon } from "../components/Icon";
import { Markdown } from "../components/Markdown";

type DocInfo = { id: string; title: string; summary: string; tags: string[] };
type Doc = DocInfo & { sections: { heading: string; body: string }[] };
type Hit = { doc: string; title: string; section: string; snippet: string; score: number };
type Cmd = { id: string; category: string; description: string; argv: string[]; params?: { name: string; kind: string; description: string; optional?: boolean; multi?: boolean }[]; root: boolean; risk: string; warning?: string };

/** The built-in Linux runbooks the AI consults, and the command catalog it can run. */
export function Knowledge({ id }: { id?: string }) {
  const [view, setView] = useState<"runbooks" | "commands">("runbooks");
  return (
    <div className="page">
      <div className="page-head">
        <div className="grow">
          <h1>Knowledge</h1>
          <p>The Linux server runbooks the AI searches before procedures and changes, and the administration commands it can run on your servers.</p>
        </div>
        <Seg options={[{ id: "runbooks", label: "Runbooks" }, { id: "commands", label: "Command catalog" }]} value={view} onChange={(v) => { setView(v); if (id) navigate("/knowledge"); }} />
      </div>
      {view === "commands" ? <Commands /> : id ? <Runbook id={id} /> : <Runbooks />}
    </div>
  );
}

function Runbooks() {
  const docs = useFetch<{ docs: DocInfo[] }>("/api/knowledge");
  const [q, setQ] = useState("");
  const [hits, setHits] = useState<Hit[] | null>(null);
  useEffect(() => {
    const t = q.trim();
    if (!t) {
      setHits(null);
      return;
    }
    const h = setTimeout(() => {
      fetch("/api/knowledge?q=" + encodeURIComponent(t), { credentials: "same-origin" })
        .then((r) => r.json())
        .then((d) => setHits(d.results ?? []))
        .catch(() => setHits([]));
    }, 200);
    return () => clearTimeout(h);
  }, [q]);
  return (
    <div className="col" style={{ gap: 14 }}>
      <div className="row" style={{ maxWidth: 640 }}>
        <Icon name="search" />
        <input className="input grow" placeholder="Search the runbooks, e.g. “nginx 502” or “disk full”" value={q} onChange={(e) => setQ(e.target.value)} autoFocus />
      </div>
      {hits ? (
        <Panel title={`${hits.length} matching section${hits.length === 1 ? "" : "s"}`}>
          {hits.length === 0 && <Empty icon="search" title="No matches">Try other words; the search matches terms in headings and text.</Empty>}
          <div className="col" style={{ gap: 12 }}>
            {hits.map((h, i) => (
              <a key={i} href={`#/knowledge/${h.doc}?s=${encodeURIComponent(h.section)}`} className="card" style={{ display: "block", padding: 12, textDecoration: "none", color: "inherit" }}>
                <div className="row" style={{ justifyContent: "space-between" }}>
                  <b>{h.title}</b>
                  <span className="faint" style={{ fontSize: 12 }}>{h.section}</span>
                </div>
                <div className="dim" style={{ fontSize: 13, marginTop: 6, whiteSpace: "pre-wrap" }}>{h.snippet}</div>
              </a>
            ))}
          </div>
        </Panel>
      ) : !docs.data ? (
        <Panel><SkeletonRows /></Panel>
      ) : (
        <div className="grid g3" style={{ gap: 12 }}>
          {docs.data.docs.map((d) => (
            <a key={d.id} href={`#/knowledge/${d.id}`} className="card" style={{ display: "block", padding: 14, textDecoration: "none", color: "inherit" }}>
              <b>{d.title}</b>
              <div className="dim" style={{ fontSize: 13, margin: "6px 0 8px" }}>{d.summary}</div>
              <div className="row wrap" style={{ gap: 4 }}>{d.tags.slice(0, 5).map((t) => <span key={t} className="chip">{t}</span>)}</div>
            </a>
          ))}
        </div>
      )}
    </div>
  );
}

function Runbook({ id }: { id: string }) {
  const doc = useFetch<Doc>(`/api/knowledge/${encodeURIComponent(id)}`, [id]);
  useEffect(() => {
    // Scroll to the section given as ?s= (links from search results).
    const anchor = new URLSearchParams(window.location.hash.split("?")[1] ?? "").get("s") ?? "";
    if (!anchor || !doc.data) return;
    document.getElementById("sec-" + anchor)?.scrollIntoView({ behavior: "smooth", block: "start" });
  }, [doc.data]);
  if (doc.error) return <Panel><Empty icon="info" title="Runbook not found">{String(doc.error)}</Empty></Panel>;
  if (!doc.data) return <Panel><SkeletonRows /></Panel>;
  const d = doc.data;
  return (
    <div className="col" style={{ gap: 14, maxWidth: 920 }}>
      <a href="#/knowledge" className="faint" style={{ fontSize: 13 }}>← All runbooks</a>
      <Panel title={d.title}>
        <p className="dim" style={{ marginTop: 0 }}>{d.summary}</p>
        <div className="row wrap" style={{ gap: 4, marginBottom: 12 }}>{d.tags.map((t) => <span key={t} className="chip">{t}</span>)}</div>
        {d.sections.map((s) => (
          <section key={s.heading} id={"sec-" + s.heading} style={{ marginBottom: 18 }}>
            <h3 style={{ margin: "14px 0 8px" }}>{s.heading}</h3>
            <Markdown text={s.body} />
          </section>
        ))}
      </Panel>
    </div>
  );
}

function Commands() {
  const cmds = useFetch<Cmd[]>("/api/admin-commands");
  const [cat, setCat] = useState("");
  const [q, setQ] = useState("");
  const cats = useMemo(() => Array.from(new Set((cmds.data ?? []).map((c) => c.category))), [cmds.data]);
  const list = (cmds.data ?? []).filter((c) => (!cat || c.category === cat) && (!q || (c.id + " " + c.description).toLowerCase().includes(q.toLowerCase())));
  if (!cmds.data) return <Panel><SkeletonRows /></Panel>;
  return (
    <div className="col" style={{ gap: 14 }}>
      <div className="row wrap" style={{ gap: 8 }}>
        <input className="input" style={{ maxWidth: 320 }} placeholder="Filter commands" value={q} onChange={(e) => setQ(e.target.value)} />
        <select className="select" value={cat} onChange={(e) => setCat(e.target.value)}>
          <option value="">All categories ({cmds.data.length})</option>
          {cats.map((c) => <option key={c} value={c}>{c}</option>)}
        </select>
      </div>
      <Panel title={`${list.length} commands`}>
        <p className="dim" style={{ marginTop: 0, fontSize: 13 }}>
          The AI runs these with <span className="mono">run_admin_command</span>. Arguments are validated and never pass through a shell. Read-only commands run immediately; changes follow your automation policy, and high-risk ones always need your approval. Anything else needs a shell command that you approve individually.
        </p>
        <table className="table">
          <thead><tr><th>Command</th><th>Risk</th><th>Runs</th><th>What it does</th></tr></thead>
          <tbody>
            {list.map((c) => (
              <tr key={c.id}>
                <td className="mono" style={{ whiteSpace: "nowrap" }}>{c.id}</td>
                <td><RiskChip risk={c.risk} /></td>
                <td className="faint" style={{ whiteSpace: "nowrap" }}>{c.root ? "as root" : "unprivileged"}</td>
                <td>
                  {c.description}
                  <div className="mono faint" style={{ fontSize: 11.5, marginTop: 4 }}>{c.argv.join(" ")}</div>
                  {c.warning && <div style={{ fontSize: 12, marginTop: 4, color: "var(--warn)" }}>{c.warning}</div>}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </Panel>
    </div>
  );
}
