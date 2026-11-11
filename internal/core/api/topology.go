package api

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/WSEU790/MoistTux/internal/protocol"
)

// Topology is generated from discovered inventory only. Relationships that
// NEXUS has not observed (for example proxy upstreams before configuration
// parsing exists) are not drawn.
type topoNode struct {
	ID       string         `json:"id"`
	Kind     string         `json:"kind"` // desktop | core | ollama | server | docker | container | service | proxy | database
	Label    string         `json:"label"`
	Sub      string         `json:"sub,omitempty"`
	Status   string         `json:"status"` // ok | warning | critical | offline | unknown
	ServerID string         `json:"server_id,omitempty"`
	Group    string         `json:"group,omitempty"`
	Meta     map[string]any `json:"meta,omitempty"`
}

type topoEdge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Kind  string `json:"kind"` // control | agent | hosts | runs | network | listens
	Label string `json:"label,omitempty"`
}

var proxyProcs = map[string]bool{"nginx": true, "apache2": true, "httpd": true, "caddy": true, "traefik": true, "haproxy": true, "envoy": true}
var dbProcs = map[string]string{"postgres": "PostgreSQL", "postgresql": "PostgreSQL", "mysqld": "MySQL", "mariadbd": "MariaDB", "redis-server": "Redis", "mongod": "MongoDB", "memcached": "Memcached", "clickhouse-serv": "ClickHouse"}
var proxyImages = []string{"nginx", "traefik", "caddy", "haproxy", "envoy", "httpd"}
var dbImages = map[string]string{"postgres": "PostgreSQL", "mysql": "MySQL", "mariadb": "MariaDB", "redis": "Redis", "mongo": "MongoDB", "valkey": "Valkey", "timescale": "TimescaleDB"}

func imageBase(img string) string {
	i := img
	if k := strings.LastIndex(i, "/"); k >= 0 {
		i = i[k+1:]
	}
	if k := strings.IndexAny(i, ":@"); k >= 0 {
		i = i[:k]
	}
	return strings.ToLower(i)
}

