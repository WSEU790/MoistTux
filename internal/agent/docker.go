//go:build linux

package agent

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/WSEU790/MoistTux/internal/protocol"
)

// Docker talks to the Engine API over its unix socket. No docker CLI is used.
type Docker struct {
	socket string
	hc     *http.Client
}

var reContainerRef = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.\-]{0,127}$`)

func NewDocker(socket string) *Docker {
	if socket == "" {
		socket = "/var/run/docker.sock"
	}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
		MaxIdleConns: 4, IdleConnTimeout: 30 * time.Second,
	}
	return &Docker{socket: socket, hc: &http.Client{Transport: tr}}
}

type dockerAPIError struct {
	Status  int
	Message string
}

func (e *dockerAPIError) Error() string { return fmt.Sprintf("docker API %d: %s", e.Status, e.Message) }

func (d *Docker) do(ctx context.Context, method, path string, q url.Values, out any) error {
	u := "http://docker" + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return err
	}
	resp, err := d.hc.Do(req)
	if err != nil {
		return fmt.Errorf("docker engine unreachable at %s: %w", d.socket, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var e struct{ Message string }
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		_ = json.Unmarshal(b, &e)
		if e.Message == "" {
			e.Message = strings.TrimSpace(string(b))
		}
		if resp.StatusCode == http.StatusNotModified {
			return nil // already in requested state
		}
		return &dockerAPIError{resp.StatusCode, e.Message}
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (d *Docker) Info(ctx context.Context) protocol.DockerInfo {
	if _, err := os.Stat(d.socket); err != nil {
		return protocol.DockerInfo{Unavailable: "Docker socket not found at " + d.socket}
	}
	var info struct {
		Containers, ContainersRunning, ContainersPaused, ContainersStopped, Images, NCPU int
		ServerVersion, Driver, DockerRootDir, CgroupDriver, OperatingSystem string
		MemTotal                                                           int64
		Warnings                                                           []string
	}
	if err := d.do(ctx, "GET", "/info", nil, &info); err != nil {
		return protocol.DockerInfo{Unavailable: err.Error()}
	}
	var ver struct{ APIVersion string }
	_ = d.do(ctx, "GET", "/version", nil, &ver)
	return protocol.DockerInfo{
		Available: true, ServerVersion: info.ServerVersion, APIVersion: ver.APIVersion,
		Containers: info.Containers, Running: info.ContainersRunning, Paused: info.ContainersPaused, Stopped: info.ContainersStopped,
		Images: info.Images, StorageDriver: info.Driver, DockerRootDir: info.DockerRootDir, CgroupDriver: info.CgroupDriver,
		OS: info.OperatingSystem, MemTotal: info.MemTotal, NCPU: info.NCPU, Warnings: info.Warnings,
	}
}

type apiContainer struct {
	ID      string `json:"Id"`
	Names   []string
	Image   string
	ImageID string
	Command string
	Created int64
	State   string
	Status  string
	Labels  map[string]string
	Ports   []struct {
		IP          string
		PrivatePort int
		PublicPort  int
		Type        string
	}
	NetworkSettings struct {
		Networks map[string]any
	}
	Mounts []struct {
		Type, Name, Source, Destination string
	}
}

func healthFromStatus(s string) string {
	switch {
	case strings.Contains(s, "(healthy)"):
		return "healthy"
	case strings.Contains(s, "(unhealthy)"):
		return "unhealthy"
	case strings.Contains(s, "(health: starting)"):
		return "starting"
	}
	return ""
}

func convContainer(c apiContainer) protocol.Container {
	out := protocol.Container{
		ID: c.ID[:12], Image: c.Image, ImageID: c.ImageID, Command: c.Command,
		Created: time.Unix(c.Created, 0).UTC(), State: c.State, Status: c.Status, Health: healthFromStatus(c.Status),
		Labels: c.Labels, ComposeProject: c.Labels["com.docker.compose.project"], ComposeService: c.Labels["com.docker.compose.service"],
	}
	if len(c.Names) > 0 {
		out.Name = strings.TrimPrefix(c.Names[0], "/")
	}
	for _, p := range c.Ports {
		out.Ports = append(out.Ports, protocol.ContainerPort{IP: p.IP, PrivatePort: p.PrivatePort, PublicPort: p.PublicPort, Type: p.Type})
	}
	for n := range c.NetworkSettings.Networks {
		out.Networks = append(out.Networks, n)
	}
	sort.Strings(out.Networks)
	for _, m := range c.Mounts {
		src := m.Source
		if m.Type == "volume" {
			src = m.Name
		}
		out.Mounts = append(out.Mounts, fmt.Sprintf("%s:%s (%s)", src, m.Destination, m.Type))
	}
	return out
}

func (d *Docker) Containers(ctx context.Context) ([]protocol.Container, error) {
	var list []apiContainer
	if err := d.do(ctx, "GET", "/containers/json", url.Values{"all": {"1"}}, &list); err != nil {
		return nil, err
	}
	out := make([]protocol.Container, 0, len(list))
	for _, c := range list {
		out = append(out, convContainer(c))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (d *Docker) Inspect(ctx context.Context, ref string, withStats bool) (*protocol.ContainerDetail, error) {
	if !reContainerRef.MatchString(ref) {
		return nil, fmt.Errorf("invalid container reference %q", ref)
	}
	var c struct {
		ID           string `json:"Id"`
		Name         string
		Created      string
		RestartCount int
		Image        string
		Path         string
		Args         []string
		State        struct {
			Status     string
			ExitCode   int
			OOMKilled  bool
			Error      string
			StartedAt  string
			FinishedAt string
			Health     *struct {
				Status string
				Log    []struct {
					ExitCode int
					Output   string
					End      string
				}
			}
		}
		HostConfig struct {
			RestartPolicy struct{ Name string }
		}
		Config struct {
			Image  string
			Env    []string
			Labels map[string]string
		}
		NetworkSettings struct {
			Networks map[string]any
			Ports    map[string][]struct{ HostIP, HostPort string }
		}
		Mounts []struct{ Type, Name, Source, Destination string }
	}
	if err := d.do(ctx, "GET", "/containers/"+ref+"/json", nil, &c); err != nil {
		return nil, err
	}
	det := &protocol.ContainerDetail{}
	det.ID = c.ID[:12]
	det.Name = strings.TrimPrefix(c.Name, "/")
	det.Image = c.Config.Image
	det.ImageID = c.Image
	det.Command = strings.TrimSpace(c.Path + " " + strings.Join(c.Args, " "))
	if t, err := time.Parse(time.RFC3339Nano, c.Created); err == nil {
		det.Created = t.UTC()
	}
	det.State = c.State.Status
	det.Labels = c.Config.Labels
	det.ComposeProject = c.Config.Labels["com.docker.compose.project"]
	det.ComposeService = c.Config.Labels["com.docker.compose.service"]
	det.RestartCount = c.RestartCount
	det.RestartPolicy = c.HostConfig.RestartPolicy.Name
	det.ExitCode = c.State.ExitCode
	det.OOMKilled = c.State.OOMKilled
	det.Error = c.State.Error
	if t, err := time.Parse(time.RFC3339Nano, c.State.StartedAt); err == nil && t.Year() > 1 {
		u := t.UTC()
		det.StartedAt = &u
	}
	if t, err := time.Parse(time.RFC3339Nano, c.State.FinishedAt); err == nil && t.Year() > 1 {
		u := t.UTC()
		det.FinishedAt = &u
	}
	if c.State.Health != nil {
		det.Health = c.State.Health.Status
		for _, l := range c.State.Health.Log {
			det.HealthLog = append(det.HealthLog, fmt.Sprintf("%s exit=%d %s", l.End, l.ExitCode, strings.TrimSpace(l.Output)))
		}
	}
	for _, e := range c.Config.Env {
		k, _, _ := strings.Cut(e, "=")
		det.Env = append(det.Env, k) // values are never returned
	}
	for n := range c.NetworkSettings.Networks {
		det.Networks = append(det.Networks, n)
	}
	sort.Strings(det.Networks)
	for port, binds := range c.NetworkSettings.Ports {
		pp, proto, _ := strings.Cut(port, "/")
		priv, _ := strconv.Atoi(pp)
		if len(binds) == 0 {
			det.Ports = append(det.Ports, protocol.ContainerPort{PrivatePort: priv, Type: proto})
		}
		for _, b := range binds {
			pub, _ := strconv.Atoi(b.HostPort)
			det.Ports = append(det.Ports, protocol.ContainerPort{IP: b.HostIP, PrivatePort: priv, PublicPort: pub, Type: proto})
		}
	}
	for _, m := range c.Mounts {
		src := m.Source
		if m.Type == "volume" {
			src = m.Name
		}
		det.Mounts = append(det.Mounts, fmt.Sprintf("%s:%s (%s)", src, m.Destination, m.Type))
	}
	if withStats && det.State == "running" {
		if st, err := d.Stats(ctx, ref); err == nil {
			det.Stats = st
		}
	}
	return det, nil
}

func (d *Docker) Stats(ctx context.Context, ref string) (*protocol.ContainerStats, error) {
	type cpu struct {
		CPUUsage struct {
			TotalUsage uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		SystemCPUUsage uint64 `json:"system_cpu_usage"`
		OnlineCPUs     int    `json:"online_cpus"`
	}
	var s struct {
		CPUStats    cpu `json:"cpu_stats"`
		PreCPUStats cpu `json:"precpu_stats"`
		MemoryStats struct {
			Usage uint64            `json:"usage"`
			Limit uint64            `json:"limit"`
			Stats map[string]uint64 `json:"stats"`
		} `json:"memory_stats"`
		Networks map[string]struct {
			RxBytes uint64 `json:"rx_bytes"`
			TxBytes uint64 `json:"tx_bytes"`
		} `json:"networks"`
		BlkioStats struct {
			IoServiceBytesRecursive []struct {
				Op    string `json:"op"`
				Value uint64 `json:"value"`
			} `json:"io_service_bytes_recursive"`
		} `json:"blkio_stats"`
		PidsStats struct {
			Current uint64 `json:"current"`
		} `json:"pids_stats"`
	}
	// stream=false makes the engine take two readings ~1 s apart, so
	// precpu_stats is populated and the CPU percentage is a real measurement.
	if err := d.do(ctx, "GET", "/containers/"+ref+"/stats", url.Values{"stream": {"false"}}, &s); err != nil {
		return nil, err
	}
	out := &protocol.ContainerStats{MemLimit: s.MemoryStats.Limit, PIDs: s.PidsStats.Current}
	cd := float64(s.CPUStats.CPUUsage.TotalUsage) - float64(s.PreCPUStats.CPUUsage.TotalUsage)
	sd := float64(s.CPUStats.SystemCPUUsage) - float64(s.PreCPUStats.SystemCPUUsage)
	if sd > 0 && cd >= 0 {
		n := s.CPUStats.OnlineCPUs
		if n == 0 {
			n = 1
		}
		out.CPUPct = float64(int64(cd/sd*float64(n)*10000)) / 100
	}
	mem := s.MemoryStats.Usage
	if v, ok := s.MemoryStats.Stats["inactive_file"]; ok && v < mem { // cgroup v2, matches docker CLI
		mem -= v
	} else if v, ok := s.MemoryStats.Stats["total_inactive_file"]; ok && v < mem {
		mem -= v
	}
	out.MemUsage = mem
	if out.MemLimit > 0 {
		out.MemPct = float64(int64(float64(mem)/float64(out.MemLimit)*10000)) / 100
	}
	for _, n := range s.Networks {
		out.NetRx += n.RxBytes
		out.NetTx += n.TxBytes
	}
	for _, b := range s.BlkioStats.IoServiceBytesRecursive {
		switch strings.ToLower(b.Op) {
		case "read":
			out.BlockRead += b.Value
		case "write":
			out.BlockWrite += b.Value
		}
	}
	return out, nil
}

var containerActions = map[string]string{
	"start": "/start", "stop": "/stop", "restart": "/restart", "pause": "/pause", "unpause": "/unpause",
}

func (d *Docker) Action(ctx context.Context, ref, action string) error {
	if !reContainerRef.MatchString(ref) {
		return fmt.Errorf("invalid container reference %q", ref)
	}
	suffix, ok := containerActions[action]
	if !ok {
		return fmt.Errorf("unsupported container action %q", action)
	}
	q := url.Values{}
	if action == "stop" || action == "restart" {
		q.Set("t", "20")
	}
	return d.do(ctx, "POST", "/containers/"+ref+suffix, q, nil)
}

// Logs returns the last lines of a container's stdout/stderr. Docker
// multiplexes streams with 8-byte frame headers unless the container has a TTY.
func (d *Docker) Logs(ctx context.Context, ref string, tail int, since string) ([]protocol.LogEntry, error) {
	if !reContainerRef.MatchString(ref) {
		return nil, fmt.Errorf("invalid container reference %q", ref)
	}
	if tail <= 0 || tail > 5000 {
		tail = 300
	}
	var cfg struct{ Config struct{ Tty bool } }
	if err := d.do(ctx, "GET", "/containers/"+ref+"/json", nil, &cfg); err != nil {
		return nil, err
	}
	q := url.Values{"stdout": {"1"}, "stderr": {"1"}, "timestamps": {"1"}, "tail": {strconv.Itoa(tail)}}
	if since != "" {
		if t, err := parseWhen(since); err == nil && !t.IsZero() {
			q.Set("since", strconv.FormatInt(t.Unix(), 10))
		}
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://docker/containers/"+ref+"/logs?"+q.Encode(), nil)
	resp, err := d.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, &dockerAPIError{resp.StatusCode, strings.TrimSpace(string(b))}
	}
	body := io.LimitReader(resp.Body, 16<<20)
	var out []protocol.LogEntry
	add := func(line string, stderr bool) {
		ts, msg, _ := strings.Cut(line, " ")
		e := protocol.LogEntry{Message: strings.TrimRight(msg, "\r"), Ident: "stdout", Priority: 6}
		if stderr {
			e.Ident, e.Priority = "stderr", guessPriority(msg)
			if e.Priority == 6 {
				e.Priority = 5
			}
		} else {
			e.Priority = guessPriority(msg)
		}
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			e.Time = t.UTC()
		} else {
			e.Message = line
		}
		out = append(out, e)
	}
	if cfg.Config.Tty {
		sc := bufio.NewScanner(body)
		sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
		for sc.Scan() {
			add(sc.Text(), false)
		}
		return out, nil
	}
	hdr := make([]byte, 8)
	for {
		if _, err := io.ReadFull(body, hdr); err != nil {
			break
		}
		n := binary.BigEndian.Uint32(hdr[4:])
		buf := make([]byte, n)
		if _, err := io.ReadFull(body, buf); err != nil {
			break
		}
		for _, l := range strings.Split(strings.TrimRight(string(buf), "\n"), "\n") {
			add(l, hdr[0] == 2)
		}
	}
	return out, nil
}

func (d *Docker) Images(ctx context.Context) ([]protocol.DockerImage, error) {
	var list []struct {
		ID         string `json:"Id"`
		RepoTags   []string
		Size       int64
		Created    int64
		Containers int
	}
	if err := d.do(ctx, "GET", "/images/json", nil, &list); err != nil {
		return nil, err
	}
	// Containers is -1 unless computed; count from container list instead.
	use := map[string]int{}
	if cs, err := d.Containers(ctx); err == nil {
		for _, c := range cs {
			use[c.ImageID]++
		}
	}
	out := make([]protocol.DockerImage, 0, len(list))
	for _, i := range list {
		tags := []string{}
		for _, t := range i.RepoTags {
			if t != "<none>:<none>" {
				tags = append(tags, t)
			}
		}
		out = append(out, protocol.DockerImage{ID: strings.TrimPrefix(i.ID, "sha256:")[:12], Tags: tags, Size: i.Size, Created: time.Unix(i.Created, 0).UTC(), InUse: use[i.ID], Dangling: len(tags) == 0})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Size > out[j].Size })
	return out, nil
}

func (d *Docker) Volumes(ctx context.Context) ([]protocol.DockerVolume, error) {
	var res struct {
		Volumes []struct {
			Name, Driver, Mountpoint, CreatedAt string
			Labels                              map[string]string
		}
	}
	if err := d.do(ctx, "GET", "/volumes", nil, &res); err != nil {
		return nil, err
	}
	out := make([]protocol.DockerVolume, 0, len(res.Volumes))
	for _, v := range res.Volumes {
		out = append(out, protocol.DockerVolume{Name: v.Name, Driver: v.Driver, Mountpoint: v.Mountpoint, Labels: v.Labels, Created: v.CreatedAt})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (d *Docker) Networks(ctx context.Context) ([]protocol.DockerNetwork, error) {
	var list []struct {
		ID       string `json:"Id"`
		Name     string
		Driver   string
		Scope    string
		Internal bool
		IPAM     struct{ Config []struct{ Subnet string } }
	}
	if err := d.do(ctx, "GET", "/networks", nil, &list); err != nil {
		return nil, err
	}
	members := map[string][]string{}
	if cs, err := d.Containers(ctx); err == nil {
		for _, c := range cs {
			for _, n := range c.Networks {
				members[n] = append(members[n], c.Name)
			}
		}
	}
	out := make([]protocol.DockerNetwork, 0, len(list))
	for _, n := range list {
		dn := protocol.DockerNetwork{ID: n.ID[:12], Name: n.Name, Driver: n.Driver, Scope: n.Scope, Internal: n.Internal, Containers: members[n.Name]}
		for _, c := range n.IPAM.Config {
			if c.Subnet != "" {
				dn.Subnets = append(dn.Subnets, c.Subnet)
			}
		}
		out = append(out, dn)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ComposeProjects groups containers by compose labels.
func (d *Docker) ComposeProjects(ctx context.Context) ([]protocol.ComposeProject, error) {
	cs, err := d.Containers(ctx)
	if err != nil {
		return nil, err
	}
	m := map[string]*protocol.ComposeProject{}
	for _, c := range cs {
		if c.ComposeProject == "" {
			continue
		}
		p, ok := m[c.ComposeProject]
		if !ok {
			p = &protocol.ComposeProject{Name: c.ComposeProject, WorkingDir: c.Labels["com.docker.compose.project.working_dir"]}
			if cf := c.Labels["com.docker.compose.project.config_files"]; cf != "" {
				p.ConfigFiles = strings.Split(cf, ",")
			}
			m[c.ComposeProject] = p
		}
		p.Containers++
		if c.State == "running" {
			p.Running++
		}
		found := false
		for _, s := range p.Services {
			if s == c.ComposeService {
				found = true
			}
		}
		if !found && c.ComposeService != "" {
			p.Services = append(p.Services, c.ComposeService)
		}
	}
	out := make([]protocol.ComposeProject, 0, len(m))
	for _, p := range m {
		sort.Strings(p.Services)
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
