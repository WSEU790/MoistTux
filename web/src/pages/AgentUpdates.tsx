import { useEffect, useState } from "react";
import { api } from "../lib/api";
import { useApp } from "../lib/state";
import { Icon } from "../components/Icon";
import { Notice, Switch } from "../components/UI";

export type AgentStatus = { server_id: string; name: string; version: string; latest: string; outdated: boolean; bundled: boolean; state?: string; message?: string };
type AgentsResp = { agents: AgentStatus[]; settings: { auto_upgrade: boolean } };

/** Live agent versions; reloads when an upgrade reports progress. */
export function useAgents() {
  const { on } = useApp();
  const [data, setData] = useState<AgentsResp | null>(null);
  const load = () => api.get<AgentsResp>("/api/agents").then(setData).catch(() => {});
  useEffect(() => {
    load();
    const off = on((e) => (e.type === "agent.upgrade" || e.type === "server.status") && load());
    return off;
  }, [on]);
  return { data, reload: load };
}

/** Fleet banner: agents older than this NEXUS, with one-click upgrades. */
export function AgentUpdateBanner() {
  const { data, reload } = useAgents();
  const { toast } = useApp();
  if (!data) return null;
  const outdated = data.agents.filter((a) => a.outdated);
  const running = data.agents.filter((a) => a.state === "running");
  const failed = data.agents.filter((a) => a.state === "failed");
  if (!outdated.length && !running.length && !failed.length) return null;
  return (
    <div className="col" style={{ gap: 8, marginBottom: 14 }}>
      {outdated.length > 0 && (
        <Notice kind="warn" icon="download">
          <div className="row wrap" style={{ justifyContent: "space-between", gap: 10 }}>
            <span>
              {outdated.length === 1 ? "1 server runs" : `${outdated.length} servers run`} an older agent than this NEXUS ({outdated[0].latest}): {outdated.map((a) => `${a.name} (${a.version})`).join(", ")}.
            </span>
            <button className="btn sm primary" disabled={running.length > 0} onClick={() => api.post("/api/agents/upgrade-all").then(() => { toast({ title: "Upgrading agents", body: "One server at a time; each takes about half a minute.", kind: "ok" }); setTimeout(reload, 800); })}>
              <Icon name="download" size={12} /> Upgrade all
            </button>
          </div>
        </Notice>
      )}
      {running.map((a) => <Notice key={a.server_id} icon="refresh">Upgrading the agent on {a.name}: {a.message}</Notice>)}
      {failed.map((a) => <Notice key={a.server_id} kind="crit">Agent upgrade on {a.name} failed: {a.message}</Notice>)}
    </div>
  );
}

/** One server's agent version with an upgrade button (server settings). */
export function AgentVersionRow({ serverId }: { serverId: string }) {
  const { data, reload } = useAgents();
  const a = data?.agents.find((x) => x.server_id === serverId);
  if (!a) return null;
  return (
    <div className="row">
      <div className="grow">
        <b>Agent</b>
        <div className="faint" style={{ fontSize: "var(--fs-xs)" }}>
          version {a.version || "unknown"}{a.outdated ? ` · ${a.latest} is available` : " · up to date"}
          {a.state === "running" && ` · ${a.message}`}
          {a.state === "failed" && ` · last upgrade failed: ${a.message}`}
          {a.state === "succeeded" && ` · ${a.message}`}
        </div>
      </div>
      {a.outdated && (
        <button className="btn" disabled={a.state === "running"} onClick={() => api.post(`/api/servers/${serverId}/agent-upgrade`).then(() => setTimeout(reload, 800))}>
          <Icon name="download" /> Upgrade agent
        </button>
      )}
    </div>
  );
}

/** Settings → Updates: automatic agent upgrades. */
export function AgentAutoUpgradeSwitch() {
  const { data, reload } = useAgents();
  if (!data) return null;
  return (
    <Switch
      on={data.settings.auto_upgrade}
      onChange={(v) => api.put("/api/settings/agents", { auto_upgrade: v }).then(reload)}
      label="Upgrade agents on my servers automatically when NEXUS updates (checked hourly; servers that need an SSH repair are left for you)"
    />
  );
}
