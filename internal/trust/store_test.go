package trust

import (
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"myapp/internal/identity"
)

func TestTrustVerifyAndRevoke(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	peer, err := identity.Generate("子电脑", now)
	if err != nil {
		t.Fatal(err)
	}
	store := New(t.TempDir())
	record, err := store.AddNew(peer.CertDER, now)
	if err != nil {
		t.Fatal(err)
	}
	if record.DeviceID != peer.DeviceID || record.DeviceName != "子电脑" {
		t.Fatalf("记录错误: %+v", record)
	}
	if _, err := store.VerifyCertificate(peer.CertDER, now); err != nil {
		t.Fatal(err)
	}
	if err := store.Revoke(peer.DeviceID, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.VerifyCertificate(peer.CertDER, now); !errors.Is(err, ErrRevoked) {
		t.Fatalf("撤销后仍被信任: %v", err)
	}
	if _, err := store.AddNew(peer.CertDER, now); !errors.Is(err, ErrRevoked) {
		t.Fatalf("撤销设备可重新加入: %v", err)
	}
}

func TestTrustRejectsDifferentCertificate(t *testing.T) {
	now := time.Now()
	first, err := identity.Generate("同名设备", now)
	if err != nil {
		t.Fatal(err)
	}
	store := New(t.TempDir())
	if _, err := store.AddNew(first.CertDER, now); err != nil {
		t.Fatal(err)
	}
	path := store.trustedPath(first.DeviceID)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	record.Fingerprint = "00" + record.Fingerprint[2:]
	data, err = json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.VerifyCertificate(first.CertDER, now); err == nil {
		t.Fatal("被篡改的信任记录未被拒绝")
	}
}
