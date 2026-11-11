// Wire types mirrored from internal/protocol and internal/core/store.

export interface CPUStats {
  usage_pct: number;
  user_pct: number;
  system_pct: number;
  iowait_pct: number;
  steal_pct: number;
  per_core?: number[];
  threads: number;
}
export interface MemStats {
  total: number;
  available: number;
  used: number;
  buffers: number;
  cached: number;
  used_pct: number;
  swap_total: number;
  swap_used: number;
  swap_pct: number;
}
export interface FSUsage {
  mount: string;
  device: string;
  fstype: string;
  total: number;
  used: number;
  avail: number;
  used_pct: number;
  inodes_total: number;
  inodes_used: number;
  inodes_pct: number;
  read_only: boolean;
}
export interface DiskIO {
  device: string;
  read_bps: number;
  write_bps: number;
  read_iops: number;
  write_iops: number;
  util_pct: number;
}
export interface NetRate {
  iface: string;
  rx_bps: number;
  tx_bps: number;
  rx_pps: number;
  tx_pps: number;
  rx_errs: number;
  tx_errs: number;
  rx_drop: number;
  tx_drop: number;
}
export interface Sample {
  seq: number;
  time: string;
  cpu: CPUStats;
  load: [number, number, number];
  mem: MemStats;
  fs: FSUsage[] | null;
  disk_io: DiskIO[] | null;
  net: NetRate[] | null;
  procs: { total: number; running: number; blocked: number; zombie: number; threads: number };
  uptime_sec: number;
  temps?: { sensor: string; label: string; celsius: number }[];
  tcp_established: number;
}

export interface NetIface {
  name: string;
  mac?: string;
  mtu: number;
  oper_state: string;
  speed_mbps?: number;
  addrs: string[] | null;
  loopback: boolean;
  virtual: boolean;
}
export interface BlockDevice {
  name: string;
  type: string;
  size: number;
  model?: string;
  serial?: string;
  rotational: boolean;
  fstype?: string;
  mounts?: string[];
  children?: BlockDevice[];
}
export interface SystemInfo {
  hostname: string;
  os_name: string;
  os_id: string;
  os_version: string;
  os_codename?: string;
  kernel: string;
  arch: string;
  cpu_model: string;
  cpu_cores: number;
  cpu_threads: number;
  mem_total: number;
  swap_total: number;
  boot_time: string;
  virtualization?: string;
  timezone?: string;
  interfaces: NetIface[] | null;
  block_devices: BlockDevice[] | null;
  package_manager: string;
  init_system: string;
  docker_available: boolean;
  last_update?: string;
  reboot_required: boolean;
  reboot_required_pkgs?: string[];
}
export interface Health {
  agent_id: string;
  agent_version: string;
  hostname: string;
  boot_id: string;
  boot_time: string;
  agent_started: string;
  sampler_ok: boolean;
  sampler_error?: string;
  interval_sec: number;
  capabilities: string[];
  running_as_root: boolean;
  helper_ok: boolean;
  helper_error?: string;
  agent_rss_bytes: number;
}

export interface Server {
  id: string;
  name: string;
  address: string;
  port: number;
  agent_id: string;
  agent_fingerprint: string;
  environment: string;
  group: string;
  tags: string[];
  notes: string;
  monitoring_enabled: boolean;
  created_at: string;
  enrolled_at?: string;
  info?: SystemInfo;
  info_at?: string;
  health?: Health;
  status: string;
  status_reason: string;
  last_seen?: string;
  boot_id: string;
  latest?: Sample;
  latest_at?: string;
  health_score: number | null;
  alerts: Record<string, number>;
}

export interface Point {
  t: number;
  cpu: number | null;
  cpu_max?: number | null;
  mem: number | null;
  swap: number | null;
  load1: number | null;
  load5: number | null;
  load15: number | null;
  disk_max: number | null;
  rx_bps: number | null;
  tx_bps: number | null;
  read_bps: number | null;
  write_bps: number | null;
  iowait: number | null;
  procs: number | null;
  tcp: number | null;
  temp_max: number | null;
}

