package main

import (
	"strings"
	"testing"
)

func TestParseWgQuick(t *testing.T) {
	// 32 zero bytes, standard WG base64 length
	const zeroKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	conf := `[Interface]
PrivateKey = ` + zeroKey + `
Address = 10.66.66.2/32
DNS = 1.1.1.1
MTU = 1280

[Peer]
PublicKey = ` + zeroKey + `
AllowedIPs = 0.0.0.0/0
Endpoint = 127.0.0.1:9000
PersistentKeepalive = 25
`
	cfg, err := parseWgQuick(conf)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.addresses) != 1 || cfg.addresses[0].String() != "10.66.66.2" {
		t.Fatalf("addresses: %v", cfg.addresses)
	}
	if cfg.endpoint != "127.0.0.1:9000" {
		t.Fatalf("endpoint: %s", cfg.endpoint)
	}
	if cfg.mtu != 1280 {
		t.Fatalf("mtu: %d", cfg.mtu)
	}
	ipc := cfg.ipcRequest()
	for _, part := range []string{
		"private_key=",
		"public_key=",
		"endpoint=127.0.0.1:9000",
		"allowed_ip=0.0.0.0/0",
	} {
		if !strings.Contains(ipc, part) {
			t.Fatalf("ipc missing %q: %s", part, ipc)
		}
	}
}
