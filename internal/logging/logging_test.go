package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoggerFiltersAndFormatsStreams(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "myapp.log")
	logger, err := Open(path, Settings{Enabled: true, Level: Info})
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()
	logger.Log(Debug, "不应写入")
	stream := logger.Stream(Error)
	if _, err := stream.Write([]byte("第一行\n第二行")); err != nil {
		t.Fatal(err)
	}
	stream.(*streamWriter).Flush()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, "不应写入") || !strings.Contains(text, "[error] 第一行") || !strings.Contains(text, "[error] 第二行") {
		t.Fatalf("日志内容不符合预期: %q", text)
	}
}

func TestDisabledLoggerDoesNotWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "myapp.log")
	logger, err := Open(path, Settings{Enabled: false, Level: Debug})
	if err != nil {
		t.Fatal(err)
	}
	logger.Log(Error, "关闭日志时不应写入")
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Fatalf("关闭日志后仍有内容: %q", data)
	}
}

func TestLoggerRedactsPairingCode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "myapp.log")
	logger, err := Open(path, DefaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	logger.Log(Info, "远端设备: test\n配对码: 123456")
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, "123456") || !strings.Contains(text, "[已隐藏]") {
		t.Fatalf("配对码未正确脱敏: %q", text)
	}
}
