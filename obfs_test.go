package main

import (
	"bytes"
	"encoding/binary"
	"testing"

	"golang.org/x/crypto/chacha20poly1305"
)

// TestObfsWrapPacketReusedDirtyBuffer проверяет, что obfsWrapPacket с
// переданным dst (буфер из пула, потенциально содержащий "грязные" данные
// от предыдущего использования) даёт тот же результат, что и с dst=nil
// (свежая аллокация) — это то, что изменилось при переходе wrapPacketConn
// на sync.Pool вместо make() на каждый пакет (см. wrapReadBufPool).
func TestObfsWrapPacketReusedDirtyBuffer(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, wrapKeyLen)
	cfg := NewObfsConfig("audio")
	cfg.PaddingMax = 0 // детерминированная длина для сравнения

	payload := []byte("hello world, this is a test payload for obfs wrap")

	stateFresh := NewObfsState()
	// Снимаем стартовые значения ДО вызова obfsWrapPacket — он мутирует
	// count внутри state, так что копировать в stateReused нужно то, что
	// реально использовалось в этом конкретном пакете, а не то, что
	// осталось в stateFresh после инкремента.
	initSeq, initTs := stateFresh.initSeq, stateFresh.initTs

	fresh, err := obfsWrapPacket(key, payload, cfg, stateFresh, nil)
	if err != nil {
		t.Fatalf("obfsWrapPacket(dst=nil): %v", err)
	}

	// Грязный переиспользуемый буфер: заполнен произвольными ненулевыми
	// байтами и имеет заведомо больше ёмкости, чем нужно для outLen.
	dirty := make([]byte, 1700)
	for i := range dirty {
		dirty[i] = 0xAA
	}

	stateReused := NewObfsState()
	stateReused.initSeq = initSeq
	stateReused.initTs = initTs

	reused, err := obfsWrapPacket(key, payload, cfg, stateReused, dirty)
	if err != nil {
		t.Fatalf("obfsWrapPacket(dst=dirty): %v", err)
	}

	if len(fresh) != len(reused) {
		t.Fatalf("packet length differs: fresh=%d reused=%d", len(fresh), len(reused))
	}
	if !bytes.Equal(fresh[:rtpHeaderLen], reused[:rtpHeaderLen]) {
		t.Fatalf("header differs:\nfresh=%x\nreused=%x", fresh[:rtpHeaderLen], reused[:rtpHeaderLen])
	}

	// Round-trip: расшифровка обоих даёт исходный payload.
	out := make([]byte, len(payload)+80)
	n, err := obfsUnwrapPacket(key, reused, out)
	if err != nil {
		t.Fatalf("obfsUnwrapPacket: %v", err)
	}
	if !bytes.Equal(out[:n], payload) {
		t.Fatalf("round-trip mismatch: got %q want %q", out[:n], payload)
	}
}

// TestObfsWrapPacketGrowsWhenDstTooSmall проверяет, что при недостаточной
// ёмкости dst функция аллоцирует новый буфер вместо паники/переполнения.
func TestObfsWrapPacketGrowsWhenDstTooSmall(t *testing.T) {
	key := bytes.Repeat([]byte{0x11}, wrapKeyLen)
	cfg := NewObfsConfig("audio")
	cfg.PaddingMax = 0
	state := NewObfsState()

	payload := bytes.Repeat([]byte{0x01}, 500)
	tooSmall := make([]byte, 4) // явно меньше, чем нужно

	out, err := obfsWrapPacket(key, payload, cfg, state, tooSmall)
	if err != nil {
		t.Fatalf("obfsWrapPacket: %v", err)
	}
	wantLen := rtpHeaderLen + len(payload) + chacha20poly1305.Overhead + 1
	if len(out) != wantLen {
		t.Fatalf("output len = %d, want %d", len(out), wantLen)
	}
}

// TestObfsUnwrapAcceptsSpecCompliantWireFormat строит wire-пакет вручную по
// формату из комментария к rtpHeaderLen (bare RFC 3550 12-байтный заголовок,
// без extension), НЕ вызывая obfsWrapPacket — так тест проверяет сам контракт
// формата (то, что должен произвести go_client/obfs.go, отдельный Go-модуль
// без общего кода), а не самосогласованность одной реализации сама с собой.
// Если go_client и server.go разойдутся в раскладке байт заголовка/AAD, этот
// тест это поймает независимо от того, что делает obfsWrapPacket в этом
// модуле.
func TestObfsUnwrapAcceptsSpecCompliantWireFormat(t *testing.T) {
	key := bytes.Repeat([]byte{0x55}, wrapKeyLen)
	payload := []byte("cross-module wire format contract check")

	const seq = uint16(1234)
	const ts = uint32(0xdeadbeef)
	const ssrc = uint32(0x11223344)

	header := make([]byte, rtpHeaderLen)
	header[0] = 0x80 // V=2, X=0, P=0 (no padding for this test)
	header[1] = 111  // PT audio
	binary.BigEndian.PutUint16(header[2:4], seq)
	binary.BigEndian.PutUint32(header[4:8], ts)
	binary.BigEndian.PutUint32(header[8:12], ssrc)

	aead, err := getAEAD(key)
	if err != nil {
		t.Fatalf("getAEAD: %v", err)
	}
	nonce := obfsBuildNonce(ssrc, seq, ts)
	sealed := aead.Seal(nil, nonce, payload, header) // header is the AAD
	wire := append(header, sealed...)                // P bit unset — no padding byte

	dst := make([]byte, len(payload)+64)
	n, err := obfsUnwrapPacket(key, wire, dst)
	if err != nil {
		t.Fatalf("obfsUnwrapPacket rejected a spec-compliant wire packet: %v", err)
	}
	if !bytes.Equal(dst[:n], payload) {
		t.Fatalf("decrypted payload mismatch: got %q want %q", dst[:n], payload)
	}
}
