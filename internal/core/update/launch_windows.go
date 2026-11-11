//go:build windows

package update

import (
	"os/exec"
	"syscall"
)

func canInstall() bool { return true }

// launch runs the per-user installer silently; /RELAUNCH makes it start NEXUS
// again when it finishes, and /UPDATE keeps the user's shortcut choices.
func launch(p *Pending) error {
	if err := VerifyFile(p); err != nil {
		return err
	}
	cmd := exec.Command(p.Path, "/S", "/UPDATE", "/RELAUNCH")
	const detached, newGroup = 0x00000008, 0x00000200 // DETACHED_PROCESS, CREATE_NEW_PROCESS_GROUP
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: detached | newGroup}
	return cmd.Start()
}
