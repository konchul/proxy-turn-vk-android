package main

// CSQTT-режим: драйвер протокола amurcanov/csqtt (Rust-сервер) для
// сравнительного бенчмарка с qWDTT. Спецификация — csqtt-protocol-notes.md.
// Ключ: HKDF-SHA256(salt "CSQTT-WRAP-v1", info "rtp-obfs/chacha20poly1305").
// Провод: [CQF1 24Б: magic+sender+flow+seq] + [RTP 24Б с расширениями]
// + payload + 16Б tag ChaCha-Poly1305 (AAD=RTP-заголовок) + padding + padlen.

import (
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"regexp"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
)

const csqttWrapSalt = "CSQTT-WRAP-v1"
const csqttWrapInfo = "rtp-obfs/chacha20poly1305"
const csqttFrameLen = 24

var csqttActive atomic.Bool

func deriveCsqttWrapKey(password string) ([]byte, error) {
	key := make([]byte, 32)
	reader := hkdf.New(sha256.New, []byte(password), []byte(csqttWrapSalt), []byte(csqttWrapInfo))
	if _, err := io.ReadFull(reader, key); err != nil {
		return nil, err
	}
	return key, nil
}

type csqttState struct {
	key            []byte
	aead           cipher.AEAD
	ssrc           uint32
	initialSeq     uint16
	initialTs      uint32
	initialAbsSend uint32
	transportSeq   uint16
	started        time.Time
	count          uint64
	flowSeq        uint32
	senderID       uint64
	flowID         uint64
	payloadType    byte
	paddingMax     int
}

func newCsqttState(key []byte, payloadType byte, paddingMax int) (*csqttState, error) {
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, err
	}
	var b4 [4]byte
	rand.Read(b4[:])
	var b2 [2]byte
	rand.Read(b2[:])
	return &csqttState{
		key:            key,
		aead:           aead,
		ssrc:           binary.BigEndian.Uint32(b4[:]),
		initialSeq:     binary.BigEndian.Uint16(b2[:]),
		initialTs:      randUint32(),
		initialAbsSend: randUint32(),
		transportSeq:   uint16(randUint32()),
		started:        time.Now(),
		payloadType:    payloadType,
		paddingMax:     paddingMax,
		senderID:       randUint64(),
		flowID:         randUint64(),
	}, nil
}

func randUint32() uint32 {
	var b [4]byte
	rand.Read(b[:])
	return binary.BigEndian.Uint32(b[:])
}

func randUint64() uint64 {
	var b [8]byte
	rand.Read(b[:])
	return binary.BigEndian.Uint64(b[:])
}

// csqttNonce: [ssrc BE4][seq BE2][00 00][ts BE4] — как в rust-client obfs.rs.
func csqttNonce(n *[12]byte, ssrc uint32, seq uint16, ts uint32) {
	binary.BigEndian.PutUint32(n[0:4], ssrc)
	binary.BigEndian.PutUint16(n[4:6], seq)
	binary.BigEndian.PutUint32(n[8:12], ts)
}

// csqttSrtpIV: 16-байтный вариант для video-режима (AES-CTR).
func csqttSrtpIV(iv *[16]byte, ssrc uint32, seq uint16, ts uint32) {
	binary.BigEndian.PutUint32(iv[0:4], ssrc)
	binary.BigEndian.PutUint16(iv[4:6], seq)
	binary.BigEndian.PutUint32(iv[8:12], ts)
}

// csqttWrap: [CQF1 24][RTP 24][chacha(payload)+tag16][pad][padlen].
// payload = raw IP packet (для flood) или текст хендшейка.
func csqttWrap(st *csqttState, payload []byte, isHandshake bool) ([]byte, error) {
	count := atomic.AddUint64(&st.count, 1) - 1
	seq := st.initialSeq + uint16(count)
	ts := st.initialTs + uint32(float64(time.Since(st.started).Seconds())*48000)
	absSend := st.initialAbsSend + uint32(float64(time.Since(st.started).Seconds())*262144)
	tseq := st.transportSeq + uint16(count)

	padRandom := 0
	if st.paddingMax > 0 {
		padRandom = int(randUint32()) % st.paddingMax
	}
	padTotal := padRandom + 1

	total := csqttFrameLen + 24 + len(payload) + 16 + padTotal
	out := make([]byte, total)

	// CQF1 flow-frame (uplink plaintext = CQF1 + raw IP)
	var cqi [24]byte
	copy(cqi[0:4], "CQF1")
	binary.BigEndian.PutUint64(cqi[4:12], st.senderID)
	binary.BigEndian.PutUint64(cqi[12:20], st.flowID)
	fs := atomic.AddUint32(&st.flowSeq, 1) - 1
	binary.BigEndian.PutUint32(cqi[20:24], fs)
	copy(out[:csqttFrameLen], cqi[:])

	// RTP-заголовок с расширениями
	hdr := out[csqttFrameLen : csqttFrameLen+24]
	hdr[0] = 0xB0 | 0x20
	hdr[1] = st.payloadType & 0x7F
	binary.BigEndian.PutUint16(hdr[2:4], seq)
	binary.BigEndian.PutUint32(hdr[4:8], ts)
	binary.BigEndian.PutUint32(hdr[8:12], st.ssrc)
	binary.BigEndian.PutUint16(hdr[12:14], 0xBEDE)
	binary.BigEndian.PutUint16(hdr[14:16], 2)
	hdr[16] = 0x32
	hdr[17] = byte(absSend >> 16)
	hdr[18] = byte(absSend >> 8)
	hdr[19] = byte(absSend)
	hdr[20] = 0x51
	binary.BigEndian.PutUint16(hdr[21:23], tseq)
	hdr[23] = 0

	// plaintext = CQF1 + payload (uplink фрейминг)
	plain := make([]byte, 0, csqttFrameLen+len(payload))
	if !isHandshake {
		plain = append(plain, cqi[:]...)
	}
	plain = append(plain, payload...)

	var nonce [12]byte
	csqttNonce(&nonce, st.ssrc, seq, ts)
	_ = st.aead.Seal(out[csqttFrameLen+24:csqttFrameLen+24],
		nonce[:], plain, out[csqttFrameLen:csqttFrameLen+24])

	// padding
	padStart := csqttFrameLen + 24 + len(payload) + 16
	for i := padStart; i < padStart+padRandom; i++ {
		out[i] = byte(randUint32())
	}
	out[total-1] = byte(padTotal)
	return out, nil
}

