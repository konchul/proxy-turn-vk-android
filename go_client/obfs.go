// SPDX-License-Identifier: MIT
// obfs.go — WebRTC SRTP-like obfuscation for DTLS traffic
// Each UDP packet is wrapped in an RTP header making it indistinguishable
// from a real WebRTC OPUS audio stream to DPI systems.
//
// Packet format:
//   [RTP Header 12 bytes][ChaCha20-Poly1305 payload+tag][Padding 0-N bytes][PadLen 1 byte]
//
// The RTP header fields (SSRC + SeqNum + Timestamp) form the 12-byte AEAD
// nonce, so no separate nonce prefix is needed. The 12-byte header is the
// AEAD's associated data.
//
// Unwrap still recognizes a 24-byte (base + RFC 8285 one-byte-header
// extension) variant on receive by checking the X bit — that longer format
// existed briefly and some deployed servers may still send it — but this
// client always WRITES the plain 12-byte form.

package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
)

var aeadCache sync.Map // ключ: struct{cipher obfsCipher; key [wrapKeyLen]byte}

// obfsCipher — AEAD-алгоритм obfs-слоя; фрейминг одинаков, отличается шифр.
// ChaCha20 — исходный протокол (все порты, кроме 46000), AES-256-GCM — новый
// (аппаратный AES на сервере и на телефонах с ARMv8 crypto extensions).
type obfsCipher int

const (
	obfsChaCha20 obfsCipher = iota
	obfsAESGCM
)

// activeObfsCipher выбирается в main.go по порту пира (46000 = AES).
var activeObfsCipher = obfsChaCha20

const replayWindowSpan = uint64(4096 * 961)
const replaySlots = 8192 // степень двойки; в пакетах вдвое больше span (~4096)

// replayWindow — анти-replay фильтр на кольце фиксированного размера: O(1)
// на пакет, ноль аллокаций, без map-чисток (прежняя map[nonce]ts раз в 8192
// пакета делала полный проход под локом — спайк задержки). ts сервера строго
// монотонен по счётчику пакетов, поэтому extended-ts биективен пакету; слот
// переиспользуется через 8192 пакета — дальше span, и такие повторы отсекает
// span-проверка. accept() зовётся из единственного reader'а сессии.
type replayWindow struct {
	seen        [replaySlots]uint64 // extended ts последнего пакета в слоте
	occupied    [replaySlots / 64]uint64
	ssrc        uint32
	highestTime uint64
	initialized bool
}

func (w *replayWindow) accept(wire []byte) bool {
	if len(wire) < rtpHeaderLenLegacy {
		return false
	}
	ssrc := binary.BigEndian.Uint32(wire[8:12])
	ts := binary.BigEndian.Uint32(wire[4:8])
	if !w.initialized {
		w.ssrc = ssrc
		w.highestTime = uint64(ts)
		w.initialized = true
	} else if w.ssrc != ssrc {
		return false
	}
	base := w.highestTime &^ uint64(0xffffffff)
	extended := base | uint64(ts)
	if extended+(1<<31) < w.highestTime {
		extended += 1 << 32
	} else if extended > w.highestTime+(1<<31) && extended >= 1<<32 {
		extended -= 1 << 32
	}
	if extended+replayWindowSpan < w.highestTime {
		return false
	}
	idx := int(extended & (replaySlots - 1))
	word, bit := idx/64, uint(idx%64)
	if w.occupied[word]&(1<<bit) != 0 && w.seen[idx] == extended {
		return false // точный повтор
	}
	if extended > w.highestTime {
		w.highestTime = extended
	}
	w.seen[idx] = extended
	w.occupied[word] |= 1 << bit
	return true
}

func getAEAD(key []byte) (cipher.AEAD, error) {
	return getAEADFor(key, activeObfsCipher)
}

