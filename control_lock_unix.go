//go:build darwin || linux

// Cross-process control lock via flock: serializes local CDP operations so
// concurrent zcodecli runs cannot interleave on the single debug port.

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

func acquireControlLock(ctx context.Context) (*os.File, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(home, ".zcode")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create control lock directory: %w", err)
	}
	file, err := os.OpenFile(filepath.Join(dir, "zcodecli-cdp.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return file, nil
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			_ = file.Close()
			return nil, fmt.Errorf("acquire CDP control lock: %w", err)
		}
		select {
		case <-ctx.Done():
			_ = file.Close()
			return nil, fmt.Errorf("timed out waiting for another zcodecli CDP operation: %w", ctx.Err())
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func releaseControlLock(file *os.File) {
	if file == nil {
		return
	}
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	_ = file.Close()
}
