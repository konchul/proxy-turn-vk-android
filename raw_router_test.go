package main

import (
	"bytes"
	"encoding/binary"
	"net"
	"os"
	"sync"
	"testing"
	"time"
)

// fakeConn — минимальный net.Conn, копящий все Write в срез под мьютексом.
// Единственный писатель на conn (гарантия downlinkWorker — по одной
// writer-горутине на conn), но читаем срез из теста конкурентно, поэтому
// мьютекс всё равно нужен для видимости между горутинами.
type fakeConn struct {
	mu      sync.Mutex
	written [][]byte
	slow    time.Duration
}

func (c *fakeConn) Write(b []byte) (int, error) {
	if c.slow > 0 {
		time.Sleep(c.slow)
	}
	cp := append([]byte(nil), b...)
	c.mu.Lock()
	c.written = append(c.written, cp)
	c.mu.Unlock()
	return len(b), nil
}
func (c *fakeConn) snapshot() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([][]byte, len(c.written))
	copy(out, c.written)
	return out
}
func (c *fakeConn) Read(b []byte) (int, error)         { return 0, net.ErrClosed }
func (c *fakeConn) Close() error                       { return nil }
func (c *fakeConn) LocalAddr() net.Addr                { return nil }
func (c *fakeConn) RemoteAddr() net.Addr               { return nil }
func (c *fakeConn) SetDeadline(t time.Time) error      { return nil }
func (c *fakeConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *fakeConn) SetWriteDeadline(t time.Time) error { return nil }

func ipv4Packet(dstIP string, payload byte, size int) []byte {
	pkt := make([]byte, size)
	pkt[0] = 0x45 // version 4, IHL 5
	dst := net.ParseIP(dstIP).To4()
	copy(pkt[16:20], dst)
	for i := 20; i < size; i++ {
		pkt[i] = payload
	}
	return pkt
}

// TestDownlinkWorkerPreservesOrderWithinConn проверяет, что асинхронная
// per-conn запись (канал + отдельная writer-горутина) не переупорядочивает
// пакеты внутри одного conn — то самое свойство, ради которого существует
// chunk-логика в pickDownlinkConn (иначе TCP-поток видит reorder).
func TestDownlinkWorkerPreservesOrderWithinConn(t *testing.T) {
	fc := &fakeConn{}
	w := newDownlinkWorker(fc, "test-device")

	const n = 200
	for i := 0; i < n; i++ {
		buf := getBuf2048()[:4]
		binary.BigEndian.PutUint32(buf, uint32(i))
		if !w.enqueue(buf) {
			t.Fatalf("enqueue(%d) unexpectedly dropped (буфер канала не должен переполниться при n=%d < downlinkWorkerBuf=%d)", i, n, downlinkWorkerBuf)
		}
	}
	w.stop()

	got := fc.snapshot()
	if len(got) != n {
		t.Fatalf("получено %d пакетов, ожидалось %d", len(got), n)
	}
	for i, pkt := range got {
		want := uint32(i)
		gotVal := binary.BigEndian.Uint32(pkt)
		if gotVal != want {
			t.Fatalf("порядок нарушен на позиции %d: got=%d want=%d", i, gotVal, want)
		}
	}
}

// TestDownlinkWorkerSlowConnDoesNotBlockOthers — сердце правки #24: медленный
// relay (симулирован через fakeConn.slow) не должен блокировать доставку
// пакетов другому, быстрому relay того же или другого клиента. Раньше вся
// запись шла через один поток downlinkLoop — если один conn.Write тормозил,
// стопорился весь downlink всех клиентов. Тест бьёт по этому сценарию напрямую.
func TestDownlinkWorkerSlowConnDoesNotBlockOthers(t *testing.T) {
	slow := &fakeConn{slow: 200 * time.Millisecond}
	fast := &fakeConn{}

	wSlow := newDownlinkWorker(slow, "test-device-slow")
	wFast := newDownlinkWorker(fast, "test-device-fast")
	defer wSlow.stop()
	defer wFast.stop()

	wSlow.enqueue(getBuf2048()[:4]) // уйдёт "зависать" в w.conn.Write на 200мс

	start := time.Now()
	for i := 0; i < 50; i++ {
		if !wFast.enqueue(getBuf2048()[:4]) {
			t.Fatalf("enqueue на быстрый conn неожиданно дропнут на пакете %d", i)
		}
	}
	elapsed := time.Since(start)

	if elapsed > 50*time.Millisecond {
		t.Fatalf("постановка в очередь быстрому conn заняла %v — похоже, всё ещё блокируется медленным relay (ожидался <50мс, медленный conn спит 200мс)", elapsed)
	}
}

