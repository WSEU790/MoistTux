package ai

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/WSEU790/MoistTux/internal/protocol"
)

// Admin commands come from the shared catalog (internal/protocol/catalog.go):
// fixed argv templates with typed arguments, run as root through the agent's
// helper where needed. Shell commands are free-form, but every one needs a
// person's approval, and the server's root helper verifies NEXUS's signature
// over that exact command before running it.

func catalogArgs(raw map[string]any) map[string][]string {
	out := map[string][]string{}
	a, _ := raw["args"].(map[string]any)
	for k, v := range a {
		switch x := v.(type) {
		case string:
			if x != "" {
				out[k] = []string{x}
			}
		case float64:
			out[k] = []string{fmt.Sprint(int64(x))}
		case []any:
			for _, e := range x {
				out[k] = append(out[k], fmt.Sprint(e))
			}
		}
	}
	// "packages": "nginx curl" is a common way to write a list.
	for k, v := range out {
		if len(v) == 1 && strings.Contains(v[0], " ") {
			if c, ok := protocol.FindCommand(fmt.Sprint(raw["command"])); ok {
				for _, p := range c.Params {
					if p.Name == k && p.Multi {
						out[k] = strings.Fields(v[0])
					}
				}
			}
		}
	}
	return out
}

func catalogRisk(raw map[string]any) Risk {
	c, ok := protocol.FindCommand(fmt.Sprint(raw["command"]))
	if !ok {
		return RiskHigh // unknown: be strict
	}
	return Risk(c.Risk)
}

func describeCatalog(c *protocol.CatalogCommand) string {
	var ps []string
	for _, p := range c.Params {
		s := p.Name
		if p.Multi {
			s += "[]"
		}
		if p.Optional {
			s += "?"
		}
		ps = append(ps, s)
	}
	root := ""
	if c.Root {
		root = ", root"
	}
	return fmt.Sprintf("%s (%s%s; args: %s): %s", c.ID, c.Risk, root, strings.Join(ps, ", "), c.Description)
}

var reShellReason = regexp.MustCompile(`\S`)

