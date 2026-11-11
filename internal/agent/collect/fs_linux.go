//go:build linux

package collect

import (
	"bufio"
	"bytes"
	"os"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/WSEU790/MoistTux/internal/protocol"
)

// Mount is one entry of /proc/self/mountinfo.
type Mount struct {
	Device  string
	Mount   string
	FSType  string
	Options string
}

// filesystems that hold user data and are worth monitoring.
var dataFS = map[string]bool{
	"ext2": true, "ext3": true, "ext4": true, "xfs": true, "btrfs": true, "zfs": true,
	"f2fs": true, "vfat": true, "exfat": true, "ntfs": true, "ntfs3": true, "jfs": true,
	"reiserfs": true, "bcachefs": true, "overlay": false, "fuseblk": true,
}

var networkFS = map[string]bool{"nfs": true, "nfs4": true, "cifs": true, "smb3": true, "sshfs": true, "fuse.sshfs": true, "glusterfs": true, "ceph": true}

// unescapeMount decodes the octal escapes used in mountinfo (\040 = space).
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if v, ok := octal(s[i+1 : i+4]); ok {
				b.WriteByte(v)
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func octal(s string) (byte, bool) {
	var v int
	for _, c := range s {
		if c < '0' || c > '7' {
			return 0, false
		}
		v = v*8 + int(c-'0')
	}
	return byte(v), v < 256
}

func (h *Host) Mounts() ([]Mount, error) {
	data, err := os.ReadFile(h.proc("self/mountinfo"))
	if err != nil {
		return nil, err
	}
	var out []Mount
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		pre, post, ok := strings.Cut(line, " - ")
		if !ok {
			continue
		}
		pf := strings.Fields(pre)
		qf := strings.Fields(post)
		if len(pf) < 6 || len(qf) < 2 {
			continue
		}
		opts := pf[5]
		if len(qf) > 2 {
			opts += "," + qf[2]
		}
		out = append(out, Mount{Device: unescapeMount(qf[1]), Mount: unescapeMount(pf[4]), FSType: qf[0], Options: opts})
	}
	return out, sc.Err()
}

// Filesystems returns usage for data-bearing mounts. Each statfs runs with a
// timeout so a hung network mount cannot stall telemetry.
func (h *Host) Filesystems(includeNetwork bool) []protocol.FSUsage {
	mounts, err := h.Mounts()
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var sel []Mount
	for _, m := range mounts {
		if !(dataFS[m.FSType] || (includeNetwork && networkFS[m.FSType])) {
			continue
		}
		// Bind mounts of the same device: keep the shortest mount path.
		key := m.Device + "|" + m.FSType
		if m.FSType == "btrfs" || m.FSType == "zfs" {
			key = m.Mount // subvolumes report independent usage
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		sel = append(sel, m)
	}
	out := make([]protocol.FSUsage, len(sel))
	ok := make([]bool, len(sel))
	var wg sync.WaitGroup
	for i, m := range sel {
		wg.Add(1)
		go func(i int, m Mount) {
			defer wg.Done()
			done := make(chan struct{})
			var st syscall.Statfs_t
			var serr error
			go func() { serr = syscall.Statfs(m.Mount, &st); close(done) }()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				return
			}
			if serr != nil {
				return
			}
			bs := uint64(st.Bsize)
			total := st.Blocks * bs
			free := st.Bfree * bs
			avail := st.Bavail * bs
			used := total - free
			u := protocol.FSUsage{
				Mount: m.Mount, Device: m.Device, FSType: m.FSType,
				Total: total, Used: used, Avail: avail,
				InodesTotal: st.Files, ReadOnly: hasOpt(m.Options, "ro"),
			}
			// df semantics: used / (used + avail) accounts for reserved blocks.
			u.UsedPct = pct(float64(used), float64(used+avail))
			if st.Files > 0 && st.Files >= st.Ffree {
				u.InodesUsed = st.Files - st.Ffree
				u.InodesPct = pct(float64(u.InodesUsed), float64(st.Files))
			}
			out[i], ok[i] = u, true
		}(i, m)
	}
	wg.Wait()
	var res []protocol.FSUsage
	for i := range out {
		if ok[i] && out[i].Total > 0 {
			res = append(res, out[i])
		}
	}
	sort.Slice(res, func(i, j int) bool { return res[i].Mount < res[j].Mount })
	return res
}

func hasOpt(opts, o string) bool {
	for _, x := range strings.Split(opts, ",") {
		if x == o {
			return true
		}
	}
	return false
}

// MountFor returns the device ID for a path, used to avoid crossing filesystems.
func DeviceOf(path string) (uint64, error) {
	var st syscall.Stat_t
	if err := syscall.Lstat(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Dev), nil
}
