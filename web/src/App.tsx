import React, { useEffect, useMemo, useRef, useState } from "react";
import { Icon, Logo } from "./components/Icon";
import { Toasts } from "./components/UI";
import { navigate, useApp, useRoute } from "./lib/state";
import { api } from "./lib/api";
import { ago, sevClass } from "./lib/format";
import type { Notification } from "./lib/types";
import { CommandCenter } from "./pages/CommandCenter";
import { Fleet } from "./pages/Fleet";
import { ServerWorkspace } from "./pages/Server";
import { AICenter } from "./pages/AICenter";
import { Incidents } from "./pages/Incidents";
import { Approvals } from "./pages/Approvals";
import { Topology } from "./pages/Topology";
import { Activity } from "./pages/Activity";
import { MemoryPage } from "./pages/Memory";
import { Policies } from "./pages/Policies";
import { Settings, restartToUpdate, useUpdateStatus } from "./pages/Settings";

type NavItem = { id: string; label: string; icon: string; badge?: () => { n: number; cls?: string } | null };

export function App() {
  const route = useRoute();
  const app = useApp();
  const { overview, pendingApprovals, appearance, setAppearance } = app;
  const [palette, setPalette] = useState(false);

  useEffect(() => {
    const k = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setPalette((p) => !p);
      }
    };
    window.addEventListener("keydown", k);
    return () => window.removeEventListener("keydown", k);
  }, []);

  const sections: { title: string; items: NavItem[] }[] = [
    {
      title: "Operate",
      items: [
        { id: "", label: "Command Center", icon: "command" },
        { id: "fleet", label: "Fleet", icon: "fleet", badge: () => (overview?.servers?.offline ? { n: overview.servers.offline, cls: "crit" } : null) },
        { id: "topology", label: "Topology", icon: "topology" },
      ],
    },
    {
      title: "Intelligence",
      items: [
        { id: "ai", label: "AI Command Center", icon: "ai" },
        {
          id: "incidents",
          label: "Incidents & Alerts",
          icon: "alert",
          badge: () => {
            const c = overview?.alerts?.critical ?? 0;
            const w = overview?.alerts?.warning ?? 0;
            return c ? { n: c, cls: "crit" } : w ? { n: w, cls: "warn" } : null;
          },
        },
        { id: "approvals", label: "Approvals", icon: "approval", badge: () => (pendingApprovals ? { n: pendingApprovals, cls: "warn" } : null) },
      ],
    },
    {
      title: "Govern",
      items: [
        { id: "policies", label: "Automation Policies", icon: "policy" },
        { id: "memory", label: "AI Memory", icon: "memory" },
        { id: "activity", label: "Activity & Audit", icon: "audit" },
      ],
    },
  ];

  const top = route.parts[0] ?? "";
  let page: React.ReactNode;
  let title = "Command Center";
  switch (top) {
    case "":
      page = <CommandCenter />;
      break;
    case "fleet":
      page = <Fleet />;
      title = "Fleet";
      break;
    case "server":
      page = <ServerWorkspace id={route.parts[1]} tab={route.parts[2]} />;
      title = app.serverById(route.parts[1])?.name ?? "Server";
      break;
    case "ai":
      page = <AICenter convId={route.parts[1]} />;
      title = "AI Command Center";
      break;
    case "incidents":
      page = <Incidents id={route.parts[1]} />;
      title = "Incidents & Alerts";
      break;
    case "approvals":
      page = <Approvals />;
      title = "Approvals";
      break;
    case "topology":
      page = <Topology />;
      title = "Infrastructure Topology";
      break;
    case "activity":
      page = <Activity />;
      title = "Activity & Audit";
      break;
    case "memory":
      page = <MemoryPage />;
      title = "AI Memory";
      break;
    case "policies":
      page = <Policies scope={route.parts[1]} />;
      title = "Automation Policies";
      break;
    case "settings":
      page = <Settings section={route.parts[1]} />;
      title = "Settings";
      break;
    default:
      page = <CommandCenter />;
  }

  const collapsed = appearance.sidebar === "collapsed";
  return (
    <div className={"shell" + (collapsed ? " nav-collapsed" : "")}>
      <nav className="nav" aria-label="Primary">
        <div className="brand">
          <Logo />
          <div className="brand-text">
            <div className="brand-name">NEXUS</div>
            <div className="brand-sub">Command center</div>
          </div>
        </div>
        <div className="nav-scroll">
          {sections.map((s) => (
            <div key={s.title}>
              <div className="nav-section">{s.title}</div>
              {s.items.map((it) => {
                const b = it.badge?.();
                const active = top === it.id || (it.id === "fleet" && top === "server");
                return (
                  <a key={it.id} href={"#/" + it.id} className={"nav-item" + (active ? " active" : "")} title={collapsed ? it.label : undefined}>
                    <Icon name={it.icon} size={17} />
                    <span className="nav-label">{it.label}</span>
                    {b && <span className={"nav-badge " + (b.cls ?? "")}>{b.n}</span>}
                  </a>
                );
              })}
            </div>
          ))}
        </div>
        <div className="nav-foot">
          <a href="#/settings" className={"nav-item" + (top === "settings" ? " active" : "")}>
            <Icon name="settings" size={17} />
            <span className="nav-label">Settings</span>
          </a>
          <button className="nav-item" onClick={() => setAppearance({ sidebar: collapsed ? "expanded" : "collapsed" })}>
            <Icon name="sidebar" size={17} />
            <span className="nav-label">Collapse</span>
          </button>
        </div>
      </nav>
      <header className="top">
        <div className="top-title">{title}</div>
        <div className="spacer" />
        <FleetPulse />
        <UpdateChip />
        <AIPill />
        <button className="btn ghost sm" onClick={() => setPalette(true)} title="Command palette">
          <Icon name="search" size={14} /> <span className="faint">Jump to…</span> <kbd>Ctrl K</kbd>
        </button>
        <ThemeToggle />
        <NotificationBell />
      </header>
      <main className="main" id="main">
        {page}
      </main>
      {palette && <Palette onClose={() => setPalette(false)} />}
      <Toasts />
      {!app.connected && app.serversLoaded && (
        <div style={{ position: "fixed", bottom: 16, left: "50%", transform: "translateX(-50%)", zIndex: 60 }} className="chip warn">
          <Icon name="refresh" size={12} className="spin" /> Reconnecting to the NEXUS core…
        </div>
      )}
    </div>
  );
}

