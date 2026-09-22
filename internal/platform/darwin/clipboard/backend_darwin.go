//go:build darwin

package darwinclipboard

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"os/exec"
)

type Backend struct{}

func New() *Backend {
	return &Backend{}
}

func (b *Backend) ReadText(ctx context.Context) (string, uint64, error) {
	data, err := exec.CommandContext(ctx, "/usr/bin/pbpaste").Output()
	if err != nil {
		if ctx.Err() != nil {
			return "", 0, ctx.Err()
		}
		data = nil
	}
	sum := sha256.Sum256(data)
	return string(data), binary.BigEndian.Uint64(sum[:8]), nil
}

func (b *Backend) WriteText(ctx context.Context, text string) error {
	command := exec.CommandContext(ctx, "/usr/bin/pbcopy")
	input, err := command.StdinPipe()
	if err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		return err
	}
	if _, err := input.Write([]byte(text)); err != nil {
		_ = input.Close()
		_ = command.Wait()
		return err
	}
	if err := input.Close(); err != nil {
		_ = command.Wait()
		return err
	}
	return command.Wait()
}