func getAEADFor(key []byte, c obfsCipher) (cipher.AEAD, error) {
	if len(key) != wrapKeyLen {
		return nil, fmt.Errorf("obfs: key must be %d bytes", wrapKeyLen)
	}
	// Ключ кэша — массив, а не string(key): string конвертация аллоцировала
	// на КАЖДЫЙ пакет горячего пути.
	var ck = struct {
		c obfsCipher
		k [wrapKeyLen]byte
	}{c, [wrapKeyLen]byte{}}
	copy(ck.k[:], key)
	if val, ok := aeadCache.Load(ck); ok {
		return val.(cipher.AEAD), nil
	}
	var aead cipher.AEAD
	var err error
	if c == obfsAESGCM {
		block, blockErr := aes.NewCipher(key)
		if blockErr != nil {
			return nil, blockErr
		}
		aead, err = cipher.NewGCM(block)
	} else {
		aead, err = chacha20poly1305.New(key)
	}
	if err != nil {
		return nil, err
	}
	aeadCache.Store(ck, aead)
	return aead, nil
}

// ─── fastPRNG (несекретная случайность для padding) ───

// fastPRNG — xorshift64* для несекретной случайности (длина и байты RTP-
// padding — маскировочный шум, не криптоматериал). crypto/rand.Read здесь
// стоил два getrandom-сисколла на каждый исходящий пакет — на телефоне это
// заметный вклад в CPU и расход батареи.
type fastPRNG struct {
	state uint64
}

func newFastPRNG() fastPRNG {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		s := uint64(time.Now().UnixNano())
		if s == 0 {
			s = 0x853c49e6748fea9b
		}
		return fastPRNG{state: s}
	}
	s := binary.LittleEndian.Uint64(buf[:])
	if s == 0 {
		s = 0x853c49e6748fea9b
	}
	return fastPRNG{state: s}
}

func (p *fastPRNG) next() uint32 {
	x := p.state
	x ^= x >> 12
	x ^= x << 25
	x ^= x >> 27
	p.state = x
	return uint32((x * 0x2545F4914F6CDD1D) >> 32)
}

func (p *fastPRNG) fill(b []byte) {
	for i := range b {
		b[i] = byte(p.next())
	}
}

// ─── Configuration ───

// ObfsConfig holds per-session obfuscation parameters.
type ObfsConfig struct {
	SSRC        uint32 // Synchronization Source — random per session
	PayloadType uint8  // RTP payload type (111 = OPUS dynamic)
	PaddingMax  int    // Max random padding bytes appended
}

// NewObfsConfig creates a config with random SSRC and sane defaults.
// mode: "audio" (OPUS-like, PT 111) or "video" (H264-like, PT 96).
func NewObfsConfig(mode string) (*ObfsConfig, error) {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return nil, err
	}

	pt := uint8(111)
	pad := 24
	if normalizeObfsMode(mode) == "video" {
		pt = 96
		pad = 60
	}

	return &ObfsConfig{
		SSRC:        binary.BigEndian.Uint32(buf[:]),
		PayloadType: pt,
		PaddingMax:  pad,
	}, nil
}

func normalizeObfsMode(mode string) string {
	if strings.EqualFold(strings.TrimSpace(mode), "video") {
		return "video"
	}
	return "audio"
}

// ─── Per-direction state (sequence + timestamp counters) ───

// ObfsState tracks monotonically increasing RTP sequence number and timestamp using a 48-bit packet counter.
type ObfsState struct {
	mu      sync.Mutex
	prng    fastPRNG
	initSeq uint16
	initTs  uint32
	count   uint64
}

// NewObfsState creates a state with random initial seq/ts and count=0.
func NewObfsState() (*ObfsState, error) {
	var buf [6]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return nil, err
	}
	return &ObfsState{
		prng:    newFastPRNG(),
		initSeq: binary.BigEndian.Uint16(buf[0:2]),
		initTs:  binary.BigEndian.Uint32(buf[2:6]),
		count:   0,
	}, nil
}

// ─── Nonce derivation ───

