package main

import (
	"context"
	"net"
	"os"
	"time"
)

// Android has no /etc/resolv.conf, so Go's pure resolver (CGO_ENABLED=0) would ask
// 127.0.0.1:53 and fail — forward.go could not reach discord.com. Point it at public DNS
// instead (same fix as native/avatar.go). On a machine with resolv.conf nothing changes.
func init() {
	if _, err := os.Stat("/etc/resolv.conf"); err == nil {
		return
	}
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: 3 * time.Second}
			c, err := d.DialContext(ctx, network, "1.1.1.1:53")
			if err != nil {
				c, err = d.DialContext(ctx, network, "8.8.8.8:53")
			}
			return c, err
		},
	}
}
