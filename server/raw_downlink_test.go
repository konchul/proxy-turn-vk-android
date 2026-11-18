package main

import (
	"encoding/binary"
	"errors"
	"net"
	"sync"
	"testing"
)

type rawTestConn struct {
	net.Conn
	writeError error
}

func (c rawTestConn) Write(p []byte) (int, error) {
	if c.writeError != nil {
		return 0, c.writeError
	}
	return len(p), nil
}

func rawTestPacket(dst uint32) []byte {
	pkt := make([]byte, 40)
	pkt[0] = 0x45
	binary.BigEndian.PutUint32(pkt[16:20], dst)
	return pkt
}

func TestRawDownlinkQueueDropCounter(t *testing.T) {
	const ip = 0x0a460002
	w := &downlinkWorker{sendCh: make(chan []byte, 1)}
	r := &rawRouter{sessions: map[uint32]*rawClientSessions{ip: {workers: []*downlinkWorker{w}}}}
	pkt := rawTestPacket(ip)
	r.dispatchDownlink(pkt)
	r.dispatchDownlink(pkt)

	if got := r.downlinkQueueDrops.Load(); got != 1 {
		t.Fatalf("queue drops = %d, want 1", got)
	}
	putBuf2048(<-w.sendCh)
}

func TestRawDownlinkCountsWriteFailure(t *testing.T) {
	const ip = 0x0a460002
	r := &rawRouter{sessions: make(map[uint32]*rawClientSessions)}
	w := r.register(ip, rawTestConn{writeError: errors.New("write failed")}, "test-writer")
	r.dispatchDownlink(rawTestPacket(ip))
	r.unregister(ip, w)
	if got := r.downlinkWriteErrors.Load(); got != 1 {
		t.Fatalf("write errors = %d, want 1", got)
	}
	if got := r.downlinkQueueDrops.Load(); got != 0 {
		t.Fatalf("queue drops = %d, want 0", got)
	}
}

func TestRawDownlinkWorkerChurn(t *testing.T) {
	const ip = 0x0a460002
	r := &rawRouter{sessions: make(map[uint32]*rawClientSessions)}
	fixed := r.register(ip, rawTestConn{}, "test-fixed")
	defer r.unregister(ip, fixed)
	pkt := rawTestPacket(ip)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 3000; i++ {
			w := r.register(ip, rawTestConn{}, "test-churn")
			r.unregister(ip, w)
			if w.downBytes.Load() != 0 {
				t.Error("worker wrote bytes after its final traffic accounting")
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 20000; i++ {
			r.dispatchDownlink(pkt)
		}
	}()
	wg.Wait()
}