func registerCommandTools(r *Registry) {
	r.Add(&Tool{
		Name: "list_admin_commands", Fleet: true, Risk: RiskRead,
		Description: "List the administration commands available to run_admin_command (id, risk, whether it runs as root, arguments, description). Filter by category or a search word.",
		Params: []Param{
			{Name: "category", Type: "string", Enum: []string{"packages", "services", "logs", "disk", "processes", "network", "firewall", "users", "time", "web", "containers", "system"}, Description: "Only this category"},
			{Name: "search", Type: "string", MaxLen: 64, Description: "Only commands whose id or description contains this word"},
		},
		Exec: func(_ context.Context, _ *Env, args map[string]any) (any, error) {
			cat, _ := args["category"].(string)
			q := strings.ToLower(fmt.Sprint(args["search"]))
			if args["search"] == nil {
				q = ""
			}
			var out []string
			for i := range protocol.Catalog {
				c := &protocol.Catalog[i]
				if cat != "" && c.Category != cat {
					continue
				}
				if q != "" && !strings.Contains(strings.ToLower(c.ID+" "+c.Description), q) {
					continue
				}
				out = append(out, "["+c.Category+"] "+describeCatalog(c))
			}
			return map[string]any{"commands": out, "count": len(out), "note": "Run one with run_admin_command {command: <id>, args: {<name>: <value or list>}}. Read-only commands run at once; changes follow the server's approval policy."}, nil
		},
	})

	r.Add(&Tool{
		Name: "run_admin_command", Risk: RiskMedium, Timeout: 32 * time.Minute,
		Description: "Run an administration command from the NEXUS catalog on a server, as root where the command needs it (e.g. apt_install, systemctl_enable, journal_vacuum_size, ufw_allow_from, nginx_test, df, ss_listen). Arguments are validated; nothing goes through a shell. Use list_admin_commands to see every command and its arguments. Read-only commands run immediately; changes follow the approval policy.",
		Params: []Param{
			{Name: "command", Type: "string", Required: true, Enum: protocol.CatalogIDs(), Description: "Catalog command id"},
			{Name: "args", Type: "object", Description: `Arguments by name, e.g. {"packages": ["nginx", "certbot"]} or {"unit": "nginx.service"}`},
		},
		RiskFor: catalogRisk,
		Check: func(args map[string]any) error {
			c, ok := protocol.FindCommand(fmt.Sprint(args["command"]))
			if !ok {
				return fmt.Errorf("unknown command %v; use list_admin_commands", args["command"])
			}
			_, err := c.Build(catalogArgs(args))
			return err
		},
		Describe: func(a map[string]any) string {
			c, _ := protocol.FindCommand(fmt.Sprint(a["command"]))
			if c == nil {
				return fmt.Sprint(a["command"])
			}
			argv, _ := c.Build(catalogArgs(a))
			return "run " + strings.Join(argv, " ")
		},
		Preview: func(ctx context.Context, env *Env, a map[string]any) (*Impact, error) {
			c, _ := protocol.FindCommand(fmt.Sprint(a["command"]))
			if c == nil {
				return nil, errors.New("unknown command")
			}
			argv, err := c.Build(catalogArgs(a))
			if err != nil {
				return nil, err
			}
			who := "as the agent's unprivileged user"
			if c.Root {
				who = "as root (through the agent's validated helper)"
			}
			imp := &Impact{
				Summary:  fmt.Sprintf("%s on %s", c.Description, env.Server.Name),
				Effects:  []string{"Runs exactly: " + strings.Join(argv, " "), "Runs " + who + fmt.Sprintf(", time limit %d s.", c.TimeoutSec)},
				Rollback: "Depends on the command. NEXUS records the full output in the execution history.",
			}
			if c.Warning != "" {
				imp.Risks = append(imp.Risks, c.Warning)
			}
			return imp, nil
		},
		Exec: func(ctx context.Context, env *Env, a map[string]any) (any, error) {
			c, _ := protocol.FindCommand(fmt.Sprint(a["command"]))
			if c == nil {
				return nil, errors.New("unknown command")
			}
			req := protocol.CatalogRunRequest{ID: c.ID, Args: catalogArgs(a)}
			var res protocol.CommandResult
			if err := env.Client.Post(ctx, "/v1/catalog/run", req, &res); err != nil {
				return nil, err
			}
			return commandOutput(&res), nil
		},
	})

	r.Add(&Tool{
		Name: "run_shell_command", Risk: RiskHigh, Shell: true, Timeout: 32 * time.Minute,
		Description: "Propose a shell command to run as root on a server when no other tool or catalog command (run_admin_command) can do the job. A person must approve every shell command; it never runs without that approval. Write one clear, non-interactive command (use -y flags; no editors or pagers), explain why in 'reason', and prefer read-only checks before changes. Commands that wipe disks, delete system directories or shut the server down are refused.",
		Params: []Param{
			{Name: "command", Type: "string", Required: true, MaxLen: 8000, Description: "The exact command, run with /bin/sh -c as root"},
			{Name: "reason", Type: "string", Required: true, MaxLen: 500, Description: "Why this command is needed and what it should change"},
			{Name: "timeout_sec", Type: "integer", Min: fptr(5), Max: fptr(1800), Default: 300, Description: "Time limit in seconds (default 300)"},
		},
		Check: func(a map[string]any) error {
			cmd, _ := a["command"].(string)
			if why := protocol.ShellBlocked(cmd); why != "" {
				return fmt.Errorf("refused: this command looks like %s; NEXUS never runs that", why)
			}
			if reason, _ := a["reason"].(string); !reShellReason.MatchString(reason) {
				return errors.New("give a reason for the command")
			}
			return nil
		},
		Describe: func(a map[string]any) string {
			cmd, _ := a["command"].(string)
			if len(cmd) > 120 {
				cmd = cmd[:117] + "…"
			}
			return "shell command as root: " + cmd
		},
		Preview: func(ctx context.Context, env *Env, a map[string]any) (*Impact, error) {
			cmd, _ := a["command"].(string)
			reason, _ := a["reason"].(string)
			warn := protocol.ShellWarnings(cmd)
			imp := &Impact{
				Summary: fmt.Sprintf("Run a shell command as root on %s", env.Server.Name),
				Effects: []string{
					"Command (exactly as it will run with /bin/sh -c):\n" + cmd,
					"Reason given: " + reason,
					fmt.Sprintf("Time limit: %v s. Output and exit code are recorded in the audit log.", a["timeout_sec"]),
				},
				Risks:    warn,
				Rollback: "NEXUS cannot undo shell commands automatically. Read the command carefully before approving.",
			}
			// Commands with warnings need the server name typed; plain ones
			// need only the approval click, since the command is shown in full.
			imp.NoConfirm = len(warn) == 0
			return imp, nil
		},
		Exec: func(ctx context.Context, env *Env, a map[string]any) (any, error) {
			if !env.HumanApproved {
				return nil, errors.New("shell commands run only after a person approves them")
			}
			cmd, _ := a["command"].(string)
			if why := protocol.ShellBlocked(cmd); why != "" {
				return nil, fmt.Errorf("refused: %s", why)
			}
			if env.Server.Info == nil || env.Server.Info.MachineID == "" {
				return nil, errors.New("the agent has not reported this server's machine-id yet; try again shortly")
			}
			timeout := 300
			if n, ok := a["timeout_sec"].(int); ok {
				timeout = n
			}
			nb := make([]byte, 16)
			if _, err := rand.Read(nb); err != nil {
				return nil, err
			}
			g := protocol.ShellGrant{V: 1, MachineID: env.Server.Info.MachineID, Command: cmd, Nonce: hex.EncodeToString(nb), Expires: time.Now().Add(2 * time.Minute).Unix(), TimeoutSec: timeout, ApprovedBy: "operator"}
			raw, err := json.Marshal(g)
			if err != nil {
				return nil, err
			}
			sig, err := env.Deps.Fleet.SignGrant(raw)
			if err != nil {
				return nil, err
			}
			var res protocol.CommandResult
			if err := env.Client.Post(ctx, "/v1/shell/run", protocol.ShellRunRequest{Token: base64.StdEncoding.EncodeToString(raw), Sig: sig}, &res); err != nil {
				return nil, err
			}
			return commandOutput(&res), nil
		},
	})
}

