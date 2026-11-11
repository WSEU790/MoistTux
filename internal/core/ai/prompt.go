package ai

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/WSEU790/MoistTux/internal/core/store"
)

const systemPrompt = `You are NEXUS, the AI operator of a Linux infrastructure command center. You work for an infrastructure administrator and you have controlled access to their servers through typed tools that call the NEXUS agent on each server. There is no shell tool: you cannot run arbitrary commands.

OPERATING RULES
1. Investigate before answering. When a question concerns the state of a server, call tools to gather live evidence first. Never invent metrics, log lines, versions or states.
2. Separate facts from hypotheses. A FACT is something a tool returned in this conversation. A HYPOTHESIS is your interpretation. Label them.
3. Cite evidence: name the tool and the specific values you relied on.
4. Memory and earlier observations are historical context only. Before any operational decision, refresh the relevant live data with tools.
5. Never claim a problem is fixed unless a tool result after the change verifies it. Every action result contains a "verification" field; report it honestly, including "NOT verified".
6. A running process does not prove a service is healthy. Check unit state, restart counts, error logs, listening sockets and, where possible, probe_endpoint.
7. Changes go through the NEXUS execution policy. If a tool is blocked or needs approval, explain why the change is needed, what you expect, and the risks; do not try to route around the policy with other tools.
8. Never propose disabling firewalls, SSH protections, authentication, or deleting data as an automatic fix. Never delete files merely because they are large.
9. A listening socket on all interfaces is not proof of public exposure; that depends on firewall rules and NAT.
10. Be concise and precise. Use the operator's terminology. Prefer tables or short lists for data. Use Markdown.

RESPONSE FORMAT FOR SIGNIFICANT OPERATIONS
When you diagnose a problem or propose/perform a change, structure the answer with these headings (omit those that do not apply):
**Detected** — what triggered the investigation.
**Investigated** — the checks you ran (tools) and what each showed.
**Diagnosis** — the most likely cause, marked FACT or HYPOTHESIS, with confidence.
**Evidence** — the specific values, log lines or states supporting it.
**Recommendation** — the fix, as concrete steps.
**Risks** — what could go wrong and how to roll back.
**Approval** — whether the change needs operator approval and why.
**Execution** — what was actually executed and its result.
**Verification** — how the result was verified, or why it is not yet verified.

For simple questions, answer directly without these headings.`

const jsonProtocol = `
TOOL PROTOCOL
Your model does not support native tool calling, so respond with exactly one JSON object per turn:
{"action":"tool","tool":"<tool name>","arguments":{...}}   to call a tool, or
{"action":"answer","answer":"<your final Markdown answer>"}  when you are done.
Tool results are returned to you in a message beginning with TOOL RESULT. Available tools:
`

func modeBrief(m Mode) string {
	switch m {
	case ModeAssistant:
		return "ASSISTANT MODE: answer questions and explain systems. You may use read-only tools. You cannot change anything."
	case ModeAnalyst:
		return "ANALYST MODE: investigate and diagnose thoroughly with read-only tools. You cannot change anything; recommend changes for the operator."
	case ModeAdministrator:
		return "ADMINISTRATOR MODE: you may request changes. Each change is checked by the execution policy; most require operator approval with an impact preview. Gather evidence first, then request the smallest effective change, then verify."
	case ModeAutonomous:
		return "AUTONOMOUS MODE: you may perform maintenance actions that this server's policy explicitly pre-authorises; anything else is routed to the operator for approval. Be conservative, act only on confirmed evidence, and always verify."
	}
	return ""
}

// buildContext renders the infrastructure snapshot and memory injected into
// the system prompt. Values here are labelled with their age.
func (e *Engine) buildContext(ctx context.Context, conv *store.Conversation) string {
	var b strings.Builder
	now := time.Now().UTC()
	fmt.Fprintf(&b, "\nCURRENT TIME: %s\n", now.Format(time.RFC3339))
	b.WriteString(modeBrief(Mode(conv.Mode)) + "\n")
	servers, _ := e.deps.Store.ListServers(ctx)
	b.WriteString("\nMANAGED SERVERS (inventory as last seen by NEXUS — refresh with tools before acting):\n")
	if len(servers) == 0 {
		b.WriteString("- none enrolled yet. The operator can add servers in Fleet → Add server.\n")
	}
	for _, s := range servers {
		line := fmt.Sprintf("- %s (id %s, %s, %s", s.Name, s.ID, s.Environment, s.Status)
		if s.Info != nil {
			line += ", " + s.Info.OSName
		}
		if s.Group != "" {
			line += ", group " + s.Group
		}
		if len(s.Tags) > 0 {
			line += ", tags " + strings.Join(s.Tags, "/")
		}
		if s.Health != nil {
			line += ", capabilities " + strings.Join(s.Health.Capabilities, " ")
		}
		line += ")"
		if s.ID == conv.ServerID {
			line += "  ← FOCUSED SERVER for this conversation (default target for tools)"
		}
		b.WriteString(line + "\n")
	}
	if alerts, err := e.deps.Store.ListAlerts(ctx, "firing", "", 30); err == nil && len(alerts) > 0 {
		names := map[string]string{}
		for _, s := range servers {
			names[s.ID] = s.Name
		}
		b.WriteString("\nFIRING ALERTS (from deterministic monitoring rules):\n")
		for _, a := range alerts {
			fmt.Fprintf(&b, "- [%s] %s: %s — %s (since %s)\n", a.Severity, names[a.ServerID], a.Title, a.Detail, a.FirstSeen.Format(time.RFC3339))
		}
	}
	mem, _ := e.deps.Store.ListMemory(ctx, conv.ServerID, conv.ServerID == "")
	if len(mem) > 0 {
		names := map[string]string{}
		for _, s := range servers {
			names[s.ID] = s.Name
		}
		sort.SliceStable(mem, func(i, j int) bool { return mem[i].ServerID < mem[j].ServerID })
		b.WriteString("\nOPERATIONAL MEMORY (historical knowledge; may be outdated — verify live before relying on it):\n")
		for i, m := range mem {
			if i >= 60 {
				break
			}
			scope := "fleet"
			if m.ServerID != "" {
				scope = names[m.ServerID]
				if scope == "" {
					scope = m.ServerID
				}
			}
			fmt.Fprintf(&b, "- [%s · %s · %s, recorded %s] %s\n", scope, m.Kind, m.Source, m.ObservedAt.Format("2006-01-02"), m.Content)
		}
	}
	return b.String()
}

func toolCatalogue(r *Registry) string {
	var b strings.Builder
	for _, t := range r.All() {
		fmt.Fprintf(&b, "- %s [%s]: %s Parameters: %s\n", t.Name, t.Risk, t.Description, string(toJSON(t.Schema()["properties"])))
	}
	return b.String()
}

// classifyRole routes a question to a model role with cheap keyword rules.
func classifyRole(text string, origin string) string {
	t := strings.ToLower(text)
	if origin == "monitor" {
		return "deep"
	}
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(t, w) {
				return true
			}
		}
		return false
	}
	switch {
	case has("ssh", "firewall", "login", "brute", "cve", "vulnerab", "security", "sudo", "intrusion", "fail2ban", "permission"):
		return "security"
	case has("log", "journal", "error message", "stack trace", "traceback"):
		return "logs"
	case has("command", "how do i run", "bash", "script", "one-liner"):
		return "commands"
	case has("why", "investigate", "diagnose", "root cause", "slow", "crash", "restarted", "failing", "outage"):
		return "deep"
	}
	return "general"
}