function FleetPulse() {
  const { overview } = useApp();
  if (!overview) return null;
  const s = overview.servers ?? {};
  return (
    <a href="#/fleet" className="row" style={{ gap: 12, fontSize: "var(--fs-xs)", color: "var(--text-2)", textDecoration: "none" }}>
      <span className="row" style={{ gap: 6 }}>
        <span className="dot ok live" /> {s.online ?? 0} online
      </span>
      {(s.degraded ?? 0) > 0 && (
        <span className="row" style={{ gap: 6 }}>
          <span className="dot warn" /> {s.degraded} degraded
        </span>
      )}
      {(s.offline ?? 0) + (s.untrusted ?? 0) > 0 && (
        <span className="row" style={{ gap: 6 }}>
          <span className="dot crit" /> {(s.offline ?? 0) + (s.untrusted ?? 0)} offline
        </span>
      )}
    </a>
  );
}

function UpdateChip() {
  const { toast } = useApp();
  const [s] = useUpdateStatus();
  if (!s || s.state !== "ready") return null;
  return (
    <button className="chip accent" style={{ border: 0, cursor: "pointer" }} title={`NEXUS ${s.latest} is downloaded and verified`} onClick={() => (s.can_install ? restartToUpdate(toast) : navigate("/settings/updates"))}>
      <Icon name="download" size={12} /> Update {s.latest} ready · Restart
    </button>
  );
}