// commandOutput trims long output for the model while keeping both ends.
func commandOutput(res *protocol.CommandResult) map[string]any {
	trim := func(s string, n int) string {
		if len(s) <= n {
			return s
		}
		return s[:n/3] + "\n… [" + fmt.Sprint(len(s)-n) + " characters omitted] …\n" + s[len(s)-2*n/3:]
	}
	out := map[string]any{"command": res.Command, "exit_code": res.ExitCode, "duration_ms": res.Duration, "stdout": trim(res.Stdout, 6000), "stderr": trim(res.Stderr, 2000)}
	if res.ExitCode == 0 {
		out["status"] = "succeeded"
	} else {
		out["status"] = "failed"
	}
	return out
}

func registerKnowledgeTools(r *Registry) {
	r.Add(&Tool{
		Name: "search_knowledge", Fleet: true, Risk: RiskRead,
		Description: "Search NEXUS's built-in Linux server runbooks (APT, systemd, logs, disk, memory, CPU, networking, firewall, SSH, users, nginx, Apache, PHP-FPM, TLS, MySQL, PostgreSQL, Docker, backups, kernel updates, time sync, cron, mail, hardening, incident response, website-down triage). Use it before procedures and changes; then read_knowledge for the full section. Returns the best-matching sections with snippets.",
		Params: []Param{
			{Name: "query", Type: "string", Required: true, MaxLen: 200, Description: "What you need, e.g. 'nginx 502 bad gateway' or 'journal using too much disk'"},
			{Name: "max_results", Type: "integer", Min: fptr(1), Max: fptr(10), Default: 5, Description: "Number of results"},
		},
		Exec: func(_ context.Context, env *Env, a map[string]any) (any, error) {
			if env.Deps == nil || env.Deps.Knowledge == nil {
				return nil, errors.New("knowledge library unavailable")
			}
			n, _ := a["max_results"].(int)
			hits := env.Deps.Knowledge.Search(fmt.Sprint(a["query"]), n)
			return map[string]any{"results": hits, "note": "Read a section with read_knowledge {doc, section}. Runbooks are general guidance: confirm the live state of the server with tools before acting."}, nil
		},
	})
	r.Add(&Tool{
		Name: "read_knowledge", Fleet: true, Risk: RiskRead,
		Description: "Read a NEXUS runbook, or one section of it, found with search_knowledge.",
		Params: []Param{
			{Name: "doc", Type: "string", Required: true, MaxLen: 80, Description: "Runbook id from search_knowledge"},
			{Name: "section", Type: "string", MaxLen: 120, Description: "Section heading (optional; omit for the whole runbook)"},
		},
		Exec: func(_ context.Context, env *Env, a map[string]any) (any, error) {
			if env.Deps == nil || env.Deps.Knowledge == nil {
				return nil, errors.New("knowledge library unavailable")
			}
			sec, _ := a["section"].(string)
			text, err := env.Deps.Knowledge.Read(fmt.Sprint(a["doc"]), sec, 9000)
			if err != nil {
				return nil, err
			}
			return map[string]any{"runbook": text}, nil
		},
	})
}

