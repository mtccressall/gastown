//go:build !windows

package nudge

import (
	"errors"
	"os"
	"syscall"
)

// tryLockFile takes an exclusive flock without blocking. The lock lives on the
// descriptor, so the kernel drops it if this process dies holding it.
func tryLockFile(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, errSlotLockBusy
		}
		return nil, err
	}

	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		// Closing would drop the lock anyway; unlocking first keeps the order
		// explicit. The file itself is never unlinked — removing a locked file
		// lets the next process lock a different inode under the same name.
		_ = f.Close()
	}, nil
}
