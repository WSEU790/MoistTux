//go:build !windows

package secrets

// Default returns the platform protector.
func Default(dataDir string) (Protector, error) { return NewFileKey(dataDir) }
