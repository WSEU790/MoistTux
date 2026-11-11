// Package protocol defines the v1 wire types exchanged between the NEXUS core
// and nexus-agent. Every field is produced from a real system source; when a
// source is unavailable the field is omitted or an explicit Unavailable reason
// is set — values are never invented.
package protocol

import "time"

const Version = "1"

// Error is the JSON body of every non-2xx agent response.
type Error struct {
	Error string `json:"error"`
	Code  string `json:"code,omitempty"`
}

// ---------------------------------------------------------------- enrollment

type EnrollRequest struct {
	Token      string `json:"token"`       // base32 token from the enrollment code
	ClientCert string `json:"client_cert"` // PEM; must equal the TLS client certificate
	Label      string `json:"label"`       // human label for the core, e.g. "NEXUS on WORKSTATION-7"
}

type EnrollResponse struct {
	AgentID      string    `json:"agent_id"`
	Hostname     string    `json:"hostname"`
	AgentVersion string    `json:"agent_version"`
	Fingerprint  string    `json:"fingerprint"`
	EnrolledAt   time.Time `json:"enrolled_at"`
}

type RotateClientRequest struct {
	NewClientCert string `json:"new_client_cert"`
}

type RotateAgentResponse struct {
	NewFingerprint string `json:"new_fingerprint"`
	ActivatesAfter string `json:"activates_after"`
}

// ---------------------------------------------------------------- health

type Health struct {
	AgentID       string    `json:"agent_id"`
	AgentVersion  string    `json:"agent_version"`
	Hostname      string    `json:"hostname"`
	BootID        string    `json:"boot_id"`
	BootTime      time.Time `json:"boot_time"`
	AgentStarted  time.Time `json:"agent_started"`
	Now           time.Time `json:"now"`
	SamplerOK     bool      `json:"sampler_ok"`
	SamplerError  string    `json:"sampler_error,omitempty"`
	LastSeq       uint64    `json:"last_seq"`
	IntervalSec   int       `json:"interval_sec"`
	Capabilities  []string  `json:"capabilities"`
	RunningAsRoot bool      `json:"running_as_root"`
	HelperOK      bool      `json:"helper_ok"`
	HelperError   string    `json:"helper_error,omitempty"`
	AgentRSSBytes uint64    `json:"agent_rss_bytes"`
}

// ---------------------------------------------------------------- system info

type SystemInfo struct {
	Hostname        string        `json:"hostname"`
	OSName          string        `json:"os_name"`
	OSID            string        `json:"os_id"`
	OSIDLike        []string      `json:"os_id_like,omitempty"`
	OSVersion       string        `json:"os_version"`
	OSCodename      string        `json:"os_codename,omitempty"`
	Kernel          string        `json:"kernel"`
	Arch            string        `json:"arch"`
	CPUModel        string        `json:"cpu_model"`
	CPUCores        int           `json:"cpu_cores"`
	CPUThreads      int           `json:"cpu_threads"`
	MemTotal        uint64        `json:"mem_total"`
	SwapTotal       uint64        `json:"swap_total"`
	BootTime        time.Time     `json:"boot_time"`
	Virtualization  string        `json:"virtualization,omitempty"`
	MachineID       string        `json:"machine_id,omitempty"`
	Timezone        string        `json:"timezone,omitempty"`
	Interfaces      []NetIface    `json:"interfaces"`
	BlockDevices    []BlockDevice `json:"block_devices"`
	PackageManager  string        `json:"package_manager"`
	InitSystem      string        `json:"init_system"`
	DockerAvailable bool          `json:"docker_available"`
	LastUpdate      *time.Time    `json:"last_update,omitempty"`
	RebootRequired  bool          `json:"reboot_required"`
	RebootPkgs      []string      `json:"reboot_required_pkgs,omitempty"`
}

type NetIface struct {
	Name      string   `json:"name"`
	MAC       string   `json:"mac,omitempty"`
	MTU       int      `json:"mtu"`
	OperState string   `json:"oper_state"`
	SpeedMbps int      `json:"speed_mbps,omitempty"`
	Addrs     []string `json:"addrs"`
	Loopback  bool     `json:"loopback"`
	Virtual   bool     `json:"virtual"`
}

