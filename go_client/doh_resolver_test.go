package main

import (
	"context"
	"net"
	"testing"
)

func TestDoHResolverLookupHost(t *testing.T) {
	if testing.Short() {
		t.Skip("network test")
	}
	cases := []string{"doh-cloudflare", "doh-google", "doh-yandex"}
	for _, arg := range cases {
		t.Run(arg, func(t *testing.T) {
			setupGlobalResolver(arg)
			addrs, err := net.DefaultResolver.LookupHost(context.Background(), "login.vk.ru")
			if err != nil {
				t.Fatalf("lookup failed: %v", err)
			}
			if len(addrs) == 0 {
				t.Fatal("no addresses")
			}
			t.Logf("addrs=%v", addrs)
		})
	}
}
