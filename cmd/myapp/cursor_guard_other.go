//go:build !windows

package main

import (
	"errors"
	"io"
)

func runCursorGuard(io.Reader, io.Writer) error { return errors.New("光标看护仅用于 Windows") }
