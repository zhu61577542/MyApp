//go:build linux

package linuxclipboard

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type FileBackend struct {
	readCommand  []string
	writeCommand []string
}

func NewFiles() (*FileBackend, error) {
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		if _, err := exec.LookPath("wl-paste"); err == nil {
			if _, err := exec.LookPath("wl-copy"); err == nil {
				return &FileBackend{readCommand: []string{"wl-paste", "--no-newline", "--type", "text/uri-list"}, writeCommand: []string{"wl-copy", "--type", "text/uri-list"}}, nil
			}
		}
	}
	if _, err := exec.LookPath("xclip"); err == nil {
		return &FileBackend{readCommand: []string{"xclip", "-selection", "clipboard", "-t", "text/uri-list", "-out"}, writeCommand: []string{"xclip", "-selection", "clipboard", "-t", "text/uri-list", "-in"}}, nil
	}
	return nil, errors.New("未找到 wl-clipboard 或 xclip")
}

func (b *FileBackend) ReadFiles(ctx context.Context) ([]string, uint64, bool, error) {
	var formats []byte
	var err error
	if b.readCommand[0] == "xclip" {
		formats, err = exec.CommandContext(ctx, "xclip", "-selection", "clipboard", "-t", "TARGETS", "-out").Output()
	} else {
		formats, err = exec.CommandContext(ctx, "wl-paste", "--list-types").Output()
	}
	if err != nil {
		return nil, 0, false, ctx.Err()
	}
	found := false
	for _, format := range strings.Fields(string(formats)) {
		if format == "text/uri-list" {
			found = true
		}
	}
	if !found {
		return nil, 0, false, nil
	}
	data, err := exec.CommandContext(ctx, b.readCommand[0], b.readCommand[1:]...).Output()
	if err != nil {
		return nil, 0, false, nil
	}
	sum := sha256.Sum256(data)
	paths, err := parseURIList(data)
	return paths, binary.BigEndian.Uint64(sum[:8]), len(paths) > 0, err
}

func (b *FileBackend) WriteFiles(ctx context.Context, paths []string) error {
	var content bytes.Buffer
	for _, filename := range paths {
		absolute, err := filepath.Abs(filename)
		if err != nil {
			return err
		}
		if _, err := os.Stat(absolute); err != nil {
			return err
		}
		uri := &url.URL{Scheme: "file", Path: filepath.ToSlash(absolute)}
		content.WriteString(uri.String())
		content.WriteString("\r\n")
	}
	command := exec.CommandContext(ctx, b.writeCommand[0], b.writeCommand[1:]...)
	command.Stdin = &content
	return command.Run()
}

func parseURIList(data []byte) ([]string, error) {
	var paths []string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		uri, err := url.Parse(line)
		if err != nil || uri.Scheme != "file" || uri.Host != "" && uri.Host != "localhost" {
			return nil, errors.New("文件剪贴板 URI 无效")
		}
		paths = append(paths, filepath.FromSlash(uri.Path))
	}
	return paths, scanner.Err()
}
