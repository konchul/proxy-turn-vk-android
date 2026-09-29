// loadgen — генератор нагрузки для raw-режима WDTT-сервера.
// Говорит тем же протоколом, что go_client в RawMode: RTP-obfs (ChaCha20-Poly1305)
// поверх UDP на -listen-raw, handshake GETCONF_RAW, дальше — сырые IPv4/UDP
// пакеты через серверный TUN. Пары "эхо" на той же машине замыкают цикл
// uplink→TUN→echo→TUN→downlink, чтобы грузить оба направления.
package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"strconv"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
	"golang.org/x/sys/unix"
)

const rtpHeaderLen = 12

func deriveWrapKey(password string) ([]byte, error) {
	key := make([]byte, 32)
	reader := hkdf.New(sha256.New, []byte(password), []byte("WDTT-WRAP-v1"), []byte("rtp-obfs/chacha20poly1305"))
	if _, err := io.ReadFull(reader, key); err != nil {
		return nil, err
	}
	return key, nil
}

// obfsWrap — точная копия фрейма server/obfs.go obfsWrapPacket (audio-режим:
// PT=111, padding 1..24). dst переиспользуется.
func obfsWrap(aead cipher.AEAD, payload []byte, ssrc uint32, seq uint16, ts uint32, dst []byte) []byte {
	var nonce [12]byte
	binary.BigEndian.PutUint32(nonce[0:4], ssrc)
	binary.BigEndian.PutUint16(nonce[4:6], seq)
	binary.BigEndian.PutUint32(nonce[8:12], ts)

	padTotal := 1 + rand.Intn(24)
	outLen := rtpHeaderLen + len(payload) + 16 + padTotal
	out := dst[:outLen]
	out[0] = 0x80 | 0x20
	out[1] = 111
	binary.BigEndian.PutUint16(out[2:4], seq)
	binary.BigEndian.PutUint32(out[4:8], ts)
	binary.BigEndian.PutUint32(out[8:12], ssrc)
	aead.Seal(out[rtpHeaderLen:rtpHeaderLen], nonce[:], payload, out[:rtpHeaderLen])
	out[outLen-1] = byte(padTotal)
	return out
}

// obfsUnwrap — зеркальная копия server/obfs.go obfsUnwrapPacket.
func obfsUnwrap(aead cipher.AEAD, wire, dst []byte) (int, bool) {
	if len(wire) < rtpHeaderLen+1 || (wire[0]>>6) != 2 {
		return 0, false
	}
	seq := binary.BigEndian.Uint16(wire[2:4])
	ts := binary.BigEndian.Uint32(wire[4:8])
	ssrc := binary.BigEndian.Uint32(wire[8:12])
	payloadEnd := len(wire)
	if wire[0]&0x20 != 0 {
		padLen := int(wire[len(wire)-1])
		if padLen == 0 || padLen > payloadEnd-rtpHeaderLen {
			return 0, false
		}
		payloadEnd -= padLen
	}
	cipherLen := payloadEnd - rtpHeaderLen
	if cipherLen <= 16 || cipherLen-16 > len(dst) {
		return 0, false
	}
	var nonce [12]byte
	binary.BigEndian.PutUint32(nonce[0:4], ssrc)
	binary.BigEndian.PutUint16(nonce[4:6], seq)
	binary.BigEndian.PutUint32(nonce[8:12], ts)
	plain, err := aead.Open(dst[:0], nonce[:], wire[rtpHeaderLen:payloadEnd], wire[:rtpHeaderLen])
	if err != nil {
		return 0, false
	}
	return len(plain), true
}

