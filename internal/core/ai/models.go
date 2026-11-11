package ai

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/WSEU790/MoistTux/internal/core/ollama"
)

// Roles NEXUS can assign separate models to.
var Roles = []RoleInfo{
	{"general", "General infrastructure reasoning", "Conversation, explanations and planning. Needs tool calling."},
	{"deep", "Complex diagnostics", "Multi-step investigations of incidents; the largest model that fits."},
	{"logs", "Log analysis", "Reading and summarising journal and application logs."},
	{"security", "Security analysis", "SSH, authentication, firewall and posture reviews."},
	{"commands", "Linux command generation", "Explaining and drafting shell commands for the operator (never executed automatically)."},
	{"classify", "Lightweight classification", "Fast routing decisions; a small model is ideal."},
}

type RoleInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ModelInfo is a discovered model enriched with capabilities.
type ModelInfo struct {
	ollama.Model
	Caps       ollama.Capabilities `json:"capabilities"`
	ParamsB    float64             `json:"params_b"`
	CapsError  string              `json:"capabilities_error,omitempty"`
	FitsMemory *bool               `json:"fits_memory,omitempty"`
}

var reParams = regexp.MustCompile(`(?i)([\d.]+)\s*([BM])`)

func ParseParams(s string) float64 {
	m := reParams.FindStringSubmatch(s)
	if m == nil {
		return 0
	}
	v, _ := strconv.ParseFloat(m[1], 64)
	if strings.EqualFold(m[2], "M") {
		v /= 1000
	}
	return v
}

// Families with good tool-calling behaviour in Ollama (used as a tie-breaker only).
var toolFriendly = []string{"qwen3", "qwen2.5", "llama3.1", "llama3.2", "llama3.3", "mistral-nemo", "mistral-small", "granite3", "command-r", "hermes3", "firefunction", "gpt-oss", "devstral"}

// scoreModel ranks a model for a role given the memory budget in bytes.
func scoreModel(m ModelInfo, role string, budget uint64) float64 {
	if m.Caps.Embedding && !m.Caps.Completion {
		return -1
	}
	s := 0.0
	if m.Caps.Tools {
		s += 100
	} else if role != "classify" && role != "commands" {
		s -= 50
	}
	fits := budget == 0 || uint64(float64(m.Size)*1.2) <= budget
	if !fits {
		s -= 80
	}
	p := m.ParamsB
	switch role {
	case "classify":
		s += 20 - p // smaller is better
	case "deep":
		s += p * 2
	default:
		// Prefer the 7–14B sweet spot for interactive latency.
		if p >= 6 && p <= 15 {
			s += 20 + p
		} else {
			s += p
		}
	}
	name := strings.ToLower(m.Name)
	for _, f := range toolFriendly {
		if strings.HasPrefix(name, f) {
			s += 10
			break
		}
	}
	return s
}

// Resolution is the outcome of picking a model for a role.
type Resolution struct {
	Role      string `json:"role"`
	Model     string `json:"model"`
	Preferred string `json:"preferred,omitempty"`
	Automatic bool   `json:"automatic"`
	Notice    string `json:"notice,omitempty"`
	Tools     bool   `json:"tools"`
}

// Resolve picks the model for a role: the explicit assignment if installed,
// else the general assignment, else the best automatic choice — and always
// says when it had to deviate from the operator's preference.
func Resolve(role string, assigned map[string]string, models []ModelInfo, budget uint64) (Resolution, error) {
	if len(models) == 0 {
		return Resolution{}, fmt.Errorf("no models are installed in Ollama; open Settings → AI models to download one")
	}
	installed := map[string]ModelInfo{}
	for _, m := range models {
		installed[m.Name] = m
	}
	lookup := func(name string) (ModelInfo, bool) {
		if m, ok := installed[name]; ok {
			return m, true
		}
		if !strings.Contains(name, ":") {
			if m, ok := installed[name+":latest"]; ok {
				return m, true
			}
		}
		return ModelInfo{}, false
	}
	pref := assigned[role]
	if pref != "" {
		if m, ok := lookup(pref); ok {
			return Resolution{Role: role, Model: m.Name, Tools: m.Caps.Tools}, nil
		}
	}
	notice := ""
	if pref != "" {
		notice = fmt.Sprintf("The %s model %q is not installed in Ollama", role, pref)
	}
	if g := assigned["general"]; role != "general" && g != "" {
		if m, ok := lookup(g); ok {
			if notice != "" {
				notice += "; using the general model " + m.Name + " instead."
			}
			return Resolution{Role: role, Model: m.Name, Preferred: pref, Notice: notice, Tools: m.Caps.Tools}, nil
		}
	}
	ranked := append([]ModelInfo(nil), models...)
	sort.SliceStable(ranked, func(i, j int) bool { return scoreModel(ranked[i], role, budget) > scoreModel(ranked[j], role, budget) })
	best := ranked[0]
	if scoreModel(best, role, budget) < 0 {
		return Resolution{}, fmt.Errorf("no installed model can serve the %s role (embedding-only models cannot chat)", role)
	}
	if notice != "" {
		notice += "; automatically selected " + best.Name + "."
	}
	if !best.Caps.Tools {
		if notice != "" {
			notice += " "
		}
		notice += best.Name + " does not support native tool calling; NEXUS is using its structured JSON tool protocol, which is slower and less reliable. Install a tool-capable model (e.g. qwen2.5 or llama3.1) for best results."
	}
	return Resolution{Role: role, Model: best.Name, Preferred: pref, Automatic: true, Notice: notice, Tools: best.Caps.Tools}, nil
}

