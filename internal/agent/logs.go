//go:build linux

package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/WSEU790/MoistTux/internal/protocol"
)

var reRelative = regexp.MustCompile(`^-(\d{1,5})([smhd])$`)

// parseWhen accepts RFC3339 or a relative offset like "-24h" / "-7d".
func parseWhen(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if m := reRelative.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1])
		unit := map[string]time.Duration{"s": time.Second, "m": time.Minute, "h": time.Hour, "d": 24 * time.Hour}[m[2]]
		return time.Now().Add(-time.Duration(n) * unit), nil
	}
	return time.Parse(time.RFC3339, s)
}

func journalAvailable() bool {
	if _, err := lookPath("journalctl"); err != nil {
		return false
	}
	for _, d := range []string{"/var/log/journal", "/run/log/journal"} {
		if entries, err := os.ReadDir(d); err == nil && len(entries) > 0 {
			return true
		}
	}
	return false
}

// QueryLogs reads from journald when present, otherwise from an allowlisted
// file under /var/log.
func QueryLogs(ctx context.Context, priv *Privileged, q protocol.LogQuery) (protocol.LogResult, error) {
	if q.Limit <= 0 || q.Limit > 5000 {
		q.Limit = 500
	}
	if q.Unit != "" && !reUnit.MatchString(q.Unit) && !reUnit.MatchString(q.Unit+".service") {
		return protocol.LogResult{}, fmt.Errorf("invalid unit %q", q.Unit)
	}
	if q.Priority < 0 || q.Priority > 7 {
		return protocol.LogResult{}, fmt.Errorf("priority must be 0..7")
	}
	var grep *regexp.Regexp
	if q.Grep != "" {
		if len(q.Grep) > 200 {
			return protocol.LogResult{}, fmt.Errorf("grep pattern too long")
		}
		var err error
		if grep, err = regexp.Compile("(?i)" + q.Grep); err != nil {
			grep = regexp.MustCompile("(?i)" + regexp.QuoteMeta(q.Grep))
		}
	}
	since, err := parseWhen(q.Since)
	if err != nil {
		return protocol.LogResult{}, fmt.Errorf("since: %w", err)
	}
	until, err := parseWhen(q.Until)
	if err != nil {
		return protocol.LogResult{}, fmt.Errorf("until: %w", err)
	}
	if q.File == "" && journalAvailable() {
		return queryJournal(ctx, q, since, until, grep)
	}
	return queryFile(ctx, priv, q, since, until, grep)
}

func queryJournal(ctx context.Context, q protocol.LogQuery, since, until time.Time, grep *regexp.Regexp) (protocol.LogResult, error) {
	args := []string{"--no-pager", "-o", "json", "-r", "-n", strconv.Itoa(q.Limit * 4)}
	if q.Unit != "" {
		args = append(args, "-u", q.Unit)
	}
	if !since.IsZero() {
		args = append(args, "--since", since.Local().Format("2006-01-02 15:04:05"))
	}
	if !until.IsZero() {
		args = append(args, "--until", until.Local().Format("2006-01-02 15:04:05"))
	}
	if q.Priority > 0 {
		args = append(args, "-p", strconv.Itoa(q.Priority))
	}
	r := runCmd(ctx, 30*time.Second, nil, "journalctl", args...)
	if r.ExitCode != 0 && r.Stdout == "" {
		return protocol.LogResult{}, fmt.Errorf("journalctl: %s", firstLine(r.Stderr))
	}
	res := protocol.LogResult{Source: "journald", Entries: []protocol.LogEntry{}}
	sc := bufio.NewScanner(strings.NewReader(r.Stdout))
	sc.Buffer(make([]byte, 256*1024), 8*1024*1024)
	for sc.Scan() {
		var m map[string]any
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		e := protocol.LogEntry{Message: journalString(m["MESSAGE"])}
		if us, err := strconv.ParseInt(journalString(m["__REALTIME_TIMESTAMP"]), 10, 64); err == nil {
			e.Time = time.UnixMicro(us).UTC()
		}
		e.Unit = journalString(m["_SYSTEMD_UNIT"])
		e.Ident = journalString(m["SYSLOG_IDENTIFIER"])
		e.PID, _ = strconv.Atoi(journalString(m["_PID"]))
		e.Priority = 6
		if p, err := strconv.Atoi(journalString(m["PRIORITY"])); err == nil {
			e.Priority = p
		}
		if grep != nil && !grep.MatchString(e.Message) {
			continue
		}
		res.Entries = append(res.Entries, e)
		if len(res.Entries) >= q.Limit {
			res.Truncated = true
			break
		}
	}
	return res, nil
}

