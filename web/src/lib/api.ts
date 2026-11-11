// Thin client for the NEXUS loopback API. The session cookie is set by the
// launch URL; requests are same-origin.

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

async function request<T>(method: string, path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
  const res = await fetch(path, {
    method,
    credentials: "same-origin",
    headers: body !== undefined ? { "Content-Type": "application/json" } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined,
    signal,
  });
  const text = await res.text();
  let data: any = null;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = text;
    }
  }
  if (!res.ok) {
    const msg = (data && typeof data === "object" && data.error) || (typeof data === "string" && data) || res.statusText;
    throw new ApiError(res.status, msg);
  }
  return data as T;
}

export const api = {
  get: <T,>(p: string, signal?: AbortSignal) => request<T>("GET", p, undefined, signal),
  post: <T,>(p: string, body?: unknown) => request<T>("POST", p, body ?? {}),
  put: <T,>(p: string, body: unknown) => request<T>("PUT", p, body),
  patch: <T,>(p: string, body: unknown) => request<T>("PATCH", p, body),
  del: <T,>(p: string) => request<T>("DELETE", p),
};

export const agent = (serverId: string, path: string, q?: Record<string, string | number | undefined>) => {
  const qs = new URLSearchParams();
  if (q) for (const [k, v] of Object.entries(q)) if (v !== undefined && v !== "") qs.set(k, String(v));
  const s = qs.toString();
  return `/api/servers/${encodeURIComponent(serverId)}/agent/${path}${s ? "?" + s : ""}`;
};

// ---------------------------------------------------------------- live events

export type BusEvent = { id: number; type: string; time: string; data: any };
type Listener = (e: BusEvent) => void;

class EventStream {
  private es: EventSource | null = null;
  private listeners = new Set<Listener>();
  private statusListeners = new Set<(s: boolean) => void>();
  connected = false;
  private retry = 1000;

  private static TYPES = [
    "metrics", "server.added", "server.removed", "server.updated", "server.status", "inventory", "alert", "incident",
    "notification", "approval", "execution", "chat.message", "chat.delta", "chat.state", "chat.conversation", "model.pull", "policy",
  ];

  start() {
    if (this.es) return;
    const es = new EventSource("/api/events", { withCredentials: true });
    this.es = es;
    es.addEventListener("hello", () => {
      this.retry = 1000;
      this.setConnected(true);
    });
    for (const t of EventStream.TYPES) {
      es.addEventListener(t, (ev) => {
        try {
          const e = JSON.parse((ev as MessageEvent).data) as BusEvent;
          this.listeners.forEach((l) => l(e));
        } catch {
          /* ignore malformed frame */
        }
      });
    }
    es.onerror = () => {
      this.setConnected(false);
      es.close();
      this.es = null;
      setTimeout(() => this.start(), this.retry);
      this.retry = Math.min(this.retry * 2, 15000);
    };
  }
  private setConnected(v: boolean) {
    if (this.connected !== v) {
      this.connected = v;
      this.statusListeners.forEach((l) => l(v));
    }
  }
  on(l: Listener) {
    this.listeners.add(l);
    return () => {
      this.listeners.delete(l);
    };
  }
  onStatus(l: (s: boolean) => void) {
    this.statusListeners.add(l);
    return () => {
      this.statusListeners.delete(l);
    };
  }
}

export const events = new EventStream();