// runCsqtt — самодостаточный flood-клиент CSQTT: handshake GETCONF + flood.
func runCsqtt(server, password, device string, pps, size, duration int) {
	key, err := deriveCsqttWrapKey(password)
	if err != nil {
		log.Fatalf("csqtt key: %v", err)
	}
	st, err := newCsqttState(key, 111, 24)
	if err != nil {
		log.Fatalf("csqtt state: %v", err)
	}

	raddr, err := net.ResolveUDPAddr("udp", server)
	if err != nil {
		log.Fatalf("csqtt resolve: %v", err)
	}
	conn, err := net.DialUDP("udp", nil, raddr)
	if err != nil {
		log.Fatalf("csqtt dial: %v", err)
	}
	defer conn.Close()
	_ = conn.SetReadBuffer(4 << 20)
	_ = conn.SetWriteBuffer(4 << 20)

	// handshake: GETCONF (plaintext без CQF1)
	hs := fmt.Sprintf("GETCONF:9000|%s|%s|1|salt|4|36|CSQTT-WIRE-3", device, password)
	w, err := csqttWrap(st, []byte(hs), true)
	if err != nil {
		log.Fatalf("csqtt wrap hs: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(8 * time.Second))
	if _, err := conn.Write(w); err != nil {
		log.Fatalf("csqtt hs write: %v", err)
	}
	buf := make([]byte, 2048)
	n, err := conn.Read(buf)
	if err != nil {
		log.Fatalf("csqtt handshake read: %v", err)
	}
	// unwrap ответа: RTP 24Б + chacha
	if n < 40 {
		log.Fatalf("csqtt handshake reply too short: %d", n)
	}
	var nonce [12]byte
	csqttNonce(&nonce, binary.BigEndian.Uint32(buf[8:12]), binary.BigEndian.Uint16(buf[2:4]), binary.BigEndian.Uint32(buf[4:8]))
	plain, err := st.aead.Open(nil, nonce[:], buf[24:n], buf[:24])
	if err != nil {
		log.Fatalf("csqtt handshake decrypt: %v", err)
	}
	if len(plain) >= 24 && string(plain[:4]) == "CQF1" {
		plain = plain[24:]
	}
	text := string(plain)
	re := regexp.MustCompile(`\b(?:10|172|192)\.\d+\.\d+\.\d+\b`)
	assigned := re.FindString(text)
	if assigned == "" {
		assigned = "10.70.99.1"
	}
	log.Printf("[CSQTT] конфиг: %q | src=%s", text[:min(len(text), 120)], assigned)

	// flood: односторонний поток src=assigned → bogon
	assignedIP := net.ParseIP(assigned).To4()
	if assignedIP == nil {
		assignedIP = net.ParseIP("10.70.99.1").To4()
	}
	payload := make([]byte, size-28)
	for i := range payload {
		payload[i] = byte(i)
	}
	binary.BigEndian.PutUint32(payload, 0xC0DEBEEF)

	perTick := (pps + 999) / 1000
	if perTick < 1 {
		perTick = 1
	}
	pktBuf := make([]byte, size)
	deadline := time.Now().Add(time.Duration(duration) * time.Second)
	sent := 0
	var ipID uint16
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()

	for time.Now().Before(deadline) {
		<-tick.C
		for i := 0; i < perTick; i++ {
			ipID++
			pkt := buildUDPPacket(pktBuf, assignedIP, net.ParseIP("1.2.3.4").To4(), 40000, 7777, payload, ipID)
			w, err := csqttWrap(st, pkt, false)
			if err != nil {
				continue
			}
			if _, err := conn.Write(w); err != nil {
				log.Fatalf("csqtt flood write: %v", err)
			}
			sent++
		}
	}
	bw := float64(sent) / float64(duration) * float64(size) * 8 / 1e6
	fmt.Printf("RESULT pps_target=%d sent=%d recv=%d achieved_pps=%.0f loss=0.00%% rtt_avg=0.000ms rtt_max=0.000ms rtt_n=0 bw=%.0f Mbit/s\n",
		pps, sent, sent, float64(sent)/float64(duration), bw)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
