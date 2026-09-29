package main

// Юнит-тесты GSO-сегментации: чистые функции без TUN — собираются отдельно и
// запускаются на тестовой машине (go test -c).

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// ipChecksumValid проверяет корректность IPv4-чексуммы заголовка.
func ipChecksumValid(hdr []byte) bool {
	return foldSum(rawSum(hdr)) == 0
}

// l4ChecksumStored — пересчёт TCP/UDP чексуммы с нулевым полем и сверка со stored.
func l4ChecksumValid(pkt []byte, ipHdrLen int) bool {
	ver := pkt[0] >> 4
	var src, dst []byte
	proto := pkt[9]
	l4Len := len(pkt) - ipHdrLen
	if ver == 4 {
		src = pkt[12:16]
		dst = pkt[16:20]
	} else {
		src = pkt[8:24]
		dst = pkt[24:40]
		proto = 6
		if pkt[6] == 17 {
			proto = 17
		}
	}
	csumOff := ipHdrLen + 16
	if pkt[ipHdrLen]>>4 == 5 && ver == 4 && proto == 17 {
		csumOff = ipHdrLen + 6
	}
	if ver == 6 {
		csumOff = ipHdrLen + 16
		if pkt[6] == 17 {
			csumOff = ipHdrLen + 6
		}
	}
	// UDP-заголовок: поле длины на +4
	if proto == 17 {
		l4Len = int(binary.BigEndian.Uint16(pkt[ipHdrLen+4 : ipHdrLen+6]))
	}
	stored := binary.BigEndian.Uint16(pkt[csumOff : csumOff+2])
	pkt[csumOff], pkt[csumOff+1] = 0, 0
	sum := pseudoSum(src, dst, byte(proto), l4Len) + rawSum(pkt[ipHdrLen:])
	got := foldSum(sum)
	pkt[csumOff], pkt[csumOff+1] = byte(stored>>8), byte(stored)
	return got == stored
}

func buildTCPGSOFrame(payloadLen int, gsoSize int) ([]byte, gsoInfo) {
	payload := make([]byte, payloadLen)
	for i := range payload {
		payload[i] = byte(i * 7)
	}
	iph := make([]byte, 20)
	iph[0] = 0x45
	binary.BigEndian.PutUint16(iph[2:4], uint16(20+20+payloadLen))
	binary.BigEndian.PutUint16(iph[4:6], 0x1234)
	iph[6] = 0x40 // DF
	iph[8] = 64
	iph[9] = 6
	copy(iph[12:16], []byte{1, 2, 3, 4})
	copy(iph[16:20], []byte{5, 6, 7, 8})
	tcp := make([]byte, 20)
	tcp[12] = 0x50 // doff=5
	binary.BigEndian.PutUint32(tcp[4:8], 1000)
	frame := append(append(iph, tcp...), payload...)
	info := gsoInfo{
		gsoType:    gsoTypeTCPv4,
		gsoSize:    uint16(gsoSize),
		csumStart:  20,
		csumOffset: 20 + 16,
	}
	return frame, info
}

func TestSegmentTCPv4(t *testing.T) {
	payloadLen, gsoSize := 4000, 1000
	frame, info := buildTCPGSOFrame(payloadLen, gsoSize)

	var segs [][]byte
	err := segmentGSO(frame, info, 1300, func(seg []byte) {
		cp := make([]byte, len(seg))
		copy(cp, seg)
		segs = append(segs, cp)
	})
	if err != nil {
		t.Fatalf("segmentGSO: %v", err)
	}
	if len(segs) != 4 {
		t.Fatalf("ожидалось 4 сегмента, получено %d", len(segs))
	}
	seqWant := uint32(1000)
	for i, seg := range segs {
		if len(seg) != 20+20+gsoSize {
			t.Fatalf("сегмент %d: длина %d, ожидалось %d", i, len(seg), 20+20+gsoSize)
		}
		if !ipChecksumValid(seg[:20]) {
			t.Errorf("сегмент %d: некорректная IPv4 чексумма", i)
		}
		if !l4ChecksumValid(seg, 20) {
			t.Errorf("сегмент %d: некорректная TCP чексумма", i)
		}
		seq := binary.BigEndian.Uint32(seg[24:28])
		if seq != seqWant {
			t.Errorf("сегмент %d: seq=%d, ожидалось %d", i, seq, seqWant)
		}
		seqWant += uint32(gsoSize)
		wantPayload := frame[40+i*gsoSize : 40+(i+1)*gsoSize]
		if !bytes.Equal(seg[40:], wantPayload) {
			t.Errorf("сегмент %d: payload не совпадает", i)
		}
	}
	// последний сегмент: PSH установлен
	if segs[3][33]&0x08 == 0 {
		t.Errorf("последний сегмент без PSH")
	}
	// промежуточные — без PSH
	for i := 0; i < 3; i++ {
		if segs[i][33]&0x08 != 0 {
			t.Errorf("сегмент %d: лишний PSH", i)
		}
	}
}

