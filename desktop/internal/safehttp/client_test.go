package safehttp

import (
	"net"
	"testing"
)

func TestPublicIP(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1": false, "10.1.2.3": false, "192.168.1.1": false, "169.254.1.1": false,
		"::1": false, "fd00::1": false, "0.0.0.0": false,
		"1.1.1.1": true, "2606:4700::1111": true,
	} {
		if got := PublicIP(net.ParseIP(addr)); got != want {
			t.Errorf("PublicIP(%s) = %v, want %v", addr, got, want)
		}
	}
}
