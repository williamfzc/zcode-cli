//go:build !(darwin || linux)

// Non-Unix fallback for the control lock: a no-op, matching the flock build.

package main

import (
	"context"
	"os"
)

func acquireControlLock(context.Context) (*os.File, error) { return nil, nil }
func releaseControlLock(*os.File)                          {}
