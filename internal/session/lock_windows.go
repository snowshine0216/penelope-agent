//go:build windows

package session

// lockExclusive is a no-op on Windows. Per spec D16 the project is POSIX
// focused; running on Windows means concurrent writers are not protected
// at the file-integrity layer. This is documented in the README.
func lockExclusive(_ uintptr) error { return nil }

func unlock(_ uintptr) error { return nil }
