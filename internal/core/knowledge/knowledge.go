// Package knowledge is NEXUS's built-in library of Linux server runbooks.
// The documents are Markdown files embedded in the binary (docs/). They are
// split into sections at "## " headings and searched with BM25, so the AI can
// look up procedures offline, without an embedding model or the internet.
package knowledge

import (
	"bufio"
	"embed"
	"fmt"
	"io/fs"
	"math"
	"sort"
	"strings"
	"unicode"
)

//go:embed docs/*.md
var docsFS embed.FS

// Doc is one runbook.
type Doc struct {
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	Summary  string    `json:"summary"`
	Tags     []string  `json:"tags"`
	Sections []Section `json:"sections,omitempty"`
}

// Section is a searchable part of a runbook.
type Section struct {
	Heading string `json:"heading"`
	Body    string `json:"body"`
}

// Hit is a search result.
type Hit struct {
	DocID   string  `json:"doc"`
	Title   string  `json:"title"`
	Section string  `json:"section"`
	Snippet string  `json:"snippet"`
	Score   float64 `json:"score"`
}

type chunk struct {
	doc     *Doc
	section int
	tf      map[string]float64
	length  float64
}

// Library is an immutable, searchable set of runbooks.
type Library struct {
	docs   []*Doc
	byID   map[string]*Doc
	chunks []chunk
	df     map[string]int
	avgLen float64
}

// Load parses the embedded runbooks.
func Load() (*Library, error) {
	ents, err := fs.ReadDir(docsFS, "docs")
	if err != nil {
		return nil, err
	}
	lib := &Library{byID: map[string]*Doc{}, df: map[string]int{}}
	for _, e := range ents {
		b, err := docsFS.ReadFile("docs/" + e.Name())
		if err != nil {
			return nil, err
		}
		d, err := parse(strings.TrimSuffix(e.Name(), ".md"), string(b))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		lib.docs = append(lib.docs, d)
		lib.byID[d.ID] = d
	}
	sort.Slice(lib.docs, func(i, j int) bool { return lib.docs[i].Title < lib.docs[j].Title })
	var total float64
	for _, d := range lib.docs {
		for i, s := range d.Sections {
			tf := map[string]float64{}
			n := 0.0
			add := func(text string, w float64) {
				for _, t := range tokens(text) {
					tf[t] += w
					n += w
				}
			}
			add(d.Title, 2)
			add(strings.Join(d.Tags, " "), 2)
			add(s.Heading, 3)
			add(s.Body, 1)
			for t := range tf {
				lib.df[t]++
			}
			lib.chunks = append(lib.chunks, chunk{doc: d, section: i, tf: tf, length: n})
			total += n
		}
	}
	if len(lib.chunks) > 0 {
		lib.avgLen = total / float64(len(lib.chunks))
	}
	return lib, nil
}

// parse reads front matter lines ("# Title", "> summary", "Tags: a, b") and
// splits the body into sections at "## " headings.
func parse(id, text string) (*Doc, error) {
	d := &Doc{ID: id}
	var cur *Section
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	intro := &Section{Heading: "Overview"}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case d.Title == "" && strings.HasPrefix(line, "# "):
			d.Title = strings.TrimSpace(line[2:])
		case d.Summary == "" && strings.HasPrefix(line, "> ") && cur == nil:
			d.Summary = strings.TrimSpace(line[2:])
		case strings.HasPrefix(line, "Tags:") && cur == nil:
			for _, t := range strings.Split(line[5:], ",") {
				if t = strings.TrimSpace(t); t != "" {
					d.Tags = append(d.Tags, t)
				}
			}
		case strings.HasPrefix(line, "## "):
			d.Sections = append(d.Sections, Section{Heading: strings.TrimSpace(line[3:])})
			cur = &d.Sections[len(d.Sections)-1]
		default:
			if cur != nil {
				cur = &d.Sections[len(d.Sections)-1]
				cur.Body += line + "\n"
			} else {
				intro.Body += line + "\n"
			}
		}
	}
	if d.Title == "" {
		return nil, fmt.Errorf("missing '# Title'")
	}
	if strings.TrimSpace(intro.Body) != "" {
		d.Sections = append([]Section{*intro}, d.Sections...)
	}
	for i := range d.Sections {
		d.Sections[i].Body = strings.TrimSpace(d.Sections[i].Body)
	}
	if len(d.Sections) == 0 {
		return nil, fmt.Errorf("no sections")
	}
	return d, nil
}

var stop = map[string]bool{}

