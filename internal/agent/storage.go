//go:build linux

package agent

import (
	"container/heap"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"syscall"
	"time"

	"github.com/WSEU790/MoistTux/internal/agent/collect"
	"github.com/WSEU790/MoistTux/internal/protocol"
)

const scanFileCap = 5_000_000

var skipDirs = map[string]bool{"/proc": true, "/sys": true, "/dev": true, "/run": true}

// DirectoryUsage sums allocated bytes below root, returning per-child totals
// down to depth levels. It never follows symlinks or crosses filesystems
// unless crossFS is set, and it honours ctx cancellation.
func DirectoryUsage(ctx context.Context, root string, depth int, crossFS bool) protocol.DirUsageResult {
	start := time.Now()
	res := protocol.DirUsageResult{CrossedFS: crossFS}
	rootDev, err := collect.DeviceOf(root)
	if err != nil {
		res.Root = protocol.DirUsage{Path: root, Partial: true}
		res.Errors = 1
		return res
	}
	var count uint64
	var walk func(path string, level int) protocol.DirUsage
	walk = func(path string, level int) protocol.DirUsage {
		node := protocol.DirUsage{Path: path}
		entries, err := os.ReadDir(path)
		if err != nil {
			res.Errors++
			node.Partial = true
			return node
		}
		for _, e := range entries {
			if ctx.Err() != nil || count > scanFileCap {
				res.Truncated = true
				node.Partial = true
				return node
			}
			p := filepath.Join(path, e.Name())
			if skipDirs[p] {
				continue
			}
			info, err := e.Info()
			if err != nil {
				res.Errors++
				continue
			}
			st, _ := info.Sys().(*syscall.Stat_t)
			if e.IsDir() {
				if st != nil && !crossFS && uint64(st.Dev) != rootDev {
					continue
				}
				child := walk(p, level+1)
				node.Size += child.Size
				node.Files += child.Files
				if child.Partial {
					node.Partial = true
				}
				if level < depth {
					node.Children = append(node.Children, child)
				}
				continue
			}
			count++
			node.Files++
			if st != nil {
				node.Size += uint64(st.Blocks) * 512
			} else {
				node.Size += uint64(info.Size())
			}
		}
		sort.Slice(node.Children, func(i, j int) bool { return node.Children[i].Size > node.Children[j].Size })
		if len(node.Children) > 50 {
			node.Children = node.Children[:50]
		}
		return node
	}
	res.Root = walk(root, 0)
	res.ScannedMs = time.Since(start).Milliseconds()
	return res
}

type fileHeap []protocol.LargeFile

func (h fileHeap) Len() int            { return len(h) }
func (h fileHeap) Less(i, j int) bool  { return h[i].Size < h[j].Size }
func (h fileHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *fileHeap) Push(x any)         { *h = append(*h, x.(protocol.LargeFile)) }
func (h *fileHeap) Pop() any           { o := *h; n := len(o); x := o[n-1]; *h = o[:n-1]; return x }

// FindLargeFiles returns the largest regular files >= minSize under root.
// It only reports; NEXUS never deletes files based on size alone.
func FindLargeFiles(ctx context.Context, root string, minSize uint64, limit int, crossFS bool) protocol.LargeFilesResult {
	start := time.Now()
	res := protocol.LargeFilesResult{}
	rootDev, err := collect.DeviceOf(root)
	if err != nil {
		res.Errors = 1
		return res
	}
	h := &fileHeap{}
	users := map[uint32]string{}
	if pw, err := collect.Default().Passwd(); err == nil {
		for _, u := range pw {
			users[uint32(u.UID)] = u.Name
		}
	}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil || res.Scanned > scanFileCap {
			res.Truncated = true
			return filepath.SkipAll
		}
		if err != nil {
			res.Errors++
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if skipDirs[p] {
				return filepath.SkipDir
			}
			if !crossFS {
				if info, err := d.Info(); err == nil {
					if st, ok := info.Sys().(*syscall.Stat_t); ok && uint64(st.Dev) != rootDev {
						return filepath.SkipDir
					}
				}
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		res.Scanned++
		info, err := d.Info()
		if err != nil {
			res.Errors++
			return nil
		}
		if uint64(info.Size()) < minSize {
			return nil
		}
		lf := protocol.LargeFile{Path: p, Size: uint64(info.Size()), ModTime: info.ModTime().UTC()}
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			if n, ok := users[st.Uid]; ok {
				lf.Owner = n
			} else {
				lf.Owner = strconv.Itoa(int(st.Uid))
			}
		}
		if h.Len() < limit {
			heap.Push(h, lf)
		} else if (*h)[0].Size < lf.Size {
			(*h)[0] = lf
			heap.Fix(h, 0)
		}
		return nil
	})
	res.Files = make([]protocol.LargeFile, h.Len())
	for i := len(res.Files) - 1; i >= 0; i-- {
		res.Files[i] = heap.Pop(h).(protocol.LargeFile)
	}
	res.ScannedMs = time.Since(start).Milliseconds()
	return res
}
