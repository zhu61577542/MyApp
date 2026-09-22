//go:build linux

package linuxclipboard

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"os"
	"os/exec"
)

type Backend struct {
	readCommand  []string
	writeCommand []string
}

func New() (*Backend, error) {
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		if _, err := exec.LookPath("wl-paste"); err == nil {
			if _, err := exec.LookPath("wl-copy"); err == nil {
				return &Backend{readCommand: []string{"wl-paste", "--no-newline", "--type", "text"}, writeCommand: []string{"wl-copy", "--type", "text/plain;charset=utf-8"}}, nil
			}
		}
	}
	if _, err := exec.LookPath("xclip"); err == nil {
		return &Backend{readCommand: []string{"xclip", "-selection", "clipboard", "-out"}, writeCommand: []string{"xclip", "-selection", "clipboard", "-in"}}, nil
	}
	return nil, errors.New("未找到 wl-clipboard 或 xclip")
}

func (b *Backend) ReadText(ctx context.Context) (string, uint64, error) {
	data, err := exec.CommandContext(ctx, b.readCommand[0], b.readCommand[1:]...).Output()
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
	command := exec.CommandContext(ctx, b.writeCommand[0], b.writeCommand[1:]...)
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
