import React, { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from "react";
import { api, events, BusEvent } from "./api";
import type { AIStatus, Sample, Server } from "./types";

// ---------------------------------------------------------------- router

export type Route = { path: string; parts: string[]; query: URLSearchParams };

function parse(): Route {
  const h = window.location.hash.replace(/^#/, "") || "/";
  const [p, q] = h.split("?");
  return { path: p, parts: p.split("/").filter(Boolean), query: new URLSearchParams(q ?? "") };
}

export function navigate(to: string) {
  if (window.location.hash !== "#" + to) window.location.hash = to;
}

export function useRoute(): Route {
  const [r, setR] = useState(parse);
  useEffect(() => {
    const on = () => setR(parse());
    window.addEventListener("hashchange", on);
    return () => window.removeEventListener("hashchange", on);
  }, []);
  return r;
}

// ---------------------------------------------------------------- appearance

export type Appearance = {
  theme: "graphite" | "midnight" | "tactical" | "light";
  accent: number; // hue
  density: "compact" | "comfortable" | "spacious";
  motion: "full" | "reduced";
  sidebar: "expanded" | "collapsed";
  layout: { id: string; visible: boolean }[];
};

export const DEFAULT_LAYOUT = [
  { id: "kpis", visible: true },
  { id: "fleet", visible: true },
  { id: "alerts", visible: true },
  { id: "trend", visible: true },
  { id: "ai", visible: true },
  { id: "activity", visible: true },
];

const DEFAULT_APPEARANCE: Appearance = { theme: "graphite", accent: 188, density: "comfortable", motion: "full", sidebar: "expanded", layout: DEFAULT_LAYOUT };

function applyAppearance(a: Appearance) {
  const el = document.documentElement;
  el.dataset.theme = a.theme;
  el.dataset.density = a.density;
  el.dataset.motion = a.motion;
  const hue = a.theme === "tactical" && a.accent === 188 ? 44 : a.accent;
  el.style.setProperty("--accent-h", String(hue));
}

// ---------------------------------------------------------------- global store

type Toast = { id: number; title: string; body?: string; kind?: string };

interface Ctx {
  servers: Server[];
  serversLoaded: boolean;
  reloadServers: () => void;
  serverById: (id: string) => Server | undefined;
  live: (id: string) => Sample[]; // recent live samples (session)
  liveTick: number;
  overview: any;
  reloadOverview: () => void;
  ai: AIStatus | null;
  reloadAI: () => void;
  connected: boolean;
  appearance: Appearance;
  setAppearance: (p: Partial<Appearance>) => void;
  toasts: Toast[];
  toast: (t: Omit<Toast, "id">) => void;
  dismissToast: (id: number) => void;
  unread: number;
  setUnread: (n: number) => void;
  pendingApprovals: number;
  bootstrap: any;
  on: (fn: (e: BusEvent) => void) => () => void;
}

const C = createContext<Ctx>(null as any);
export const useApp = () => useContext(C);

export function AppProvider({ children }: { children: React.ReactNode }) {
  const [servers, setServers] = useState<Server[]>([]);
  const [serversLoaded, setLoaded] = useState(false);
  const [overview, setOverview] = useState<any>(null);
  const [ai, setAI] = useState<AIStatus | null>(null);
  const [connected, setConnected] = useState(false);
  const [appearance, setApp] = useState<Appearance>(() => {
    try {
      const raw = localStorage.getItem("nexus.appearance");
      if (raw) return { ...DEFAULT_APPEARANCE, ...JSON.parse(raw) };
    } catch {
      /* storage unavailable */
    }
    return DEFAULT_APPEARANCE;
  });
  const [toasts, setToasts] = useState<Toast[]>([]);
  const [unread, setUnread] = useState(0);
  const [pendingApprovals, setPending] = useState(0);
  const [bootstrap, setBootstrap] = useState<any>(null);
  const [liveTick, setLiveTick] = useState(0);
  const liveRef = useRef(new Map<string, Sample[]>());
  const toastId = useRef(1);

  useEffect(() => applyAppearance(appearance), [appearance]);

  const reloadServers = useCallback(() => {
    api.get<Server[]>("/api/servers").then((s) => {
      setServers(s);
      setLoaded(true);
      for (const srv of s) {
        if (srv.latest && !liveRef.current.has(srv.id)) liveRef.current.set(srv.id, [srv.latest]);
      }
    }).catch(() => setLoaded(true));
  }, []);
  const reloadOverview = useCallback(() => {
    api.get("/api/overview").then(setOverview).catch(() => {});
    api.get<any[]>("/api/approvals?status=pending").then((a) => setPending(a.length)).catch(() => {});
  }, []);
  const reloadAI = useCallback(() => {
    api.get<AIStatus>("/api/ai/status").then(setAI).catch(() => setAI(null));
  }, []);

  const toast = useCallback((t: Omit<Toast, "id">) => {
    const id = toastId.current++;
    setToasts((x) => [...x.slice(-4), { ...t, id }]);
    setTimeout(() => setToasts((x) => x.filter((y) => y.id !== id)), 7000);
  }, []);
  const dismissToast = useCallback((id: number) => setToasts((x) => x.filter((y) => y.id !== id)), []);

  const setAppearance = useCallback((p: Partial<Appearance>) => {
    setApp((prev) => {
      const next = { ...prev, ...p };
      try {
        localStorage.setItem("nexus.appearance", JSON.stringify(next));
      } catch {
        /* storage unavailable */
      }
      api.put("/api/settings/appearance", next).catch(() => {});
      return next;
    });
  }, []);

  useEffect(() => {
    api.get<any>("/api/bootstrap").then((b) => {
      setBootstrap(b);
      if (b.appearance && Object.keys(b.appearance).length > 0) {
        setApp((prev) => ({ ...prev, ...b.appearance }));
      }
    }).catch(() => {});
    api.get<any>("/api/notifications?limit=1").then((n) => setUnread(n.unread)).catch(() => {});
    reloadServers();
    reloadOverview();
    reloadAI();
    events.start();
    const offStatus = events.onStatus((s) => {
      setConnected(s);
      if (s) {
        reloadServers();
        reloadOverview();
      }
    });
    setConnected(events.connected);
    const t1 = setInterval(reloadOverview, 30000);
    const t2 = setInterval(reloadAI, 20000);
    const t3 = setInterval(reloadServers, 60000);
    return () => {
      offStatus();
      clearInterval(t1);
      clearInterval(t2);
      clearInterval(t3);
    };
  }, [reloadServers, reloadOverview, reloadAI]);

  // Fold live events into state.
  useEffect(() => {
    let pendingReload: number | undefined;
    const debounced = () => {
      if (pendingReload) return;
      pendingReload = window.setTimeout(() => {
        pendingReload = undefined;
        reloadServers();
        reloadOverview();
      }, 600);
    };
    return events.on((e) => {
      switch (e.type) {
        case "metrics": {
          const { server_id, sample } = e.data as { server_id: string; sample: Sample };
          const arr = liveRef.current.get(server_id) ?? [];
          if (!arr.length || arr[arr.length - 1].seq !== sample.seq) arr.push(sample);
          if (arr.length > 360) arr.splice(0, arr.length - 360);
          liveRef.current.set(server_id, arr);
          setServers((prev) => prev.map((s) => (s.id === server_id ? { ...s, latest: sample, latest_at: new Date().toISOString() } : s)));
          setLiveTick((t) => t + 1);
          break;
        }
        case "server.added":
        case "server.removed":
        case "server.updated":
        case "server.status":
        case "inventory":
        case "alert":
        case "incident":
          debounced();
          break;
        case "approval":
          reloadOverview();
          if (e.data?.status === "pending") toast({ title: "Approval required", body: e.data.summary, kind: "warn" });
          break;
        case "notification": {
          const n = e.data;
          setUnread((u) => u + 1);
          toast({ title: n.title, body: n.body, kind: n.severity === "critical" ? "crit" : n.severity === "warning" ? "warn" : "" });
          if ((n.severity === "critical" || n.severity === "warning") && "Notification" in window && Notification.permission === "granted" && document.visibilityState !== "visible") {
            try {
              new Notification(n.title, { body: n.body, tag: n.ref || String(n.id) });
            } catch {
              /* notification API unavailable */
            }
          }
          break;
        }
      }
    });
  }, [reloadServers, reloadOverview, toast]);

  const value = useMemo<Ctx>(
    () => ({
      servers, serversLoaded, reloadServers, serverById: (id) => servers.find((s) => s.id === id),
      live: (id) => liveRef.current.get(id) ?? [], liveTick, overview, reloadOverview, ai, reloadAI, connected,
      appearance, setAppearance, toasts, toast, dismissToast, unread, setUnread, pendingApprovals, bootstrap, on: (fn) => events.on(fn),
    }),
    [servers, serversLoaded, reloadServers, liveTick, overview, reloadOverview, ai, reloadAI, connected, appearance, setAppearance, toasts, toast, dismissToast, unread, pendingApprovals, bootstrap],
  );
  return <C.Provider value={value}>{children}</C.Provider>;
}

// ---------------------------------------------------------------- data hooks

export function useFetch<T>(url: string | null, deps: unknown[] = [], intervalMs?: number) {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [n, setN] = useState(0);
  const reload = useCallback(() => setN((x) => x + 1), []);
  useEffect(() => {
    if (!url) return;
    let alive = true;
    const ctrl = new AbortController();
    setLoading(true);
    api
      .get<T>(url, ctrl.signal)
      .then((d) => {
        if (alive) {
          setData(d);
          setError(null);
        }
      })
      .catch((e) => {
        if (alive && e.name !== "AbortError") setError(e.message);
      })
      .finally(() => alive && setLoading(false));
    return () => {
      alive = false;
      ctrl.abort();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [url, n, ...deps]);
  useEffect(() => {
    if (!intervalMs || !url) return;
    const t = setInterval(reload, intervalMs);
    return () => clearInterval(t);
  }, [intervalMs, url, reload]);
  return { data, error, loading, reload, setData };
}