func registerSiteTools(r *Registry) {
	r.Add(&Tool{
		Name: "forecast_disk_usage", Risk: RiskRead,
		Description: "Predict when each filesystem of a server will be full, from 14 days of hourly usage history: growth per day, days left, expected full date and how well the trend fits (R²). Use it when disk space is a concern, before clean-ups, and to decide if a disk must be extended.",
		Exec: func(ctx context.Context, env *Env, _ map[string]any) (any, error) {
			if env.Deps == nil || env.Deps.Forecast == nil {
				return nil, errors.New("forecasts unavailable")
			}
			fs, err := env.Deps.Forecast.Server(ctx, env.Server.ID)
			if err != nil {
				return nil, err
			}
			return map[string]any{"filesystems": fs, "note": "days_left is omitted when the filesystem is not growing or the history is too short or irregular for a reliable forecast."}, nil
		},
	})
	r.Add(&Tool{
		Name: "list_website_checks", Fleet: true, Risk: RiskRead,
		Description: "List the website checks NEXUS runs from the operator's PC (the outside view): URL, hosting server, state up/down, last status code and error, response time, uptime over 24 h and 7 days, and days until the TLS certificate expires.",
		Exec: func(ctx context.Context, env *Env, _ map[string]any) (any, error) {
			if env.Deps == nil || env.Deps.Sites == nil {
				return nil, errors.New("website checks unavailable")
			}
			sums, err := env.Deps.Sites.Summaries(ctx)
			if err != nil {
				return nil, err
			}
			var out []map[string]any
			for _, s := range sums {
				row := map[string]any{"name": s.Name, "url": s.URL, "server_id": s.ServerID, "state": s.State, "uptime_24h_pct": s.Uptime24h, "uptime_7d_pct": s.Uptime7d, "avg_response_ms_24h": s.AvgMs24h, "certificate_days_left": s.CertDays}
				if s.LastResult != nil {
					row["last_status"], row["last_error"], row["last_checked"] = s.LastResult.Status, s.LastResult.Error, s.LastResult.Time
				}
				out = append(out, row)
			}
			return map[string]any{"checks": out, "count": len(out)}, nil
		},
	})
}
