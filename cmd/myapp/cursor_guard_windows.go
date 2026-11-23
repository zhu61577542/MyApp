//go:build windows

package main

import (
	"io"
	"myapp/internal/pointer"
)

func runCursorGuard(input io.Reader, output io.Writer) error { return pointer.RunGuard(input, output) }
