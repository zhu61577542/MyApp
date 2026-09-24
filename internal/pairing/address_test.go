package pairing

import "testing"

func TestValidateConnectAddress(t *testing.T) {
	for _, address := range []string{"192.168.1.20:24800", "localhost:24800", "[fe80::1]:24800"} {
		if err := ValidateConnectAddress(address); err != nil {
			t.Fatalf("地址 %q 不应被拒绝: %v", address, err)
		}
	}
	for _, address := range []string{"0.0.0.0:24800", "[::]:24800", ":24800", "192.168.1.20:0"} {
		if err := ValidateConnectAddress(address); err == nil {
			t.Fatalf("地址 %q 应被拒绝", address)
		}
	}
}