type BlockDevice struct {
	Name       string        `json:"name"`
	Type       string        `json:"type"`
	Size       uint64        `json:"size"`
	Model      string        `json:"model,omitempty"`
	Serial     string        `json:"serial,omitempty"`
	Rotational bool          `json:"rotational"`
	FSType     string        `json:"fstype,omitempty"`
	Mounts     []string      `json:"mounts,omitempty"`
	Children   []BlockDevice `json:"children,omitempty"`
}

// ---------------------------------------------------------------- metrics

type MetricsBatch struct {
	AgentID  string          `json:"agent_id"`
	BootID   string          `json:"boot_id"`
	Samples  []MetricsSample `json:"samples"`
	LastSeq  uint64          `json:"last_seq"`
	Interval int             `json:"interval_sec"`
}

type MetricsSample struct {
	Seq      uint64      `json:"seq"`
	Time     time.Time   `json:"time"`
	CPU      CPUStats    `json:"cpu"`
	Load     [3]float64  `json:"load"`
	Mem      MemStats    `json:"mem"`
	FS       []FSUsage   `json:"fs"`
	DiskIO   []DiskIO    `json:"disk_io"`
	Net      []NetRate   `json:"net"`
	Procs    ProcCounts  `json:"procs"`
	Uptime   float64     `json:"uptime_sec"`
	Temps    []TempValue `json:"temps,omitempty"`
	TCPConns int         `json:"tcp_established"`
}

type CPUStats struct {
	UsagePct  float64   `json:"usage_pct"`
	UserPct   float64   `json:"user_pct"`
	SystemPct float64   `json:"system_pct"`
	IOWaitPct float64   `json:"iowait_pct"`
	StealPct  float64   `json:"steal_pct"`
	PerCore   []float64 `json:"per_core,omitempty"`
	Threads   int       `json:"threads"`
}

type MemStats struct {
	Total     uint64  `json:"total"`
	Available uint64  `json:"available"`
	Used      uint64  `json:"used"`
	Buffers   uint64  `json:"buffers"`
	Cached    uint64  `json:"cached"`
	UsedPct   float64 `json:"used_pct"`
	SwapTotal uint64  `json:"swap_total"`
	SwapUsed  uint64  `json:"swap_used"`
	SwapPct   float64 `json:"swap_pct"`
}

type FSUsage struct {
	Mount       string  `json:"mount"`
	Device      string  `json:"device"`
	FSType      string  `json:"fstype"`
	Total       uint64  `json:"total"`
	Used        uint64  `json:"used"`
	Avail       uint64  `json:"avail"`
	UsedPct     float64 `json:"used_pct"`
	InodesTotal uint64  `json:"inodes_total"`
	InodesUsed  uint64  `json:"inodes_used"`
	InodesPct   float64 `json:"inodes_pct"`
	ReadOnly    bool    `json:"read_only"`
}

type DiskIO struct {
	Device    string  `json:"device"`
	ReadBps   float64 `json:"read_bps"`
	WriteBps  float64 `json:"write_bps"`
	ReadIOPS  float64 `json:"read_iops"`
	WriteIOPS float64 `json:"write_iops"`
	UtilPct   float64 `json:"util_pct"`
}

type NetRate struct {
	Iface  string  `json:"iface"`
	RxBps  float64 `json:"rx_bps"`
	TxBps  float64 `json:"tx_bps"`
	RxPps  float64 `json:"rx_pps"`
	TxPps  float64 `json:"tx_pps"`
	RxErrs uint64  `json:"rx_errs"`
	TxErrs uint64  `json:"tx_errs"`
	RxDrop uint64  `json:"rx_drop"`
	TxDrop uint64  `json:"tx_drop"`
}

type ProcCounts struct {
	Total   int `json:"total"`
	Running int `json:"running"`
	Blocked int `json:"blocked"`
	Zombie  int `json:"zombie"`
	Threads int `json:"threads"`
}

type TempValue struct {
	Sensor  string  `json:"sensor"`
	Label   string  `json:"label"`
	Celsius float64 `json:"celsius"`
}

// ---------------------------------------------------------------- processes

