export function bytes(n: number | null | undefined, digits = 1): string {
  if (n === null || n === undefined || isNaN(n)) return "—";
  const u = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"];
  let i = 0;
  let v = Math.abs(n);
  while (v >= 1024 && i < u.length - 1) {
    v /= 1024;
    i++;
  }
  return `${n < 0 ? "-" : ""}${v.toFixed(i === 0 ? 0 : v >= 100 ? 0 : digits)} ${u[i]}`;
}

export function rate(bps: number | null | undefined): string {
  if (bps === null || bps === undefined) return "—";
  return bytes(bps) + "/s";
}

export function pct(v: number | null | undefined, d = 1): string {
  if (v === null || v === undefined || isNaN(v)) return "—";
  return v.toFixed(d) + "%";
}

export function num(v: number | null | undefined, d = 2): string {
  if (v === null || v === undefined || isNaN(v)) return "—";
  return v.toFixed(d);
}

export function duration(sec: number | null | undefined): string {
  if (sec === null || sec === undefined || isNaN(sec)) return "—";
  const d = Math.floor(sec / 86400);
  const h = Math.floor((sec % 86400) / 3600);
  const m = Math.floor((sec % 3600) / 60);
  if (d > 0) return `${d}d ${h}h`;
  if (h > 0) return `${h}h ${m}m`;
  if (m > 0) return `${m}m`;
  return `${Math.floor(sec)}s`;
}

export function ago(iso: string | number | undefined | null): string {
  if (!iso) return "—";
  const t = typeof iso === "number" ? iso : Date.parse(iso);
  if (isNaN(t)) return "—";
  const s = (Date.now() - t) / 1000;
  if (s < 0) return "in " + duration(-s);
  if (s < 5) return "just now";
  if (s < 60) return `${Math.floor(s)}s ago`;
  return duration(s) + " ago";
}

export function until(iso: string | undefined): string {
  if (!iso) return "—";
  const s = (Date.parse(iso) - Date.now()) / 1000;
  if (s <= 0) return "expired";
  return "in " + duration(s);
}

export function time(iso: string | number | undefined): string {
  if (!iso) return "—";
  const d = new Date(iso);
  return d.toLocaleString(undefined, { month: "short", day: "2-digit", hour: "2-digit", minute: "2-digit", second: "2-digit" });
}

export function clock(t: number): string {
  return new Date(t).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
}

export function sevClass(s: string | undefined): string {
  switch (s) {
    case "critical":
    case "high":
      return "crit";
    case "warning":
    case "medium":
      return "warn";
    case "info":
    case "low":
      return "info";
  }
  return "";
}

export function statusClass(s: string): string {
  switch (s) {
    case "online":
      return "ok";
    case "degraded":
      return "warn";
    case "offline":
    case "untrusted":
      return "crit";
  }
  return "";
}

export function levelClass(v: number | null | undefined, warn = 80, crit = 92): string {
  if (v === null || v === undefined) return "";
  if (v >= crit) return "crit";
  if (v >= warn) return "warn";
  return "ok";
}

export function riskClass(r: string): string {
  return { read: "", low: "info", medium: "warn", high: "crit" }[r] ?? "";
}

export function maxFS(fs: { used_pct: number }[] | null | undefined): number | null {
  if (!fs || fs.length === 0) return null;
  return Math.max(...fs.map((f) => f.used_pct));
}

export function netTotals(net: { iface: string; rx_bps: number; tx_bps: number }[] | null | undefined) {
  let rx = 0,
    tx = 0;
  for (const n of net ?? []) {
    if (n.iface === "lo") continue;
    rx += n.rx_bps;
    tx += n.tx_bps;
  }
  return { rx, tx };
}