func (s *Server) topology(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var nodes []topoNode
	var edges []topoEdge
	st := s.AI.Status(ctx)
	ollamaStatus := "critical"
	if st.Reachable {
		ollamaStatus = "ok"
	}
	nodes = append(nodes,
		topoNode{ID: "desktop", Kind: "desktop", Label: "NEXUS Desktop", Sub: "operator console", Status: "ok"},
		topoNode{ID: "core", Kind: "core", Label: "NEXUS AI Core", Sub: "policy · monitoring · tools", Status: "ok"},
		topoNode{ID: "ollama", Kind: "ollama", Label: "Ollama", Sub: st.URL, Status: ollamaStatus, Meta: map[string]any{"version": st.Version, "models": st.Models, "error": st.Error}},
	)
	edges = append(edges, topoEdge{"desktop", "core", "control", ""}, topoEdge{"core", "ollama", "control", "local inference"})

	servers, err := s.Store.ListServers(ctx)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	for _, srv := range servers {
		sid := "srv:" + srv.ID
		alerts, _ := s.Store.FiringForServer(ctx, srv.ID)
		status := "ok"
		switch srv.Status {
		case "offline", "untrusted":
			status = "offline"
		case "online", "degraded":
			for _, a := range alerts {
				if a.Severity == "critical" {
					status = "critical"
				} else if a.Severity == "warning" && status != "critical" {
					status = "warning"
				}
			}
			if srv.Status == "degraded" && status == "ok" {
				status = "warning"
			}
		default:
			status = "unknown"
		}
		sub := srv.Address
		if srv.Info != nil {
			sub = srv.Info.OSName
		}
		nodes = append(nodes, topoNode{ID: sid, Kind: "server", Label: srv.Name, Sub: sub, Status: status, ServerID: srv.ID, Group: srv.Group, Meta: map[string]any{"environment": srv.Environment, "alerts": len(alerts), "address": srv.Address}})
		edges = append(edges, topoEdge{"core", sid, "agent", "mTLS :" + fmt.Sprint(srv.Port)})

		alertFor := func(rule, subject string) string {
			for _, a := range alerts {
				if a.Rule == rule && (subject == "" || a.Subject == subject) {
					return map[string]string{"critical": "critical", "warning": "warning"}[a.Severity]
				}
			}
			return ""
		}

		// Host listeners → services, proxies and databases.
		var netv protocol.NetworkOverview
		seenProc := map[string]bool{}
		if _, err := s.Store.GetInventory(ctx, srv.ID, "network", &netv); err == nil {
			byProc := map[string][]protocol.Socket{}
			for _, sock := range netv.Listening {
				if sock.Process == "" || sock.Process == "docker-proxy" || sock.Process == "containerd" || sock.Process == "nexus-agent" {
					continue
				}
				byProc[sock.Process] = append(byProc[sock.Process], sock)
			}
			names := make([]string, 0, len(byProc))
			for n := range byProc {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, p := range names {
				socks := byProc[p]
				kind, label := "service", p
				if proxyProcs[p] {
					kind = "proxy"
				}
				if db, ok := dbProcs[p]; ok {
					kind, label = "database", db
				}
				var ports []string
				public := false
				for _, so := range socks {
					ports = append(ports, fmt.Sprintf("%s/%d", so.Proto, so.LocalPort))
					if so.Scope != "loopback" {
						public = true
					}
				}
				scope := "loopback only"
				if public {
					scope = "bound beyond loopback"
				}
				id := sid + ":p:" + p
				seenProc[p] = true
				nodes = append(nodes, topoNode{ID: id, Kind: kind, Label: label, Sub: strings.Join(uniq(ports), " "), Status: "ok", ServerID: srv.ID, Meta: map[string]any{"process": p, "scope": scope}})
				edges = append(edges, topoEdge{sid, id, "listens", scope})
			}
		}

		// Failed systemd services are shown even without sockets.
		var sl protocol.ServiceList
		if _, err := s.Store.GetInventory(ctx, srv.ID, "services", &sl); err == nil {
			for _, svc := range sl.Services {
				if svc.ActiveState != "failed" {
					continue
				}
				id := sid + ":u:" + svc.Unit
				nodes = append(nodes, topoNode{ID: id, Kind: "service", Label: strings.TrimSuffix(svc.Unit, ".service"), Sub: "failed", Status: "critical", ServerID: srv.ID, Meta: map[string]any{"unit": svc.Unit}})
				edges = append(edges, topoEdge{sid, id, "runs", "systemd"})
			}
		}

		// Docker engine and containers.
		var cs []protocol.Container
		if _, err := s.Store.GetInventory(ctx, srv.ID, "containers", &cs); err == nil {
			did := sid + ":docker"
			nodes = append(nodes, topoNode{ID: did, Kind: "docker", Label: "Docker", Sub: fmt.Sprintf("%d containers", len(cs)), Status: "ok", ServerID: srv.ID})
			edges = append(edges, topoEdge{sid, did, "runs", "engine"})
			netMembers := map[string][]string{}
			for _, c := range cs {
				id := sid + ":c:" + c.Name
				kind := "container"
				base := imageBase(c.Image)
				for _, p := range proxyImages {
					if base == p {
						kind = "proxy"
					}
				}
				if db, ok := dbImages[base]; ok {
					kind = "database"
					_ = db
				}
				cst := "ok"
				switch {
				case c.State == "exited" || c.Health == "unhealthy":
					cst = "critical"
				case c.State == "restarting" || c.State == "paused" || c.Health == "starting":
					cst = "warning"
				case c.State != "running":
					cst = "unknown"
				}
				if a := alertFor("container_unhealthy", c.Name); a != "" {
					cst = a
				}
				var ports []string
				for _, p := range c.Ports {
					if p.PublicPort > 0 {
						ports = append(ports, fmt.Sprintf("%d→%d", p.PublicPort, p.PrivatePort))
					}
				}
				sub := c.Image
				if len(ports) > 0 {
					sub += " · " + strings.Join(uniq(ports), " ")
				}
				nodes = append(nodes, topoNode{ID: id, Kind: kind, Label: c.Name, Sub: sub, Status: cst, ServerID: srv.ID, Group: c.ComposeProject, Meta: map[string]any{"container": c.Name, "image": c.Image, "state": c.State, "health": c.Health, "compose": c.ComposeProject}})
				edges = append(edges, topoEdge{did, id, "runs", ""})
				for _, n := range c.Networks {
					if n != "bridge" && n != "host" && n != "none" {
						netMembers[n] = append(netMembers[n], id)
					}
				}
			}
			// Containers sharing a user-defined network can reach each other.
			for n, members := range netMembers {
				sort.Strings(members)
				for i := 1; i < len(members); i++ {
					edges = append(edges, topoEdge{members[0], members[i], "network", n})
				}
			}
		}
		_ = seenProc
	}
	if nodes == nil {
		nodes = []topoNode{}
	}
	if edges == nil {
		edges = []topoEdge{}
	}
	writeJSON(w, 200, map[string]any{"nodes": nodes, "edges": edges, "note": "Generated from discovered inventory. Reverse-proxy upstream routes are not drawn until configuration analysis is available."})
}

func uniq(s []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range s {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}
