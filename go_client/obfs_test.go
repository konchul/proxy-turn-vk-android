package main

import (
	"bytes"
	"testing"
)

// TestObfsWrapUnwrapRoundTrip проверяет, что wrap->unwrap по-прежнему даёт
// исходный payload побайтово — заголовок целиком участвует как AEAD AAD,
// малейшее рассогласование между Wrap и Unwrap ломает аутентификацию молча.
func TestObfsWrapUnwrapRoundTrip(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, wrapKeyLen)
	cfg := NewObfsConfig("audio")
	state := NewObfsState()

	payload := []byte("hello world, this is a test payload for obfs wrap")

	wire, err := obfsWrapPacket(key, payload, cfg, state)
	if err != nil {
		t.Fatalf("obfsWrapPacket: %v", err)
	}
	if len(wire) < rtpHeaderLenLegacy {
		t.Fatalf("wire len = %d, shorter than rtpHeaderLenLegacy=%d", len(wire), rtpHeaderLenLegacy)
	}

	dst := make([]byte, len(payload)+64)
	n, err := obfsUnwrapPacket(key, wire, dst)
	if err != nil {
		t.Fatalf("obfsUnwrapPacket: %v", err)
	}
	if !bytes.Equal(dst[:n], payload) {
		t.Fatalf("round-trip mismatch: got %q want %q", dst[:n], payload)
	}
}

// TestObfsWrapHeaderShape проверяет биты заголовка (V=2,P=1,X=0) — bare
// 12-байтный RTP-заголовок без one-byte-header extension (RFC 8285), формат,
// понятный всем ранее развёрнутым серверам.
func TestObfsWrapHeaderShape(t *testing.T) {
	key := bytes.Repeat([]byte{0x11}, wrapKeyLen)
	cfg := NewObfsConfig("audio")
	state := NewObfsState()

	wire, err := obfsWrapPacket(key, []byte("x"), cfg, state)
	if err != nil {
		t.Fatalf("obfsWrapPacket: %v", err)
	}

	version := wire[0] >> 6
	if version != 2 {
		t.Fatalf("RTP version = %d, want 2", version)
	}
	extBit := wire[0] & 0x10
	if extBit != 0 {
		t.Fatalf("extension bit (X) set on bare 12-byte header: %08b", wire[0])
	}
}

// TestObfsIsRTPPacketRejectsShortPackets гарантирует, что detection-логика
// учитывает минимальную длину bare-заголовка (rtpHeaderLenLegacy=12) — эта
// проверка нарочно принимает и 12-, и 24-байтные заголовки (см.
// rtpHeaderLenFor/legacy-server compat), полноценная валидация длины под
// конкретный формат делается уже в obfsUnwrapPacket по X-биту.
func TestObfsIsRTPPacketRejectsShortPackets(t *testing.T) {
	short := make([]byte, rtpHeaderLenLegacy) // ровно на границе, без хотя бы 1 байта payload
	short[0] = 0x90
	short[1] = 111
	if obfsIsRTPPacket(short) {
		t.Fatalf("obfsIsRTPPacket accepted a packet with no room for payload (len=%d)", len(short))
	}

	valid := make([]byte, rtpHeaderLenLegacy+1)
	valid[0] = 0x90
	valid[1] = 111
	if !obfsIsRTPPacket(valid) {
		t.Fatalf("obfsIsRTPPacket rejected a minimally valid packet (len=%d)", len(valid))
	}
}

// TestObfsWrapUnwrapVideoMode проверяет тот же round-trip для video-режима
// (другой PayloadType/PaddingMax) — оба режима используют один и тот же
// заголовок/AAD-путь, но стоит подтвердить явно.
func TestObfsWrapUnwrapVideoMode(t *testing.T) {
	key := bytes.Repeat([]byte{0x77}, wrapKeyLen)
	cfg := NewObfsConfig("video")
	state := NewObfsState()

	payload := bytes.Repeat([]byte{0xAB}, 500)
	wire, err := obfsWrapPacket(key, payload, cfg, state)
	if err != nil {
		t.Fatalf("obfsWrapPacket: %v", err)
	}

	dst := make([]byte, len(payload)+64)
	n, err := obfsUnwrapPacket(key, wire, dst)
	if err != nil {
		t.Fatalf("obfsUnwrapPacket: %v", err)
	}
	if !bytes.Equal(dst[:n], payload) {
		t.Fatalf("round-trip mismatch in video mode")
	}
}

// TestObfsWrapUnwrapMultiplePackets проверяет несколько последовательных
// пакетов в одной сессии (seq/ts инкрементируются, transport-cc в extension
// тоже растёт вместе со счётчиком) — каждый должен расшифровываться
// независимо и корректно.
func TestObfsWrapUnwrapMultiplePackets(t *testing.T) {
	key := bytes.Repeat([]byte{0x99}, wrapKeyLen)
	cfg := NewObfsConfig("audio")
	state := NewObfsState()

	for i := 0; i < 20; i++ {
		payload := bytes.Repeat([]byte{byte(i)}, 10+i)
		wire, err := obfsWrapPacket(key, payload, cfg, state)
		if err != nil {
			t.Fatalf("obfsWrapPacket(#%d): %v", i, err)
		}
		dst := make([]byte, len(payload)+64)
		n, err := obfsUnwrapPacket(key, wire, dst)
		if err != nil {
			t.Fatalf("obfsUnwrapPacket(#%d): %v", i, err)
		}
		if !bytes.Equal(dst[:n], payload) {
			t.Fatalf("round-trip mismatch at packet #%d", i)
		}
	}
}