// TestRawRouterPickDownlinkConnRoundRobinAndChunking проверяет, что
// pickDownlinkConn с новой сигнатурой (*downlinkWorker вместо net.Conn)
// сохраняет прежнее поведение: адаптивный chunk-размер и round-robin между
// зарегистрированными воркерами одного клиента.
func TestRawRouterPickDownlinkConnRoundRobinAndChunking(t *testing.T) {
	r := &rawRouter{sessions: make(map[string]*rawClientSessions)}

	c1 := &fakeConn{}
	c2 := &fakeConn{}
	w1 := r.register("10.70.66.5", c1, "dev1")
	w2 := r.register("10.70.66.5", c2, "dev2")
	defer r.unregister("10.70.66.5", w1)
	defer r.unregister("10.70.66.5", w2)

	// Мелкий пакет (<=100 байт по downlinkChunkSizeFor) -> chunk=1, значит
	// должно чередоваться на каждом вызове.
	small := 50
	first := r.pickDownlinkConn("10.70.66.5", small)
	second := r.pickDownlinkConn("10.70.66.5", small)
	if first == second {
		t.Fatalf("мелкие пакеты (chunk=1) должны чередоваться между воркерами на каждом вызове, а не оставаться на одном")
	}

	// Неизвестный клиент -> nil, не паника.
	if got := r.pickDownlinkConn("10.70.66.99", small); got != nil {
		t.Fatalf("pickDownlinkConn для незарегистрированного клиента должен возвращать nil, got %v", got)
	}
}

// TestRawRouterUnregisterDrainsQueuedPackets: unregister должен дождаться,
// пока writer-горутина дожуёт уже поставленные в очередь пакеты (stop()
// закрывает канал, но не отбрасывает то, что уже в буфере), а не оборвать
// доставку на середине.
func TestRawRouterUnregisterDrainsQueuedPackets(t *testing.T) {
	r := &rawRouter{sessions: make(map[string]*rawClientSessions)}
	fc := &fakeConn{}
	w := r.register("10.70.66.7", fc, "dev7")

	const n = 30
	for i := 0; i < n; i++ {
		buf := getBuf2048()[:1]
		buf[0] = byte(i)
		w.enqueue(buf)
	}
	r.unregister("10.70.66.7", w)

	got := fc.snapshot()
	if len(got) != n {
		t.Fatalf("после unregister доставлено %d пакетов из %d поставленных в очередь — writer должен дожать буфер перед остановкой", len(got), n)
	}
}

// TestRawRouterEndToEndDownlinkLoop — сквозной прогон через сам downlinkLoop
// (не только pickDownlinkConn/downlinkWorker по отдельности): пишем IPv4
// пакеты в r.file (через os.Pipe, эмулирующий TUN), проверяем что они
// доходят до зарегистрированного conn клиента с корректным содержимым.
func TestRawRouterEndToEndDownlinkLoop(t *testing.T) {
	rd, wr, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer wr.Close()

	r := &rawRouter{file: rd, sessions: make(map[string]*rawClientSessions)}
	go r.downlinkLoop()

	fc := &fakeConn{}
	w := r.register("10.70.66.10", fc, "dev10")
	defer r.unregister("10.70.66.10", w)

	pkt := ipv4Packet("10.70.66.10", 0xAB, 100)
	if _, err := wr.Write(pkt); err != nil {
		t.Fatalf("write to fake TUN: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(fc.snapshot()) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	got := fc.snapshot()
	if len(got) != 1 {
		t.Fatalf("ожидался 1 доставленный пакет, получено %d", len(got))
	}
	if !bytes.Equal(got[0], pkt) {
		t.Fatalf("содержимое пакета изменилось при доставке:\ngot=%x\nwant=%x", got[0], pkt)
	}
}

// TestRawRouterConcurrentRegisterUnregisterUnderLoad эмулирует прод-условия:
// много клиентов постоянно переподключаются (register/unregister), пока
// downlinkLoop параллельно раздаёт им пакеты через pickDownlinkConn — именно
// та комбинация, что и должна быть безопасна после перехода на per-conn
// writer-горутины. Гоняется с -race, чтобы поймать гонки на r.mu/rrIndex/
// rrCount, если они где-то остались незащищёнными.
func TestRawRouterConcurrentRegisterUnregisterUnderLoad(t *testing.T) {
	r := &rawRouter{sessions: make(map[string]*rawClientSessions)}

	const clients = 20
	const churns = 50

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// downlinkLoop-подобная нагрузка: непрерывно дёргаем pickDownlinkConn
	// для случайных клиентов, пока идёт churn регистраций.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				ip := "10.70.66." + string(rune('0'+(seed%clients)%10))
				r.pickDownlinkConn(ip, 200)
			}
		}(i)
	}

	var churnWg sync.WaitGroup
	for c := 0; c < clients; c++ {
		churnWg.Add(1)
		go func(idx int) {
			defer churnWg.Done()
			ip := "10.70.67.1"
			for i := 0; i < churns; i++ {
				fc := &fakeConn{}
				w := r.register(ip, fc, "dev-churn")
				w.enqueue(getBuf2048()[:10])
				r.unregister(ip, w)
			}
		}(c)
	}

	churnWg.Wait()
	close(stop)
	wg.Wait()
}