// ---------------------------------------------------------------- hardware

type GPU struct {
	Name      string `json:"name"`
	VRAMBytes uint64 `json:"vram_bytes"`
	Source    string `json:"source"`
}

type Hardware struct {
	CPUModel     string   `json:"cpu_model"`
	CPUThreads   int      `json:"cpu_threads"`
	RAMTotal     uint64   `json:"ram_total"`
	RAMAvailable uint64   `json:"ram_available"`
	GPUs         []GPU    `json:"gpus"`
	Notes        []string `json:"notes,omitempty"`
	OllamaLocal  bool     `json:"ollama_local"`
}

// Budget returns the memory available to hold a model.
func (h Hardware) Budget() uint64 {
	var vram uint64
	for _, g := range h.GPUs {
		if g.VRAMBytes > vram {
			vram = g.VRAMBytes
		}
	}
	if vram >= 4<<30 {
		return vram
	}
	// CPU inference: leave room for the OS and NEXUS itself.
	if h.RAMTotal > 0 {
		return uint64(float64(h.RAMTotal) * 0.6)
	}
	return 0
}

type Recommendation struct {
	Role     string `json:"role"`
	Model    string `json:"model"`
	Approx   string `json:"approx_download"`
	Why      string `json:"why"`
	Tools    bool   `json:"tools"`
	Installed bool  `json:"installed"`
}

// Recommend suggests Ollama library tags sized for this machine. Sizes are
// the approximate Q4_K_M download sizes published in the Ollama library.
func Recommend(h Hardware, installed []ModelInfo) []Recommendation {
	b := h.Budget()
	gib := float64(b) / (1 << 30)
	have := map[string]bool{}
	for _, m := range installed {
		have[m.Name] = true
	}
	var recs []Recommendation
	add := func(role, model, approx, why string) {
		recs = append(recs, Recommendation{Role: role, Model: model, Approx: approx, Why: why, Tools: true, Installed: have[model]})
	}
	gpu := len(h.GPUs) > 0 && h.Budget() == maxVRAM(h)
	where := "system RAM (CPU inference)"
	if gpu {
		where = "GPU memory"
	}
	switch {
	case gib >= 22:
		add("deep", "qwen2.5:32b", "≈20 GB", fmt.Sprintf("%.0f GiB of %s fits a 32B model at Q4 for thorough diagnostics", gib, where))
		add("general", "qwen2.5:14b", "≈9 GB", "fast, reliable tool calling for everyday conversation")
		add("classify", "qwen2.5:3b", "≈1.9 GB", "very fast routing and summaries")
	case gib >= 11:
		add("general", "qwen2.5:14b", "≈9 GB", fmt.Sprintf("%.0f GiB of %s fits a 14B model at Q4", gib, where))
		add("deep", "qwen2.5:14b", "≈9 GB", "the same model serves diagnostics well at this size")
		add("classify", "qwen2.5:3b", "≈1.9 GB", "fast routing")
	case gib >= 6:
		add("general", "qwen2.5:7b", "≈4.7 GB", fmt.Sprintf("%.0f GiB of %s fits a 7–8B model at Q4", gib, where))
		add("deep", "llama3.1:8b", "≈4.9 GB", "a second strong tool-calling model for investigations")
		add("classify", "qwen2.5:1.5b", "≈1 GB", "fast routing")
	case gib >= 2.5:
		add("general", "qwen2.5:3b", "≈1.9 GB", fmt.Sprintf("only %.1f GiB of %s is available; a 3B model keeps NEXUS responsive", gib, where))
		add("classify", "qwen2.5:1.5b", "≈1 GB", "fast routing")
	default:
		add("general", "qwen2.5:1.5b", "≈1 GB", "very limited memory; expect simpler answers")
	}
	if !gpu && gib >= 6 {
		recs[0].Why += ". Without a supported GPU, generation runs on the CPU and will be noticeably slower"
	}
	return recs
}

func maxVRAM(h Hardware) uint64 {
	var v uint64
	for _, g := range h.GPUs {
		if g.VRAMBytes > v {
			v = g.VRAMBytes
		}
	}
	return v
}