// buildUDPPacket — сырой IPv4+UDP пакет src→dst:port, пишется в pktBuf
// (переиспользуется — ноль аллокаций на пакет).
func buildUDPPacket(pktBuf []byte, srcIP, dstIP net.IP, srcPort, dstPort uint16, payload []byte, id uint16) []byte {
	udpLen := 8 + len(payload)
	ipLen := 20 + udpLen
	pkt := pktBuf[:ipLen]
	// IPv4
	pkt[0] = 0x45
	binary.BigEndian.PutUint16(pkt[2:4], uint16(ipLen))
	binary.BigEndian.PutUint16(pkt[4:6], id)
	pkt[6] = 0x40 // DF
	pkt[8] = 64   // TTL
	pkt[9] = 17   // UDP
	copy(pkt[12:16], srcIP.To4())
	copy(pkt[16:20], dstIP.To4())
	// контрольная сумма IPv4 (поле обязано быть 0 при расчёте — при переиспользовании
	// буфера там лежит чексумма прошлого пакета)
	binary.BigEndian.PutUint16(pkt[10:12], 0)
	var sum uint32
	for i := 0; i < 20; i += 2 {
		sum += uint32(pkt[i])<<8 | uint32(pkt[i+1])
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	binary.BigEndian.PutUint16(pkt[10:12], ^uint16(sum))
	// UDP
	binary.BigEndian.PutUint16(pkt[20:22], srcPort)
	binary.BigEndian.PutUint16(pkt[22:24], dstPort)
	binary.BigEndian.PutUint16(pkt[24:26], uint16(udpLen))
	binary.BigEndian.PutUint16(pkt[26:28], 0) // UDP checksum 0 = не проверять (IPv4)
	copy(pkt[28:], payload)
	return pkt
}

func main() {
	server := flag.String("server", "127.0.0.1:56003", "адрес raw-листенера WDTT-сервера")
	password := flag.String("password", "", "мастер-пароль сервера")
	device := flag.String("device", "loadgen-1", "deviceID")
	echo := flag.String("echo", "127.0.0.1:7777", "адрес UDP-эха, куда уходят IP-пакеты")
	pps := flag.Int("pps", 10000, "целевой rate пакетов/с")
	size := flag.Int("size", 1200, "размер сырого IP-пакета (байт, включая заголовки)")
	duration := flag.Int("duration", 10, "длительность теста, секунд")
	mode := flag.String("mode", "load", "load | echo")
	cipherFlag := flag.String("cipher", "chacha", "obfs-шифр: chacha | aes (новый протокол, порт 46000)")
	sport := flag.Int("sport", 0, "фиксированный UDP-порт клиента в IP-пакетах (0 = случайный); нужен для echo-gso")
	echoGso := flag.Int("echo-gso", 0, "echo: датаграмм в одной UDP_SEGMENT-отправке (GSO-даунлинк)")
	flag.Parse()

	switch *mode {
	case "echo":
		runEcho(*echo, *echoGso)
		return
	case "load", "flood":
	default:
		log.Fatalf("неизвестный mode %q", *mode)
	}
	if *password == "" {
		log.Fatal("нужен -password")
	}

	key, err := deriveWrapKey(*password)
	if err != nil {
		log.Fatal(err)
	}
	var aead cipher.AEAD
	if *cipherFlag == "aes" {
		block, err := aes.NewCipher(key)
		if err != nil {
			log.Fatal(err)
		}
		aead, err = cipher.NewGCM(block)
		if err != nil {
			log.Fatal(err)
		}
	} else {
		aead, err = chacha20poly1305.New(key)
		if err != nil {
			log.Fatal(err)
		}
	}

	raddr, err := net.ResolveUDPAddr("udp", *server)
	if err != nil {
		log.Fatal(err)
	}
	conn, err := net.DialUDP("udp", nil, raddr)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadBuffer(4 * 1024 * 1024)
	_ = conn.SetWriteBuffer(4 * 1024 * 1024)

	var ssrc = rand.Uint32()
	var seq uint16
	var ts uint32
	var counter uint64
	sendBuf := make([]byte, 2048)
	recvBuf := make([]byte, 2048)
	plain := make([]byte, 2048)

	wrap := func(payload []byte) []byte {
		seq++
		ts += 960
		counter++
		return obfsWrap(aead, payload, ssrc, seq, ts, sendBuf)
	}
	send := func(payload []byte) {
		w := wrap(payload)
		if _, err := conn.Write(w); err != nil {
			log.Fatalf("write: %v", err)
		}
	}

	// Handshake: GETCONF_RAW:deviceID|password
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	send([]byte("GETCONF_RAW:" + *device + "|" + *password))
	n, err := conn.Read(recvBuf)
	if err != nil {
		log.Fatalf("handshake read: %v", err)
	}
	m, ok := obfsUnwrap(aead, recvBuf[:n], plain)
	if !ok {
		log.Fatalf("handshake unwrap failed (%d bytes)", n)
	}
	resp := string(plain[:m])
	if len(resp) < 8 || resp[:8] != "RAWCONF:" {
		log.Fatalf("handshake: сервер ответил %q", resp)
	}
	parts := splitN(resp[8:], '|')
	if len(parts) < 1 || parts[0] == "" {
		log.Fatalf("handshake: нет IP в %q", resp)
	}
	rawIP := net.ParseIP(parts[0]).To4()
	echoHost, echoPortStr, _ := net.SplitHostPort(*echo)
	echoPort, _ := strconv.Atoi(echoPortStr)
	echoIP := net.ParseIP(echoHost)
	if echoIP == nil {
		// резолвим имя (в т.ч. публичный адрес самой машины)
		ips, err := net.LookupIP(echoHost)
		if err != nil || len(ips) == 0 {
			log.Fatalf("echo addr: %v", err)
		}
		echoIP = ips[0]
	}
	log.Printf("[LOADGEN] RAWCONF: ip=%s mtu=%s | echo=%s", parts[0], parts[2], *echo)

	// Основной цикл: пачками по perTick пакетов каждые 1мс.
	perTick := (*pps + 999) / 1000
	if perTick < 1 {
		perTick = 1
	}
	payload := make([]byte, *size-28)
	for i := range payload {
		payload[i] = byte(i)
	}
	pktBuf := make([]byte, *size)
	binary.BigEndian.PutUint32(payload, 0xC0DEBEEF) // маркер эха

	deadline := time.Now().Add(time.Duration(*duration) * time.Second)
	sent, recv := uint64(0), uint64(0)
	oneWay := *mode == "flood"
	var rttSum time.Duration
	rttN := uint64(0)
	rttMax := time.Duration(0)
	var ipID uint16
	var pendingMu sync.Mutex
	pending := make(map[uint32]time.Time) // ip id → время отправки (для RTT)
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()

	go func() {
		for {
			nn, err := conn.Read(recvBuf)
			if err != nil {
				return
			}
			m, ok := obfsUnwrap(aead, recvBuf[:nn], plain)
			if !ok || m < 28 {
				continue
			}
			// Ответ — сырой IP-пакет от эха; берём ip.id для RTT и считаем приём
			// только для наших маркерных пакетов (payload[0:4] == 0xC0DEBEEF).
			if oneWay {
				continue
			}
			if m-28 >= 8 && binary.BigEndian.Uint32(plain[28:32]) == 0xC0DEBEEF {
				recv++
				id := binary.BigEndian.Uint32(plain[32:36])
				pendingMu.Lock()
				if t, ok := pending[id]; ok {
					d := time.Since(t)
					rttSum += d
					rttN++
					if d > rttMax {
						rttMax = d
					}
					delete(pending, uint32(id))
				}
				pendingMu.Unlock()
			}
		}
	}()

	for time.Now().Before(deadline) {
		<-tick.C
		for i := 0; i < perTick; i++ {
			ipID++
			if ipID%64 == 0 {
				binary.BigEndian.PutUint32(payload[4:8], uint32(ipID))
				pendingMu.Lock()
				pending[uint32(ipID)] = time.Now()
				pendingMu.Unlock()
			}
			clientPort := uint16(*sport)
			if clientPort == 0 {
				clientPort = uint16(40000 + rand.Intn(20000))
			}
			pkt := buildUDPPacket(pktBuf, rawIP, echoIP, clientPort, uint16(echoPort), payload, ipID)
			w := wrap(pkt)
			if _, err := conn.Write(w); err != nil {
				log.Fatalf("write: %v", err)
			}
			sent++
		}
	}
	time.Sleep(500 * time.Millisecond)

	var rttAvg time.Duration
	var rttAvgF, rttMaxF float64
	var rttNFinal uint64
	pendingMu.Lock()
	rttNFinal = rttN
	if rttN > 0 {
		rttAvg = rttSum / time.Duration(rttN)
	}
	rttAvgF = float64(rttAvg.Microseconds()) / 1000
	rttMaxF = float64(rttMax.Microseconds()) / 1000
	pendingMu.Unlock()
	if oneWay {
		recv = sent // односторонний замер: считаем только то, что сервер принял
	}
	lossPct := 0.0
	bw := float64(recv) / float64(*duration) * float64(*size) * 8 / 1e6
	if sent > 0 {
		lossPct = 100 * float64(sent-recv) / float64(sent)
	}
	fmt.Printf("RESULT pps_target=%d sent=%d recv=%d achieved_pps=%.0f loss=%.2f%% rtt_avg=%.3fms rtt_max=%.3fms rtt_n=%d bw=%.0f Mbit/s\n",
		*pps, sent, recv, float64(recv)/float64(*duration), lossPct,
		rttAvgF, rttMaxF, rttNFinal, bw)
}

func splitN(s string, sep byte) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

func runEcho(addr string, gsoBatch int) {
	a, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		log.Fatal(err)
	}
	pc, err := net.ListenUDP("udp", a)
	if err != nil {
		log.Fatal(err)
	}
	buf := make([]byte, 2048)
	log.Printf("[ECHO] слушаю %s (gso=%d)", addr, gsoBatch)
	count := 0
	errCount := 0
	last := time.Now()

	var gsoAccum []byte
	// gsoBatch>0: копим batch датаграмм одного размера и отдаём одной
	// UDP_SEGMENT-отправкой — ядро создаёт GSO-skb, TUN с VNET_HDR отдаст
	// её серверу одним суперкадром (упражняет юзерспейс-сегментацию).
	flushGSO := func(to *net.UDPAddr) {
		if len(gsoAccum) == 0 {
			return
		}
		raw, err := pc.SyscallConn()
		if err != nil {
			return
		}
		var sa unix.Sockaddr
		if to4 := to.IP.To4(); to4 != nil {
			sa4 := &unix.SockaddrInet4{Port: to.Port}
			copy(sa4.Addr[:], to4)
			sa = sa4
		}
		// ancillary data: cmsg(SOL_UDP, UDP_SEGMENT, uint16 gso_size)
		oob := make([]byte, unix.CmsgSpace(2))
		h := (*unix.Cmsghdr)(unsafe.Pointer(&oob[0]))
		h.Level = unix.SOL_UDP
		h.Type = unix.UDP_SEGMENT
		h.SetLen(unix.CmsgLen(2))
		binary.LittleEndian.PutUint16(oob[unix.CmsgLen(2):unix.CmsgSpace(2)], uint16(gsoBatch))
		_ = raw.Write(func(fd uintptr) bool {
			n, err := unix.SendmsgN(int(fd), gsoAccum, oob, sa, 0)
			return err == nil && n == len(gsoAccum)
		})
		gsoAccum = gsoAccum[:0]
	}

	for {
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			log.Printf("[ECHO] read err: %v", err)
			return
		}
		count++
		if gsoBatch > 1 {
			if udp4, ok := from.(*net.UDPAddr); ok && n == 1200 {
				gsoAccum = append(gsoAccum, buf[:n]...)
				if len(gsoAccum)/1200 >= gsoBatch {
					flushGSO(udp4)
				}
			} else {
				if _, err := pc.WriteTo(buf[:n], from); err != nil {
					errCount++
				}
			}
		} else {
			if _, err := pc.WriteTo(buf[:n], from); err != nil {
				errCount++
				if errCount <= 3 {
					log.Printf("[ECHO] write err: %v", err)
				}
			}
		}
		if time.Since(last) > time.Second {
			log.Printf("[ECHO] recv=%d err=%d rate=%.0f pps", count, errCount, float64(count)/time.Since(last).Seconds())
			count = 0
			last = time.Now()
		}
	}
}
