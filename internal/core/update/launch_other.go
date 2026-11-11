//go:build !windows

package update

import "errors"

func canInstall() bool { return false }

func launch(*Pending) error {
	return errors.New("automatic installation is available in the Windows app; download the new release from GitHub")
}