type Process struct {
	PID       int       `json:"pid"`
	PPID      int       `json:"ppid"`
	Name      string    `json:"name"`
	Cmdline   string    `json:"cmdline"`
	User      string    `json:"user"`
	State     string    `json:"state"`
	CPUPct    float64   `json:"cpu_pct"`
	RSS       uint64    `json:"rss"`
	VSize     uint64    `json:"vsize"`
	MemPct    float64   `json:"mem_pct"`
	Threads   int       `json:"threads"`
	StartTime time.Time `json:"start_time"`
	Unit      string    `json:"unit,omitempty"` // systemd unit from cgroup
	Container string    `json:"container,omitempty"`
}

type ProcessList struct {
	Processes []Process `json:"processes"`
	Total     int       `json:"total"`
	WindowMs  int64     `json:"cpu_window_ms"`
	Partial   string    `json:"partial,omitempty"` // reason when some processes were unreadable
}

type ProcessDetail struct {
	Process
	Exe          string            `json:"exe,omitempty"`
	Cwd          string            `json:"cwd,omitempty"`
	OpenFDs      int               `json:"open_fds"`
	Limits       map[string]string `json:"limits,omitempty"`
	Status       map[string]string `json:"status"`
	Children     []int             `json:"children"`
	Listening    []Socket          `json:"listening,omitempty"`
	Cgroup       string            `json:"cgroup,omitempty"`
	OOMScore     int               `json:"oom_score"`
	IOReadBytes  uint64            `json:"io_read_bytes"`
	IOWriteBytes uint64            `json:"io_write_bytes"`
	Unavailable  []string          `json:"unavailable,omitempty"`
}

// ---------------------------------------------------------------- services

type Service struct {
	Unit        string `json:"unit"`
	Description string `json:"description"`
	LoadState   string `json:"load_state"`
	ActiveState string `json:"active_state"`
	SubState    string `json:"sub_state"`
	UnitFile    string `json:"unit_file_state,omitempty"`
}

type ServiceList struct {
	Services    []Service `json:"services"`
	Failed      int       `json:"failed"`
	Running     int       `json:"running"`
	Available   bool      `json:"available"`
	Unavailable string    `json:"unavailable,omitempty"`
}

type ServiceDetail struct {
	Service
	MainPID         int               `json:"main_pid"`
	Result          string            `json:"result"`
	NRestarts       int               `json:"n_restarts"`
	Restart         string            `json:"restart_policy"`
	ActiveEnter     *time.Time        `json:"active_enter,omitempty"`
	InactiveEnter   *time.Time        `json:"inactive_enter,omitempty"`
	ExecMainStatus  int               `json:"exec_main_status"`
	MemoryCurrent   uint64            `json:"memory_current,omitempty"`
	CPUUsageNSec    uint64            `json:"cpu_usage_nsec,omitempty"`
	TasksCurrent    uint64            `json:"tasks_current,omitempty"`
	FragmentPath    string            `json:"fragment_path,omitempty"`
	Requires        []string          `json:"requires,omitempty"`
	Wants           []string          `json:"wants,omitempty"`
	After           []string          `json:"after,omitempty"`
	RequiredBy      []string          `json:"required_by,omitempty"`
	WantedBy        []string          `json:"wanted_by,omitempty"`
	Raw             map[string]string `json:"raw"`
	HealthAssessed  string            `json:"health"` // healthy | degraded | failed | inactive | unknown
	HealthReasons   []string          `json:"health_reasons"`
	ListeningPorts  []Socket          `json:"listening_ports,omitempty"`
	RecentLogErrors int               `json:"recent_log_errors"`
}

// ---------------------------------------------------------------- logs

type LogQuery struct {
	Unit     string `json:"unit,omitempty"`
	Since    string `json:"since,omitempty"` // RFC3339 or relative like "-24h"
	Until    string `json:"until,omitempty"`
	Priority int    `json:"priority,omitempty"` // 0..7, include this and more severe; 0 = any
	Grep     string `json:"grep,omitempty"`
	Limit    int    `json:"limit,omitempty"`
	File     string `json:"file,omitempty"` // allowlisted /var/log file when journald is absent
}

type LogEntry struct {
	Time     time.Time `json:"time"`
	Unit     string    `json:"unit,omitempty"`
	Ident    string    `json:"ident,omitempty"`
	PID      int       `json:"pid,omitempty"`
	Priority int       `json:"priority"`
	Message  string    `json:"message"`
}

