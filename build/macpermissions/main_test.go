package main

import (
	"bytes"
	"testing"
)

func TestPatchAddsAndUpdatesPermissions(t *testing.T) {
	input := []byte(`<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>local.myapp.client</string><key>Nested</key><dict><key>Value</key><string>kept</string></dict><key>NSLocalNetworkUsageDescription</key><string>old</string></dict></plist>`)
	one, err := patch(input)
	if err != nil {
		t.Fatal(err)
	}
	two, err := patch(one)
	if err != nil || !bytes.Equal(one, two) {
		t.Fatalf("重复补齐不稳定: %v", err)
	}
	for _, key := range []string{"NSLocalNetworkUsageDescription", "NSInputMonitoringUsageDescription", "NSAccessibilityUsageDescription"} {
		if bytes.Count(one, []byte("<key>"+key+"</key>")) != 1 {
			t.Fatal(key)
		}
	}
	if !bytes.Contains(one, []byte("<string>kept</string>")) || bytes.Contains(one, []byte("<string>old</string>")) {
		t.Fatal("原属性或说明更新错误")
	}
	if _, err := patch([]byte(`<plist><dict></dict></plist>`)); err == nil {
		t.Fatal("接受了其他应用")
	}
}