export interface Alert {
  id: string;
  fingerprint: string;
  server_id: string;
  rule: string;
  severity: "critical" | "warning" | "info";
  subject: string;
  title: string;
  detail: string;
  evidence?: unknown;
  status: string;
  first_seen: string;
  last_seen: string;
  resolved_at?: string;
  acknowledged_at?: string;
  count: number;
  incident_id: string;
}
export interface Incident {
  id: string;
  server_id: string;
  title: string;
  severity: string;
  status: string;
  opened_at: string;
  updated_at: string;
  resolved_at?: string;
  ai_status: string;
  ai_report: string;
  ai_conversation_id: string;
  alerts?: Alert[];
}

export interface Conversation {
  id: string;
  title: string;
  server_id: string;
  mode: Mode;
  model: string;
  origin: string;
  created_at: string;
  updated_at: string;
}
export type Mode = "assistant" | "analyst" | "administrator" | "autonomous";

export interface ToolCall {
  function: { name: string; arguments: Record<string, unknown> };
}
export interface Message {
  id: number;
  conversation_id: string;
  role: "user" | "assistant" | "tool" | "event";
  content: string;
  tool_calls?: ToolCall[];
  tool_name?: string;
  execution_id?: string;
  meta?: Record<string, any>;
  created_at: string;
}
export interface Execution {
  id: string;
  conversation_id: string;
  incident_id: string;
  server_id: string;
  tool: string;
  args: Record<string, unknown>;
  risk: Risk;
  requested_by: string;
  mode: string;
  decision: string;
  decision_reason: string;
  status: string;
  result?: unknown;
  error?: string;
  created_at: string;
  started_at?: string;
  finished_at?: string;
}
export type Risk = "read" | "low" | "medium" | "high";

export interface Impact {
  summary: string;
  effects: string[] | null;
  risks: string[] | null;
  rollback: string;
  evidence?: unknown;
  confirm?: string;
}
export interface Approval {
  id: string;
  execution_id: string;
  server_id: string;
  tool: string;
  risk: Risk;
  summary: string;
  impact?: Impact;
  status: string;
  requested_at: string;
  expires_at: string;
  decided_at?: string;
  decided_by?: string;
  reason?: string;
  execution?: Execution;
}

export interface Notification {
  id: number;
  time: string;
  severity: string;
  kind: string;
  title: string;
  body: string;
  server_id: string;
  ref: string;
  read: boolean;
}

export interface ToolInfo {
  name: string;
  description: string;
  risk: Risk;
  timeout_sec: number;
  fleet: boolean;
  internal: boolean;
  capability?: string;
}

export interface ModelInfo {
  name: string;
  model: string;
  modified_at: string;
  size: number;
  digest: string;
  details: { family: string; parameter_size: string; quantization_level: string; format: string };
  capabilities: { tools: boolean; vision: boolean; thinking: boolean; embedding: boolean; completion: boolean; context_length: number; source: string };
  params_b: number;
  capabilities_error?: string;
  fits_memory?: boolean;
}
export interface Resolution {
  role: string;
  model: string;
  preferred?: string;
  automatic: boolean;
  notice?: string;
  tools: boolean;
}
export interface AIStatus {
  reachable: boolean;
  url: string;
  local: boolean;
  version?: string;
  error?: string;
  models: number;
  running: { name: string; size: number; size_vram: number; expires_at: string; context_length: number }[];
  roles: Resolution[] | null;
  role_error?: string;
}

export interface Policy {
  low: string;
  medium: string;
  high: string;
  tool_overrides?: Record<string, string>;
  autonomous_enabled: boolean;
  autonomous_tools: string[];
  maintenance_windows: { days: string[]; start: string; end: string }[];
  protected: string[];
  monitoring_level: string;
  auto_investigate: boolean;
  allow_reboot: boolean;
}

export interface Memory {
  id: string;
  server_id: string;
  kind: string;
  content: string;
  source: string;
  created_at: string;
  updated_at: string;
  observed_at: string;
}

export interface AuditEntry {
  id: number;
  time: string;
  actor: string;
  action: string;
  target: string;
  detail: unknown;
  hash: string;
}

export interface Socket {
  proto: string;
  local_addr: string;
  local_port: number;
  state: string;
  pid?: number;
  process?: string;
  user?: string;
  scope: string;
}