func init() {
	for _, w := range strings.Fields("a an and are as at be but by can do does did for from has have how i if in into is it its me my no not of on or our so than that the their then there these this to up use used using was we what when where which while who why will with you your too much many very more less most some any all just only also get gets got keep keeps kept after before cannot can't won't doesn't don't isn't should would could need needs want help please server servers") {
		stop[w] = true
	}
}

func tokens(s string) []string {
	f := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.')
	})
	out := f[:0]
	for _, t := range f {
		t = strings.Trim(t, ".-")
		if len(t) < 2 || stop[t] {
			continue
		}
		out = append(out, t)
		// "nginx.service" also matches "nginx"; "apt-get" matches "apt".
		if i := strings.IndexAny(t, ".-"); i > 1 {
			out = append(out, t[:i])
		}
	}
	return out
}

// Search ranks sections with BM25.
func (l *Library) Search(q string, max int) []Hit {
	if max <= 0 {
		max = 5
	}
	qt := tokens(q)
	if len(qt) == 0 {
		return nil
	}
	const k1, b = 1.4, 0.75
	n := float64(len(l.chunks))
	type scored struct {
		i int
		s float64
	}
	var res []scored
	for i, c := range l.chunks {
		s := 0.0
		for _, t := range qt {
			f := c.tf[t]
			if f == 0 {
				continue
			}
			idf := math.Log(1 + (n-float64(l.df[t])+0.5)/(float64(l.df[t])+0.5))
			s += idf * f * (k1 + 1) / (f + k1*(1-b+b*c.length/l.avgLen))
		}
		if s > 0 {
			res = append(res, scored{i, s})
		}
	}
	sort.Slice(res, func(i, j int) bool { return res[i].s > res[j].s })
	var hits []Hit
	perDoc := map[string]int{}
	for _, r := range res {
		c := l.chunks[r.i]
		if perDoc[c.doc.ID] >= 2 { // keep results varied
			continue
		}
		perDoc[c.doc.ID]++
		sec := c.doc.Sections[c.section]
		hits = append(hits, Hit{DocID: c.doc.ID, Title: c.doc.Title, Section: sec.Heading, Snippet: snippet(sec.Body, qt), Score: math.Round(r.s*100) / 100})
		if len(hits) == max {
			break
		}
	}
	return hits
}

// snippet returns the paragraph that matches the query best.
func snippet(body string, qt []string) string {
	best, bestN := "", -1
	for _, p := range strings.Split(body, "\n\n") {
		pl := strings.ToLower(p)
		n := 0
		for _, t := range qt {
			if strings.Contains(pl, t) {
				n++
			}
		}
		if n > bestN {
			best, bestN = p, n
		}
	}
	best = strings.TrimSpace(best)
	if len(best) > 600 {
		best = best[:600] + "…"
	}
	return best
}

// Docs lists the runbooks (without bodies).
func (l *Library) Docs() []Doc {
	out := make([]Doc, 0, len(l.docs))
	for _, d := range l.docs {
		c := *d
		c.Sections = nil
		out = append(out, c)
	}
	return out
}

// Get returns one runbook with all sections.
func (l *Library) Get(id string) (*Doc, bool) {
	d, ok := l.byID[id]
	return d, ok
}

// Read returns a runbook, or one section of it, as Markdown for the AI.
func (l *Library) Read(id, section string, maxChars int) (string, error) {
	d, ok := l.byID[id]
	if !ok {
		return "", fmt.Errorf("no runbook %q; use search_knowledge", id)
	}
	var b strings.Builder
	b.WriteString("# " + d.Title + "\n\n")
	found := false
	for _, s := range d.Sections {
		if section != "" && !strings.EqualFold(s.Heading, section) && !strings.Contains(strings.ToLower(s.Heading), strings.ToLower(section)) {
			continue
		}
		found = true
		b.WriteString("## " + s.Heading + "\n\n" + s.Body + "\n\n")
	}
	if !found {
		var hs []string
		for _, s := range d.Sections {
			hs = append(hs, s.Heading)
		}
		return "", fmt.Errorf("runbook %s has no section %q; sections: %s", id, section, strings.Join(hs, "; "))
	}
	out := b.String()
	if maxChars > 0 && len(out) > maxChars {
		out = out[:maxChars] + "\n\n… (truncated; read one section at a time)"
	}
	return out, nil
}

// Stats describes the library.
func (l *Library) Stats() (docs, sections int) { return len(l.docs), len(l.chunks) }
