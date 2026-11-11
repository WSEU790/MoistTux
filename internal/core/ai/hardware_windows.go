//go:build windows

package ai

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}

type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

var procGlobalMemoryStatusEx = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")

func platformHardware(context.Context) Hardware {
	h := Hardware{CPUThreads: runtime.NumCPU()}
	var ms memoryStatusEx
	ms.Length = uint32(unsafe.Sizeof(ms))
	if r, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&ms))); r != 0 {
		h.RAMTotal, h.RAMAvailable = ms.TotalPhys, ms.AvailPhys
	}
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, `HARDWARE\DESCRIPTION\System\CentralProcessor\0`, registry.QUERY_VALUE); err == nil {
		if s, _, err := k.GetStringValue("ProcessorNameString"); err == nil {
			h.CPUModel = strings.TrimSpace(s)
		}
		k.Close()
	}
	// Display adapters: the 64-bit qwMemorySize value is accurate beyond 4 GiB,
	// unlike Win32_VideoController.AdapterRAM.
	const class = `SYSTEM\CurrentControlSet\Control\Class\{4d36e968-e325-11ce-bfc1-08002be10318}`
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, class, registry.ENUMERATE_SUB_KEYS); err == nil {
		names, _ := k.ReadSubKeyNames(64)
		k.Close()
		for _, n := range names {
			sk, err := registry.OpenKey(registry.LOCAL_MACHINE, class+`\`+n, registry.QUERY_VALUE)
			if err != nil {
				continue
			}
			desc, _, _ := sk.GetStringValue("DriverDesc")
			mem, _, err := sk.GetIntegerValue("HardwareInformation.qwMemorySize")
			if err != nil {
				if b, _, err2 := sk.GetBinaryValue("HardwareInformation.MemorySize"); err2 == nil && len(b) >= 4 {
					mem = uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24
				}
			}
			sk.Close()
			if desc != "" && !strings.Contains(strings.ToLower(desc), "basic display") {
				h.GPUs = append(h.GPUs, GPU{Name: desc, VRAMBytes: mem, Source: "registry"})
			}
		}
	}
	return h
}
