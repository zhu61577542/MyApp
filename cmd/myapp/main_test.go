package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigAndIdentityCommands(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	identityDir := filepath.Join(root, "identity")
	var stdout, stderr bytes.Buffer

	if code := run([]string{"config", "init", "-path", configPath, "-name", "主电脑", "-role", "controller"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("config init code=%d stderr=%s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"config", "validate", "-path", configPath}, strings.NewReader(""), &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "配置有效") {
		t.Fatalf("config validate code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"identity", "init", "-dir", identityDir, "-name", "主电脑"}, strings.NewReader(""), &stdout, &stderr); code != 0 || strings.TrimSpace(stdout.String()) == "" {
		t.Fatalf("identity init code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"identity", "show", "-dir", identityDir}, strings.NewReader(""), &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), `"name": "主电脑"`) {
		t.Fatalf("identity show code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"unknown"}, strings.NewReader(""), &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "未知命令") {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
}