func TestSegmentTCPv4SubSplit(t *testing.T) {
	// gso_size больше нашего maxSeg — порезка на подсегменты
	frame, info := buildTCPGSOFrame(3000, 1400)
	count := 0
	lastLen := 0
	err := segmentGSO(frame, info, 1300, func(seg []byte) {
		count++
		lastLen = len(seg)
	})
	if err != nil {
		t.Fatalf("segmentGSO: %v", err)
	}
	// mss = 1300-40 = 1260 < 1400 → сегменты по 1260, последний 3000-3*1260=480... 3 сегмента
	wantSegs := (3000 + 1259) / 1260
	if count != wantSegs {
		t.Fatalf("сегментов %d, ожидалось %d", count, wantSegs)
	}
	if lastLen != 20+20+3000-1260*2 {
		t.Errorf("последний сегмент %d байт — некорректно", lastLen)
	}
}

func TestSegmentUDPL4(t *testing.T) {
	payloadLen, gsoSize := 3200, 800
	payload := make([]byte, payloadLen)
	for i := range payload {
		payload[i] = byte(i)
	}
	iph := make([]byte, 20)
	iph[0] = 0x45
	iph[8] = 64
	iph[9] = 17
	copy(iph[12:16], []byte{9, 9, 9, 9})
	copy(iph[16:20], []byte{10, 10, 10, 10})
	udp := make([]byte, 8)
	binary.BigEndian.PutUint16(udp[0:2], 7777)
	binary.BigEndian.PutUint16(udp[2:4], 40000)
	binary.BigEndian.PutUint16(udp[4:6], uint16(8+payloadLen))
	frame := append(append(iph, udp...), payload...)
	info := gsoInfo{
		gsoType:    gsoTypeUDPL4,
		gsoSize:    uint16(gsoSize),
		csumStart:  20,
		csumOffset: 20 + 6,
	}
	var segs [][]byte
	err := segmentGSO(frame, info, 1300, func(seg []byte) {
		cp := make([]byte, len(seg))
		copy(cp, seg)
		segs = append(segs, cp)
	})
	if err != nil {
		t.Fatalf("segmentGSO: %v", err)
	}
	if len(segs) != 4 {
		t.Fatalf("ожидалось 4 сегмента, получено %d", len(segs))
	}
	for i, seg := range segs {
		if len(seg) != 20+8+gsoSize {
			t.Fatalf("сегмент %d: длина %d", i, len(seg))
		}
		if got := binary.BigEndian.Uint16(seg[24:26]); got != uint16(8+gsoSize) {
			t.Errorf("сегмент %d: UDP length=%d", i, got)
		}
		if !l4ChecksumValid(seg, 20) {
			t.Errorf("сегмент %d: некорректная UDP чексумма", i)
		}
		if !bytes.Equal(seg[28:], payload[i*gsoSize:(i+1)*gsoSize]) {
			t.Errorf("сегмент %d: payload не совпадает", i)
		}
	}
}

func TestFinalizeChecksumTCP(t *testing.T) {
	// одиночный пакет с NEEDS_CSUM: после финализации чексумма валидна
	frame, _ := buildTCPGSOFrame(1200, 0)
	info := gsoInfo{flags: vnetFlagNeedsCsum, csumStart: 20, csumOffset: 36}
	if err := finalizeChecksum(frame, info); err != nil {
		t.Fatalf("finalizeChecksum: %v", err)
	}
	if !l4ChecksumValid(frame, 20) {
		t.Fatal("после финализации TCP чексумма невалидна")
	}
}