// obfsFillNonce deterministically fills a 12-byte AEAD nonce from RTP fields
// into the caller's stack buffer (прежняя obfsBuildNonce аллоцировала 12 байт
// на каждый пакет в обе стороны).
//
//	[SSRC 4B][SeqNum 2B][0x00 0x00][Timestamp 4B]
func obfsFillNonce(nonce *[12]byte, ssrc uint32, seq uint16, ts uint32) {
	binary.BigEndian.PutUint32(nonce[0:4], ssrc)
	binary.BigEndian.PutUint16(nonce[4:6], seq)
	// nonce[6], nonce[7] = 0x00 — zero padding for unique nonce space
	binary.BigEndian.PutUint32(nonce[8:12], ts)
}

// rtpHeaderLenFull is the base 12-byte RTP header plus a one-byte-header RTP
// extension (RFC 8285) carrying abs-send-time (3 bytes) and
// transport-wide-cc (2 bytes), padded to a 4-byte boundary — the same shape
// real WebRTC clients (and VK calls) send on essentially every packet.
// rtpHeaderLenLegacy is the bare 12-byte header (no extension), for
// compatibility with servers running before this extension was added.
const (
	rtpHeaderLenFull   = 24
	rtpHeaderLenLegacy = 12
)

// ─── Wrap (encrypt + add RTP header) ───

// obfsWrapPacket wraps a plaintext payload into an RTP-like packet with authenticated encryption.
// The output looks like:
//
//	[V=2,P=1,X=0,CC=0 | PT | SeqNum | Timestamp | SSRC | encrypted_payload | padding | padLen]
func obfsWrapPacket(key, payload []byte, cfg *ObfsConfig, state *ObfsState) ([]byte, error) {
	return obfsWrapPacketBuf(key, payload, cfg, state, nil)
}

// obfsWrapPacketBuf — вариант с переиспользуемым выходным буфером: горячий
// путь (обёртка каждого пакета) раньше аллоцировал out заново. dst=nil
// аллоцирует как раньше.
func obfsWrapPacketBuf(key, payload []byte, cfg *ObfsConfig, state *ObfsState, dst []byte) ([]byte, error) {
	if len(key) != wrapKeyLen {
		return nil, fmt.Errorf("obfs: key must be %d bytes (got %d)", wrapKeyLen, len(key))
	}
	if len(payload) == 0 {
		return nil, errors.New("obfs: empty payload")
	}

	state.mu.Lock()
	c := state.count
	state.count++
	state.mu.Unlock()

	seq := state.initSeq + uint16(c)
	ts := state.initTs + uint32(c)*960 + uint32(c>>16)

	// Build nonce from RTP fields (на стеке)
	var nonce [12]byte
	obfsFillNonce(&nonce, cfg.SSRC, seq, ts)

	// Determine padding (несекретная случайность — fastPRNG)
	padRand := 0
	if cfg.PaddingMax > 0 {
		padRand = int(state.prng.next()) % cfg.PaddingMax
	}
	padTotal := padRand + 1 // +1 for the length byte itself

	headerLen := rtpHeaderLenLegacy

	// Allocate output: header + payload + AEAD tag + padTotal
	outLen := headerLen + len(payload) + chacha20poly1305.Overhead + padTotal
	out := dst
	if cap(out) < outLen {
		out = make([]byte, outLen)
	} else {
		out = out[:outLen]
	}

	// RTP Header (12 bytes, no extension).
	// Byte 0 bit layout: V(2) P(1) X(1) CC(4) — masks 0xC0/0x20/0x10/0x0F.
	out[0] = 0x80 | 0x20 // V=2, P=1 (padding present), X=0 (no extension)
	out[1] = cfg.PayloadType & 0x7F
	binary.BigEndian.PutUint16(out[2:4], seq)
	binary.BigEndian.PutUint32(out[4:8], ts)
	binary.BigEndian.PutUint32(out[8:12], cfg.SSRC)

	aead, err := getAEAD(key)
	if err != nil {
		return nil, fmt.Errorf("obfs: cipher init: %w", err)
	}
	sealed := aead.Seal(out[headerLen:headerLen], nonce[:], payload, out[:headerLen])

	// Random padding bytes
	padStart := headerLen + len(sealed)
	if padRand > 0 {
		state.prng.fill(out[padStart : padStart+padRand])
	}

	// Last byte = total padding count (RFC 3550 §5.1)
	out[outLen-1] = byte(padTotal)

	return out, nil
}