function AIPill() {
  const { ai } = useApp();
  const general = ai?.roles?.find((r) => r.role === "general");
  return (
    <a href="#/settings/ai" className={"chip " + (ai?.reachable ? (general ? "accent" : "warn") : "crit")} title={ai?.error ?? ai?.url} style={{ textDecoration: "none" }}>
      <Icon name="ai" size={12} />
      {ai === null ? "AI status…" : !ai.reachable ? "Ollama offline" : general ? general.model : "No model installed"}
    </a>
  );
}

function ThemeToggle() {
  const { appearance, setAppearance } = useApp();
  const order = ["graphite", "midnight", "tactical", "light"] as const;
  const next = order[(order.indexOf(appearance.theme) + 1) % order.length];
  return (
    <button className="btn ghost icon" title={`Theme: ${appearance.theme} (switch to ${next})`} onClick={() => setAppearance({ theme: next })}>
      <Icon name={appearance.theme === "light" ? "sun" : "moon"} />
    </button>
  );
}

function NotificationBell() {
  const { unread, setUnread } = useApp();
  const [open, setOpen] = useState(false);
  const [items, setItems] = useState<Notification[] | null>(null);
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    api.get<any>("/api/notifications?limit=40").then((r) => setItems(r.items)).catch(() => setItems([]));
    const close = (e: MouseEvent) => ref.current && !ref.current.contains(e.target as Node) && setOpen(false);
    window.addEventListener("mousedown", close);
    return () => window.removeEventListener("mousedown", close);
  }, [open]);
  const go = (n: Notification) => {
    setOpen(false);
    if (n.kind === "approval") navigate("/approvals");
    else if (n.kind === "ai_report") navigate("/incidents/" + n.ref);
    else if (n.server_id) navigate("/server/" + n.server_id);
  };
  return (
    <div ref={ref} style={{ position: "relative" }}>
      <button
        className="btn ghost icon"
        aria-label="Notifications"
        onClick={() => {
          setOpen(!open);
          if (!open && "Notification" in window && Notification.permission === "default") Notification.requestPermission().catch(() => {});
        }}
      >
        <Icon name="bell" />
        {unread > 0 && <span style={{ position: "absolute", top: 4, right: 4, width: 8, height: 8, borderRadius: 4, background: "var(--crit)", boxShadow: "0 0 0 2px var(--chrome)" }} />}
      </button>
      {open && (
        <div className="popover" style={{ right: 0, top: 40, width: 400, maxHeight: 520, display: "flex", flexDirection: "column" }}>
          <div className="row" style={{ padding: "12px 14px", borderBottom: "1px solid var(--line)" }}>
            <b>Notifications</b>
            <span className="spacer" />
            <button
              className="btn ghost sm"
              onClick={() => {
                api.post("/api/notifications/read").then(() => {
                  setUnread(0);
                  setItems((x) => x?.map((n) => ({ ...n, read: true })) ?? null);
                });
              }}
            >
              Mark all read
            </button>
          </div>
          <div style={{ overflow: "auto" }}>
            {items === null && <div className="faint" style={{ padding: 16 }}>Loading…</div>}
            {items?.length === 0 && <div className="faint" style={{ padding: 24, textAlign: "center" }}>No notifications yet.</div>}
            {items?.map((n) => (
              <div key={n.id} onClick={() => go(n)} style={{ padding: "10px 14px", borderBottom: "1px solid var(--line)", cursor: "pointer", opacity: n.read ? 0.65 : 1 }} className="row">
                <span className={"dot " + sevClass(n.severity)} style={{ alignSelf: "flex-start", marginTop: 6 }} />
                <div className="grow">
                  <div style={{ fontWeight: 500 }}>{n.title}</div>
                  <div className="faint" style={{ fontSize: "var(--fs-xs)", maxHeight: 36, overflow: "hidden" }}>{n.body}</div>
                </div>
                <span className="faint nowrap" style={{ fontSize: "var(--fs-micro)", alignSelf: "flex-start" }}>{ago(n.time)}</span>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

function Palette({ onClose }: { onClose: () => void }) {
  const { servers } = useApp();
  const [q, setQ] = useState("");
  const [sel, setSel] = useState(0);
  const items = useMemo(() => {
    const base: { label: string; sub: string; icon: string; go: () => void }[] = [
      { label: "Command Center", sub: "Overview", icon: "command", go: () => navigate("/") },
      { label: "Fleet", sub: "All servers", icon: "fleet", go: () => navigate("/fleet") },
      { label: "Add server", sub: "Enroll a new agent", icon: "plus", go: () => navigate("/fleet?add=1") },
      { label: "AI Command Center", sub: "Talk to your infrastructure", icon: "ai", go: () => navigate("/ai") },
      { label: "Incidents & Alerts", sub: "Monitoring", icon: "alert", go: () => navigate("/incidents") },
      { label: "Approvals", sub: "Pending operations", icon: "approval", go: () => navigate("/approvals") },
      { label: "Topology", sub: "Infrastructure map", icon: "topology", go: () => navigate("/topology") },
      { label: "Automation Policies", sub: "What the AI may do", icon: "policy", go: () => navigate("/policies") },
      { label: "AI Memory", sub: "Operational knowledge", icon: "memory", go: () => navigate("/memory") },
      { label: "Activity & Audit", sub: "Every action, hash-chained", icon: "audit", go: () => navigate("/activity") },
      { label: "Settings · AI models", sub: "Ollama and model roles", icon: "ai", go: () => navigate("/settings/ai") },
      { label: "Settings · Appearance", sub: "Themes and density", icon: "settings", go: () => navigate("/settings/appearance") },
      { label: "Settings · Monitoring", sub: "Thresholds and intervals", icon: "activity", go: () => navigate("/settings/monitoring") },
    ];
    for (const s of servers) {
      base.push({ label: s.name, sub: `${s.address} · ${s.status}`, icon: "server", go: () => navigate("/server/" + s.id) });
      for (const t of ["processes", "services", "logs", "docker"]) base.push({ label: `${s.name} › ${t}`, sub: s.address, icon: "chevron", go: () => navigate(`/server/${s.id}/${t}`) });
    }
    const ql = q.toLowerCase();
    return base.filter((i) => !ql || i.label.toLowerCase().includes(ql) || i.sub.toLowerCase().includes(ql)).slice(0, 14);
  }, [q, servers]);
  return (
    <div className="overlay" style={{ placeItems: "start center", paddingTop: "12vh" }} onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="modal" style={{ width: 560 }}>
        <div className="row" style={{ padding: "12px 14px", borderBottom: "1px solid var(--line)" }}>
          <Icon name="search" />
          <input
            autoFocus
            className="input grow"
            style={{ border: 0, background: "none", boxShadow: "none" }}
            placeholder="Jump to a server, workspace or setting…"
            value={q}
            onChange={(e) => {
              setQ(e.target.value);
              setSel(0);
            }}
            onKeyDown={(e) => {
              if (e.key === "Escape") onClose();
              if (e.key === "ArrowDown") setSel((s) => Math.min(s + 1, items.length - 1));
              if (e.key === "ArrowUp") setSel((s) => Math.max(s - 1, 0));
              if (e.key === "Enter" && items[sel]) {
                items[sel].go();
                onClose();
              }
            }}
          />
          <kbd>Esc</kbd>
        </div>
        <div style={{ maxHeight: 420, overflow: "auto", padding: 6 }}>
          {items.map((it, i) => (
            <div
              key={it.label + i}
              className="row"
              onMouseEnter={() => setSel(i)}
              onClick={() => {
                it.go();
                onClose();
              }}
              style={{ padding: "8px 10px", borderRadius: "var(--radius-sm)", cursor: "pointer", background: i === sel ? "var(--accent-soft)" : undefined }}
            >
              <Icon name={it.icon} size={15} />
              <span>{it.label}</span>
              <span className="faint" style={{ fontSize: "var(--fs-xs)" }}>{it.sub}</span>
            </div>
          ))}
          {items.length === 0 && <div className="faint" style={{ padding: 16 }}>Nothing matches.</div>}
        </div>
      </div>
    </div>
  );
}