type LogResult struct {
	Source    string     `json:"source"` // journald | file:<path>
	Entries   []LogEntry `json:"entries"`
	Truncated bool       `json:"truncated"`
	Note      string     `json:"note,omitempty"`
}

// ---------------------------------------------------------------- docker

type DockerInfo struct {
	Available     bool   `json:"available"`
	Unavailable   string `json:"unavailable,omitempty"`
	ServerVersion string `json:"server_version,omitempty"`
	APIVersion    string `json:"api_version,omitempty"`
	Containers    int    `json:"containers"`
	Running       int    `json:"running"`
	Paused        int    `json:"paused"`
	Stopped       int    `json:"stopped"`
	Images        int    `json:"images"`
	StorageDriver string `json:"storage_driver,omitempty"`
	DockerRootDir string `json:"docker_root_dir,omitempty"`
	CgroupDriver  string `json:"cgroup_driver,omitempty"`
	OS            string `json:"os,omitempty"`
	MemTotal      int64  `json:"mem_total,omitempty"`
	NCPU          int    `json:"ncpu,omitempty"`
	Warnings      []string `json:"warnings,omitempty"`
}

type Container struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Image          string            `json:"image"`
	ImageID        string            `json:"image_id"`
	Command        string            `json:"command"`
	Created        time.Time         `json:"created"`
	State          string            `json:"state"`
	Status         string            `json:"status"`
	Health         string            `json:"health,omitempty"`
	Ports          []ContainerPort   `json:"ports,omitempty"`
	Labels         map[string]string `json:"labels,omitempty"`
	ComposeProject string            `json:"compose_project,omitempty"`
	ComposeService string            `json:"compose_service,omitempty"`
	Networks       []string          `json:"networks,omitempty"`
	Mounts         []string          `json:"mounts,omitempty"`
}

type ContainerPort struct {
	IP          string `json:"ip,omitempty"`
	PrivatePort int    `json:"private_port"`
	PublicPort  int    `json:"public_port,omitempty"`
	Type        string `json:"type"`
}

