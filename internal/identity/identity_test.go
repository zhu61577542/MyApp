package identity

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGenerateSaveLoad(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "identity")
	generated, err := Generate("主电脑", time.Unix(1700000000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveNew(directory, generated); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(directory)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DeviceID != generated.DeviceID || !loaded.PrivateKey.Equal(generated.PrivateKey) {
		t.Fatal("重载身份不一致")
	}
	if loaded.Cert.Subject.OrganizationalUnit[0] != "主电脑" {
		t.Fatal("设备名称未写入证书")
	}
	for name, mode := range map[string]os.FileMode{KeyFile: 0600, CertFile: 0644} {
		info, err := os.Stat(filepath.Join(directory, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != mode {
			t.Fatalf("%s 权限=%o", name, info.Mode().Perm())
		}
	}
	if err := SaveNew(directory, generated); err == nil {
		t.Fatal("重复保存身份应失败")
	}
}

func TestLoadRejectsMismatchedKey(t *testing.T) {
	first, err := Generate("first", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	second, err := Generate("second", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	first.PrivateKey = append(ed25519.PrivateKey(nil), second.PrivateKey...)
	if err := verify(first); err == nil {
		t.Fatal("证书与私钥不匹配未被拒绝")
	}
}

func TestGenerateRejectsInvalidName(t *testing.T) {
	for _, name := range []string{"", " device "} {
		if _, err := Generate(name, time.Now()); err == nil {
			t.Fatalf("无效名称未被拒绝: %q", name)
		}
	}
}
