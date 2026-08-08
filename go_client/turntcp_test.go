package main

import (
	"net"
	"testing"
	"time"
)

// TestDialTURNConnUDP verifies the UDP path still returns a working
// connected PacketConn (unchanged default behavior).
func TestDialTURNConnUDP(t *testing.T) {
	ln, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	defer ln.Close()

	conn, closer, err := dialTURNConn(ln.LocalAddr().String(), false)
	if err != nil {
		t.Fatalf("dialTURNConn(udp): %v", err)
	}
	defer closer.Close()

	msg := []byte("hello-udp")
	if _, err := conn.WriteTo(msg, nil); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}

	buf := make([]byte, 64)
	ln.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, err := ln.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("server ReadFromUDP: %v", err)
	}
	if string(buf[:n]) != string(msg) {
		t.Fatalf("got %q, want %q", buf[:n], msg)
	}
}

// TestDialTURNConnTCPFramesSTUNMessage verifies the TCP path round-trips a
// STUN-shaped message through turn.NewSTUNConn's framing (length taken from
// the STUN header, per RFC 5389/5766 §2.2 TCP framing), by acting as a tiny
// TURN "server" on the other end of the TCP connection.
func TestDialTURNConnTCPFramesSTUNMessage(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen tcp: %v", err)
	}
	defer ln.Close()

	serverDone := make(chan []byte, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			serverDone <- nil
			return
		}
		defer c.Close()
		buf := make([]byte, 256)
		c.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, err := c.Read(buf)
		if err != nil {
			serverDone <- nil
			return
		}
		serverDone <- append([]byte(nil), buf[:n]...)
	}()

	conn, closer, err := dialTURNConn(ln.Addr().String(), true)
	if err != nil {
		t.Fatalf("dialTURNConn(tcp): %v", err)
	}
	defer closer.Close()

	// Minimal well-formed STUN Binding Request header (RFC 5389 §6):
	// type=0x0001, length=0x0000, magic cookie, 12-byte transaction ID.
	stunMsg := []byte{
		0x00, 0x01, // Binding Request
		0x00, 0x00, // length = 0 (no attributes)
		0x21, 0x12, 0xA4, 0x42, // magic cookie
		1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, // transaction ID
	}

	if _, err := conn.WriteTo(stunMsg, nil); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}

	select {
	case got := <-serverDone:
		if got == nil {
			t.Fatal("server did not receive framed STUN message")
		}
		if string(got) != string(stunMsg) {
			t.Fatalf("framing mismatch: got %x, want %x", got, stunMsg)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for TCP-framed STUN message")
	}
}