// ─── Unwrap (strip RTP header + decrypt) ───

// obfsUnwrapPacket strips the RTP header+extension, removes padding, and
// decrypts the payload. Returns number of plaintext bytes written to dst.
func obfsUnwrapPacket(key, wire, dst []byte) (int, error) {
	if len(key) != wrapKeyLen {
		return 0, fmt.Errorf("obfs: key must be %d bytes (got %d)", wrapKeyLen, len(key))
	}
	if len(wire) < rtpHeaderLenLegacy+1 { // minimum: bare 12-byte header + at least 1 byte
		return 0, errors.New("obfs: packet too short")
	}

	// Validate RTP version
	if (wire[0] >> 6) != 2 {
		return 0, errors.New("obfs: not RTP v2")
	}

	// Header length is determined by the X bit (extension present) of the
	// INCOMING packet, not by our own LegacyHeader config — this lets a
	// single client transparently talk to both old (12-byte, no extension)
	// and new (24-byte, with extension) servers without needing to know in
	// advance which one it's receiving from.
	headerLen := rtpHeaderLenLegacy
	if wire[0]&0x10 != 0 { // X bit
		headerLen = rtpHeaderLenFull
	}
	if len(wire) < headerLen+1 {
		return 0, errors.New("obfs: packet too short for declared extension")
	}

	// Extract RTP fields for nonce
	seq := binary.BigEndian.Uint16(wire[2:4])
	ts := binary.BigEndian.Uint32(wire[4:8])
	ssrc := binary.BigEndian.Uint32(wire[8:12])

	// Handle padding (P bit)
	payloadEnd := len(wire)
	if wire[0]&0x20 != 0 {
		padLen := int(wire[len(wire)-1])
		if padLen == 0 || padLen > payloadEnd-headerLen {
			return 0, fmt.Errorf("obfs: invalid padding length %d", padLen)
		}
		payloadEnd -= padLen
	}

	ciphertextLen := payloadEnd - headerLen
	if ciphertextLen <= chacha20poly1305.Overhead {
		return 0, errors.New("obfs: no payload after stripping header/padding")
	}
	if ciphertextLen-chacha20poly1305.Overhead > len(dst) {
		return 0, errors.New("obfs: dst buffer too small")
	}

	// Build nonce and decrypt (на стеке)
	var nonce [12]byte
	obfsFillNonce(&nonce, ssrc, seq, ts)
	aead, err := getAEAD(key)
	if err != nil {
		return 0, fmt.Errorf("obfs: cipher init: %w", err)
	}
	plain, err := aead.Open(dst[:0], nonce[:], wire[headerLen:payloadEnd], wire[:headerLen])
	if err != nil {
		return 0, fmt.Errorf("obfs: auth: %w", err)
	}

	return len(plain), nil
}

// ─── Detection ───

// obfsIsRTPPacket checks if a raw UDP packet looks like our obfuscated RTP.
// Used by the server and client to reject non-obfuscated packets.
func obfsIsRTPPacket(wire []byte) bool {
	if len(wire) < rtpHeaderLenLegacy+1 {
		return false
	}
	// RTP version must be 2
	if (wire[0] >> 6) != 2 {
		return false
	}
	// Our payload types: 111 (audio) or 96 (video)
	pt := wire[1] & 0x7F
	return pt == 111 || pt == 96
}
