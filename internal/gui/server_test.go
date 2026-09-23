package gui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestBootstrapAndState(t *testing.T) {
	root := t.TempDir()
	server := New(Options{
		ConfigPath:        filepath.Join(root, "config.json"),
		IdentityDir:       filepath.Join(root, "identity"),
		DefaultDeviceName: "测试设备",
	})

	initial := httptest.NewRecorder()
	server.handleState(initial, httptest.NewRequest(http.MethodGet, "/api/state", nil))
	if initial.Code != http.StatusOK {
		t.Fatalf("initial state status = %d", initial.Code)
	}
	var before state
	if err := json.NewDecoder(initial.Body).Decode(&before); err != nil {
		t.Fatal(err)
	}
	if before.Ready || before.SetupReason == "" {
		t.Fatalf("unexpected initial state: %+v", before)
	}

	bootstrap := httptest.NewRequest(http.MethodPost, "/api/bootstrap", jsonBody(`{"device_name":"测试设备","role":"controller"}`))
	created := httptest.NewRecorder()
	server.handleBootstrap(created, bootstrap)
	if created.Code != http.StatusOK {
		t.Fatalf("bootstrap status = %d: %s", created.Code, created.Body.String())
	}

	ready := httptest.NewRecorder()
	server.handleState(ready, httptest.NewRequest(http.MethodGet, "/api/state", nil))
	var after state
	if err := json.NewDecoder(ready.Body).Decode(&after); err != nil {
		t.Fatal(err)
	}
	if !after.Ready || after.DeviceName != "测试设备" || after.Role != "controller" {
		t.Fatalf("unexpected ready state: %+v", after)
	}
}

func TestBootstrapIsIdempotentlyRejected(t *testing.T) {
	root := t.TempDir()
	server := New(Options{ConfigPath: filepath.Join(root, "config.json"), IdentityDir: filepath.Join(root, "identity"), DefaultDeviceName: "设备"})
	request := httptest.NewRequest(http.MethodPost, "/api/bootstrap", jsonBody(`{"device_name":"设备","role":"agent"}`))
	server.handleBootstrap(httptest.NewRecorder(), request)
	second := httptest.NewRecorder()
	server.handleBootstrap(second, httptest.NewRequest(http.MethodPost, "/api/bootstrap", jsonBody(`{"device_name":"设备","role":"agent"}`)))
	if second.Code != http.StatusConflict {
		t.Fatalf("second bootstrap status = %d", second.Code)
	}
}

func jsonBody(value string) *strings.Reader { return strings.NewReader(value) }