// journald encodes non-UTF8 fields as byte arrays.
func journalString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		b := make([]byte, 0, len(t))
		for _, x := range t {
			if f, ok := x.(float64); ok {
				b = append(b, byte(f))
			}
		}
		return string(b)
	case nil:
		return ""
	default:
		return fmt.Sprint(t)
	}
}

var reSyslog = regexp.MustCompile(`^(?:(\d{4}-\d{2}-\d{2}T\S+)|([A-Z][a-z]{2}\s+\d{1,2} \d{2}:\d{2}:\d{2}))\s+(\S+)\s+([^:\[\s]+)(?:\[(\d+)\])?:\s?(.*)$`)

var defaultLogFiles = []string{"/var/log/syslog", "/var/log/messages"}

func queryFile(ctx context.Context, priv *Privileged, q protocol.LogQuery, since, until time.Time, grep *regexp.Regexp) (protocol.LogResult, error) {
	path := q.File
	if path == "" {
		for _, p := range defaultLogFiles {
			if _, err := os.Stat(p); err == nil {
				path = p
				break
			}
		}
		if path == "" {
			return protocol.LogResult{Source: "none", Entries: []protocol.LogEntry{}, Note: "no journald journal and no /var/log/syslog or /var/log/messages on this host"}, nil
		}
	}
	if err := validateReadable(path); err != nil || !strings.HasPrefix(path, "/var/log/") {
		return protocol.LogResult{}, fmt.Errorf("log file must be under /var/log and readable by policy: %s", path)
	}
	const maxRead = 8 << 20
	content, err := readTail(path, maxRead)
	if err != nil && os.IsPermission(err) && priv != nil {
		err = priv.Do(ctx, 30*time.Second, &content, "read-file", path, strconv.Itoa(maxRead))
	}
	if err != nil {
		return protocol.LogResult{}, err
	}
	res := protocol.LogResult{Source: "file:" + path, Entries: []protocol.LogEntry{}}
	lines := strings.Split(content, "\n")
	year := time.Now().Year()
	unitIdent := strings.TrimSuffix(q.Unit, ".service")
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		if line == "" {
			continue
		}
		e := protocol.LogEntry{Message: line, Priority: 6}
		if m := reSyslog.FindStringSubmatch(line); m != nil {
			if m[1] != "" {
				e.Time, _ = time.Parse(time.RFC3339Nano, m[1])
			} else if t, err := time.ParseInLocation("Jan _2 15:04:05", m[2], time.Local); err == nil {
				e.Time = t.AddDate(year, 0, 0)
				if e.Time.After(time.Now().Add(24 * time.Hour)) {
					e.Time = e.Time.AddDate(-1, 0, 0)
				}
			}
			e.Ident = m[4]
			e.PID, _ = strconv.Atoi(m[5])
			e.Message = m[6]
			e.Priority = guessPriority(e.Message)
		}
		e.Time = e.Time.UTC()
		if unitIdent != "" && e.Ident != unitIdent {
			continue
		}
		if !since.IsZero() && !e.Time.IsZero() && e.Time.Before(since) {
			break
		}
		if !until.IsZero() && !e.Time.IsZero() && e.Time.After(until) {
			continue
		}
		if q.Priority > 0 && e.Priority > q.Priority {
			continue
		}
		if grep != nil && !grep.MatchString(line) {
			continue
		}
		res.Entries = append(res.Entries, e)
		if len(res.Entries) >= q.Limit {
			res.Truncated = true
			break
		}
	}
	res.Note = "priority for file-based logs is inferred from message keywords"
	return res, nil
}

var (
	reErrWords  = regexp.MustCompile(`(?i)\b(error|failed|failure|fatal|panic|segfault|denied|critical|emerg)\b`)
	reWarnWords = regexp.MustCompile(`(?i)\b(warn|warning|deprecated|timeout|timed out|retry)\b`)
)

func guessPriority(msg string) int {
	switch {
	case reErrWords.MatchString(msg):
		return 3
	case reWarnWords.MatchString(msg):
		return 4
	}
	return 6
}

// LogFiles lists readable log files under /var/log for the UI picker.
func LogFiles() []string {
	var out []string
	_ = filepath.WalkDir("/var/log", func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if strings.Count(p, "/") > 4 || p == "/var/log/journal" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(p, ".gz") || strings.HasSuffix(p, ".xz") || strings.HasSuffix(p, ".zst") || regexp.MustCompile(`\.\d+$`).MatchString(p) {
			return nil
		}
		if info, err := d.Info(); err == nil && info.Mode().IsRegular() && info.Size() > 0 {
			if validateReadable(p) == nil {
				out = append(out, p)
			}
		}
		return nil
	})
	return out
}