type ContainerDetail struct {
	Container
	RestartCount  int        `json:"restart_count"`
	RestartPolicy string     `json:"restart_policy"`
	ExitCode      int        `json:"exit_code"`
	OOMKilled     bool       `json:"oom_killed"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
	Error         string     `json:"error,omitempty"`
	HealthLog     []string   `json:"health_log,omitempty"`
	Env           []string   `json:"env_keys,omitempty"` // keys only; values may be secrets
	Stats         *ContainerStats `json:"stats,omitempty"`
}

type ContainerStats struct {
	CPUPct     float64 `json:"cpu_pct"`
	MemUsage   uint64  `json:"mem_usage"`
	MemLimit   uint64  `json:"mem_limit"`
	MemPct     float64 `json:"mem_pct"`
	NetRx      uint64  `json:"net_rx"`
	NetTx      uint64  `json:"net_tx"`
	BlockRead  uint64  `json:"block_read"`
	BlockWrite uint64  `json:"block_write"`
	PIDs       uint64  `json:"pids"`
}

type DockerImage struct {
	ID       string    `json:"id"`
	Tags     []string  `json:"tags"`
	Size     int64     `json:"size"`
	Created  time.Time `json:"created"`
	InUse    int       `json:"containers"`
	Dangling bool      `json:"dangling"`
}

type DockerVolume struct {
	Name       string            `json:"name"`
	Driver     string            `json:"driver"`
	Mountpoint string            `json:"mountpoint"`
	Labels     map[string]string `json:"labels,omitempty"`
	Created    string            `json:"created,omitempty"`
}

type DockerNetwork struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Driver     string   `json:"driver"`
	Scope      string   `json:"scope"`
	Subnets    []string `json:"subnets,omitempty"`
	Containers []string `json:"containers,omitempty"`
	Internal   bool     `json:"internal"`
}

type ComposeProject struct {
	Name        string   `json:"name"`
	WorkingDir  string   `json:"working_dir,omitempty"`
	ConfigFiles []string `json:"config_files,omitempty"`
	Services    []string `json:"services"`
	Containers  int      `json:"containers"`
	Running     int      `json:"running"`
}

// ---------------------------------------------------------------- storage

type StorageOverview struct {
	Filesystems  []FSUsage     `json:"filesystems"`
	BlockDevices []BlockDevice `json:"block_devices"`
}

type DirUsage struct {
	Path     string     `json:"path"`
	Size     uint64     `json:"size"`
	Files    uint64     `json:"files"`
	Children []DirUsage `json:"children,omitempty"`
	Partial  bool       `json:"partial,omitempty"`
}

type DirUsageResult struct {
	Root       DirUsage `json:"root"`
	ScannedMs  int64    `json:"scanned_ms"`
	Errors     int      `json:"errors"`
	Truncated  bool     `json:"truncated"`
	CrossedFS  bool     `json:"crossed_fs"`
}

type LargeFile struct {
	Path    string    `json:"path"`
	Size    uint64    `json:"size"`
	ModTime time.Time `json:"mod_time"`
	Owner   string    `json:"owner,omitempty"`
}

type LargeFilesResult struct {
	Files     []LargeFile `json:"files"`
	Scanned   uint64      `json:"scanned"`
	Errors    int         `json:"errors"`
	Truncated bool        `json:"truncated"`
	ScannedMs int64       `json:"scanned_ms"`
}

// ---------------------------------------------------------------- network

type Socket struct {
	Proto     string `json:"proto"` // tcp | tcp6 | udp | udp6
	LocalAddr string `json:"local_addr"`
	LocalPort int    `json:"local_port"`
	State     string `json:"state"`
	PID       int    `json:"pid,omitempty"`
	Process   string `json:"process,omitempty"`
	User      string `json:"user,omitempty"`
	// Scope describes the bind address only. It deliberately does not claim
	// public exposure, which depends on firewall rules and upstream NAT.
	Scope string `json:"scope"` // loopback | all-interfaces | specific-address
}

type Route struct {
	Destination string `json:"destination"`
	Gateway     string `json:"gateway,omitempty"`
	Iface       string `json:"iface"`
	Metric      int    `json:"metric"`
	Default     bool   `json:"default"`
}

type NetworkOverview struct {
	Interfaces  []NetIface `json:"interfaces"`
	Routes      []Route    `json:"routes"`
	DNS         []string   `json:"dns_servers"`
	DNSSearch   []string   `json:"dns_search,omitempty"`
	Listening   []Socket   `json:"listening"`
	Established int        `json:"tcp_established"`
	Partial     string     `json:"partial,omitempty"`
}

type FirewallStatus struct {
	Backend     string   `json:"backend"` // ufw | nftables | iptables | none
	Active      bool     `json:"active"`
	Detected    []string `json:"detected"`
	DefaultIn   string   `json:"default_incoming,omitempty"`
	DefaultOut  string   `json:"default_outgoing,omitempty"`
	Rules       []string `json:"rules"`
	Raw         string   `json:"raw,omitempty"`
	Unavailable string   `json:"unavailable,omitempty"`
}

// ---------------------------------------------------------------- packages

type Package struct {
	Name         string `json:"name"`
	Version      string `json:"version"`
	Arch         string `json:"arch"`
	InstalledKiB uint64 `json:"installed_kib,omitempty"`
	Summary      string `json:"summary,omitempty"`
}

type PackageList struct {
	Manager  string    `json:"manager"`
	Packages []Package `json:"packages"`
	Total    int       `json:"total"`
}

type PackageUpdate struct {
	Name       string `json:"name"`
	Current    string `json:"current"`
	Candidate  string `json:"candidate"`
	Origin     string `json:"origin"`
	Arch       string `json:"arch,omitempty"`
	Security   bool   `json:"security"`
	NewInstall bool   `json:"new_install,omitempty"`
}

type UpdateCheck struct {
	Manager        string          `json:"manager"`
	Updates        []PackageUpdate `json:"updates"`
	SecurityCount  int             `json:"security_count"`
	ListsAge       string          `json:"lists_age,omitempty"`
	ListsUpdatedAt *time.Time      `json:"lists_updated_at,omitempty"`
	RebootRequired bool            `json:"reboot_required"`
	RebootPkgs     []string        `json:"reboot_required_pkgs,omitempty"`
	KernelUpdate   bool            `json:"kernel_update"`
	Removals       []string        `json:"removals,omitempty"`
	Note           string          `json:"note,omitempty"`
}

type PackageHistoryEntry struct {
	Start       time.Time `json:"start"`
	CommandLine string    `json:"command_line"`
	Requested   string    `json:"requested_by,omitempty"`
	Install     []string  `json:"install,omitempty"`
	Upgrade     []string  `json:"upgrade,omitempty"`
	Remove      []string  `json:"remove,omitempty"`
	Error       string    `json:"error,omitempty"`
}

type UpgradeRequest struct {
	Packages     []string `json:"packages"` // empty = all upgradable
	SecurityOnly bool     `json:"security_only"`
	DryRun       bool     `json:"dry_run"`
}

type CommandResult struct {
	Command  string `json:"command"`
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	Duration int64  `json:"duration_ms"`
}

// ---------------------------------------------------------------- security

type SSHConfig struct {
	Source   string            `json:"source"` // sshd -T | config-file
	Settings map[string]string `json:"settings"`
	Findings []Finding         `json:"findings"`
}

type Finding struct {
	ID       string `json:"id"`
	Severity string `json:"severity"` // info | low | medium | high | critical
	Title    string `json:"title"`
	Evidence string `json:"evidence"`
	Advice   string `json:"advice"`
}

type AuthEvent struct {
	Time    time.Time `json:"time"`
	Kind    string    `json:"kind"` // ssh_failed | ssh_invalid_user | ssh_accepted | sudo | session_open
	User    string    `json:"user,omitempty"`
	Source  string    `json:"source,omitempty"`
	Method  string    `json:"method,omitempty"`
	Command string    `json:"command,omitempty"`
	Raw     string    `json:"raw"`
}

type AuthSummary struct {
	Source       string         `json:"source"`
	Since        time.Time      `json:"since"`
	Events       []AuthEvent    `json:"events"`
	FailedBySrc  map[string]int `json:"failed_by_source"`
	FailedByUser map[string]int `json:"failed_by_user"`
	Accepted     int            `json:"accepted"`
	Failed       int            `json:"failed"`
	Sudo         int            `json:"sudo"`
	Note         string         `json:"note,omitempty"`
}

type SystemUser struct {
	Name     string   `json:"name"`
	UID      int      `json:"uid"`
	GID      int      `json:"gid"`
	Home     string   `json:"home"`
	Shell    string   `json:"shell"`
	Groups   []string `json:"groups,omitempty"`
	CanLogin bool     `json:"can_login"`
	Sudo     bool     `json:"sudo"`
	System   bool     `json:"system"`
}

type SecurityPosture struct {
	Findings  []Finding `json:"findings"`
	Tools     map[string]bool `json:"tools"` // fail2ban, auditd, aide, lynis, ufw, nft
	CheckedAt time.Time `json:"checked_at"`
}

// ---------------------------------------------------------------- probes

type ProbeRequest struct {
	Kind           string `json:"kind"` // tcp | http | tls
	Target         string `json:"target"`
	ExpectStatus   int    `json:"expect_status,omitempty"`
	TimeoutSec     int    `json:"timeout_sec,omitempty"`
	InsecureVerify bool   `json:"insecure_skip_verify,omitempty"`
}

type ProbeResult struct {
	OK         bool       `json:"ok"`
	LatencyMs  float64    `json:"latency_ms"`
	Status     int        `json:"status,omitempty"`
	Error      string     `json:"error,omitempty"`
	CertExpiry *time.Time `json:"cert_expiry,omitempty"`
	CertDays   *int       `json:"cert_days_left,omitempty"`
	CertSubject string    `json:"cert_subject,omitempty"`
	CertIssuer string     `json:"cert_issuer,omitempty"`
}

// ---------------------------------------------------------------- actions

type ActionResult struct {
	OK       bool          `json:"ok"`
	Action   string        `json:"action"`
	Target   string        `json:"target"`
	Message  string        `json:"message,omitempty"`
	Command  *CommandResult `json:"command,omitempty"`
	After    any           `json:"after,omitempty"` // state re-read after the action
}
