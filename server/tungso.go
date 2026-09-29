package main

// tungso.go — поддержка IFF_VNET_HDR на raw-TUN (GSO/GRO).
//
// Даунлинк: ядро отдаёт GSO-суперкадры (GRO-агрегированный TCP с eth0,
// UDP_SEGMENT-датаграммы) одним Read — virtio_net_hdr описывает тип и размер
// сегмента. Мы режем суперкадр в юзерспейсе на IP-пакеты ≤ rawMTU и отдаём
// в клиентские воркеры как раньше. Аплинк: каждая запись получает 10-байтовый
// virtio-заголовок (нули = без GSO, чексуммы в пакете валидны — семантика
// та же, что была без VNET_HDR), зато ядро может GRO-склеивать TCP клиента.
//
// ВАЖНО про чексуммы: GSO-кадры приходят с CHECKSUM_PARTIAL
// (vnet flags = NEEDS_CSUM) — поле чексуммы в кадре НЕ валидно, полная сумма
// считается ядром на выходе из интерфейса. TUN — не выход: пересчёт делаем мы
// на каждый сегмент. Для одиночных пакетов с NEEDS_CSUM — финализация суммы.

import (
	"encoding/binary"
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// vnetHdrLen — размер virtio_net_hdr без mrg_rxbuffer (10 байт, little-endian).
const vnetHdrLen = 10

const (
	vnetFlagNeedsCsum = 1 << 0
	vnetFlagDataValid = 1 << 1

	gsoTypeNone   = 0
	gsoTypeTCPv4  = 1
	gsoTypeUDP    = 3 // legacy UFO
	gsoTypeTCPv6  = 4
	gsoTypeUDPL4  = 5
	gsoTypeECNBit = 0x80
)

// maxGSOFrame — максимум кадра: 65535 payload + vnet hdr + запас.
const maxGSOFrame = vnetHdrLen + 65535 + 16

// enableTUNOffloads включает GSO-фичи устройства. Без этого ioctl ядро
// сегментирует всё само и суперкадры не приходят.
func enableTUNOffloads(fd int) error {
	// TUN_F_UFO на новых ядрах маппится в GSO_UDP_L4; на старых — EINVAL,
	// тогда пробуем без него.
	full := unix.TUN_F_CSUM | unix.TUN_F_TSO4 | unix.TUN_F_TSO6 | unix.TUN_F_TSO_ECN | unix.TUN_F_UFO
	// TUNSETOFFLOAD = _IOW('T', 208, unsigned int): значение передаётся ПО
	// ЗНАЧЕНИЮ (IoctlSetInt), а не указателем — иначе EINVAL.
	if err := unix.IoctlSetInt(fd, unix.TUNSETOFFLOAD, full); err == nil {
		return nil
	}
	noUFO := unix.TUN_F_CSUM | unix.TUN_F_TSO4 | unix.TUN_F_TSO6 | unix.TUN_F_TSO_ECN
	return unix.IoctlSetInt(fd, unix.TUNSETOFFLOAD, noUFO)
}

// gsoInfo — разобранный virtio_net_hdr.
type gsoInfo struct {
	flags      uint8
	gsoType    uint8
	hdrLen     uint16
	gsoSize    uint16
	csumStart  uint16
	csumOffset uint16
}

func parseVnetHdr(hdr []byte) gsoInfo {
	return gsoInfo{
		flags:      hdr[0],
		gsoType:    hdr[1],
		hdrLen:     binary.LittleEndian.Uint16(hdr[2:4]),
		gsoSize:    binary.LittleEndian.Uint16(hdr[4:6]),
		csumStart:  binary.LittleEndian.Uint16(hdr[6:8]),
		csumOffset: binary.LittleEndian.Uint16(hdr[8:10]),
	}
}

// ─── Интернет-чексуммы ───

// rawSum — 16-битное слагаемое суммы по байтам (odd length: последний байт — старший).
func rawSum(b []byte) uint32 {
	var sum uint32
	n := len(b)
	for i := 0; i+1 < n; i += 2 {
		sum += uint32(b[i])<<8 | uint32(b[i+1])
	}
	if n&1 == 1 {
		sum += uint32(b[n-1]) << 8
	}
	return sum
}

func foldSum(sum uint32) uint16 {
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}

// pseudoSum — сумма псевдозаголовка TCP/UDP (IPv4 или IPv6).
func pseudoSum(srcIP, dstIP []byte, proto byte, l4Len int) uint32 {
	sum := rawSum(srcIP) + rawSum(dstIP)
	sum += uint32(proto)
	sum += uint32(l4Len)
	return sum
}

// finalizeChecksum вычисляет полную транспортную чексумму для пакета с
// NEEDS_CSUM: сумма по [csum_start:end] + псевдозаголовок, результат в
// [csum_offset]. Работает для TCP и UDP, IPv4 и IPv6.
func finalizeChecksum(pkt []byte, info gsoInfo) error {
	if len(pkt) < 20 || int(info.csumStart) > len(pkt) || int(info.csumOffset)+2 > len(pkt) {
		return errors.New("gso: bad csum metadata")
	}
	ver := pkt[0] >> 4
	var proto byte
	var src, dst []byte
	if ver == 4 {
		if len(pkt) < 20 {
			return errors.New("gso: short ipv4")
		}
		ihl := int(pkt[0]&0x0f) * 4
		if int(info.csumStart) < ihl {
			return errors.New("gso: csum_start inside ip hdr")
		}
		proto = pkt[9]
		src = pkt[12:16]
		dst = pkt[16:20]
	} else if ver == 6 {
		if len(pkt) < 40 {
			return errors.New("gso: short ipv6")
		}
		proto = pkt[6]
		src = pkt[8:24]
		dst = pkt[24:40]
	} else {
		return errors.New("gso: unknown ip version")
	}
	l4Len := len(pkt) - int(info.csumStart)
	sum := pseudoSum(src, dst, proto, l4Len) + rawSum(pkt[info.csumStart:])
	// нулевой чексумм-филд уже учтён как 0 — он в csum_offset и должен быть 0
	// при расчёте; на практике там мусор частичной суммы — вычитаем его вклад.
	fieldOff := int(info.csumOffset) - int(info.csumStart)
	if fieldOff >= 0 && fieldOff+2 <= l4Len {
		stored := uint32(pkt[int(info.csumStart)+fieldOff])<<8 | uint32(pkt[int(info.csumStart)+fieldOff+1])
		sum -= stored // вычитаем вместо сложения: поле было включено в rawSum
	}
	binary.BigEndian.PutUint16(pkt[int(info.csumOffset):int(info.csumOffset)+2], foldSum(sum))
	return nil
}

// ─── Сегментация GSO-суперкадров ───

// segmentGSO режет суперкадр на одиночные IP-пакеты (каждый ≤ maxSeg байт)
// и отдаёт через emit. Поддержка: TCPv4, TCPv6, UDP_L4 (UDP over IPv4/IPv6).
// Чексуммы пересчитываются на каждый сегмент (кадр приходит с CHECKSUM_PARTIAL).
func segmentGSO(frame []byte, info gsoInfo, maxSeg int, emit func([]byte)) error {
	if len(frame) < 1 {
		return errors.New("gso: empty frame")
	}
	switch frame[0] >> 4 {
	case 4:
		return segmentGSOv4(frame, info, maxSeg, emit)
	case 6:
		return segmentGSOv6(frame, info, maxSeg, emit)
	default:
		return fmt.Errorf("gso: unknown ip version %d", frame[0]>>4)
	}
}

func segmentGSOv4(frame []byte, info gsoInfo, maxSeg int, emit func([]byte)) error {
	if len(frame) < 20 {
		return errors.New("gso: short ipv4")
	}
	ihl := int(frame[0]&0x0f) * 4
	proto := frame[9]
	switch {
	case proto == 6 && (info.gsoType&^gsoTypeECNBit) == gsoTypeTCPv4:
		return segmentL4(frame, info, ihl, 20, maxSeg, true, emit)
	case proto == 17 && (info.gsoType&^gsoTypeECNBit) == gsoTypeUDPL4:
		return segmentL4(frame, info, ihl, 8, maxSeg, false, emit)
	default:
		return fmt.Errorf("gso: unsupported v4 proto=%d gso=%d", proto, info.gsoType)
	}
}

func segmentGSOv6(frame []byte, info gsoInfo, maxSeg int, emit func([]byte)) error {
	if len(frame) < 40 {
		return errors.New("gso: short ipv6")
	}
	nh := frame[6]
	switch {
	case nh == 6 && (info.gsoType&^gsoTypeECNBit) == gsoTypeTCPv6:
		return segmentL4(frame, info, 40, 20, maxSeg, false, emit)
	case nh == 17 && (info.gsoType&^gsoTypeECNBit) == gsoTypeUDPL4:
		return segmentL4(frame, info, 40, 8, maxSeg, false, emit)
	default:
		return fmt.Errorf("gso: unsupported v6 nh=%d gso=%d", nh, info.gsoType)
	}
}

// segmentL4 — общая сегментация: фиксированные IP-заголовок и L4-заголовок,
// payload режется кусками ≤ mss; seq TCP сдвигается, длины и чексуммы — на
// каждый сегмент. hasIPChecksum: IPv4 да (пересчёт заголовка), IPv6 нет.
// tcpSeq: сдвигать TCP sequence number.
func segmentL4(frame []byte, info gsoInfo, ipHdrLen, l4HdrLen, maxSeg int, hasIPChecksum bool, emit func([]byte)) error {
	l4Off := ipHdrLen
	if len(frame) < l4Off+l4HdrLen {
		return errors.New("gso: frame shorter than headers")
	}
	// doff для TCP
	if l4HdrLen == 20 {
		if len(frame) < l4Off+13 {
			return errors.New("gso: short tcp")
		}
		doff := int(frame[l4Off+12] >> 4)
		if doff < 5 {
			return errors.New("gso: bad tcp doff")
		}
		l4HdrLen = doff * 4
	}
	payload := frame[l4Off+l4HdrLen:]
	totalPayload := len(payload)
	if totalPayload == 0 {
		return errors.New("gso: no payload")
	}
	mss := int(info.gsoSize)
	if mss <= 0 {
		return errors.New("gso: bad gso_size")
	}
	if mss > maxSeg-ipHdrLen-l4HdrLen {
		mss = maxSeg - ipHdrLen - l4HdrLen
	}
	if mss <= 0 {
		return errors.New("gso: mss too small for maxSeg")
	}

	// Копии заголовков (модифицируются по сегментам)
	iph := make([]byte, ipHdrLen)
	copy(iph, frame[:ipHdrLen])
	l4 := make([]byte, l4HdrLen)
	copy(l4, frame[l4Off:l4Off+l4HdrLen])

	seq0 := binary.BigEndian.Uint32(l4[4:8])
	ident := binary.BigEndian.Uint16(iph[4:6])
	proto := byte(6)
	if l4HdrLen == 8 {
		proto = 17
	}
	if frame[0]>>4 == 4 {
		proto = frame[9]
		// во фрагментированных/опционных заголовках протокол тот же
	}

	var srcIP, dstIP []byte
	if frame[0]>>4 == 4 {
		srcIP = iph[12:16]
		dstIP = iph[16:20]
	} else {
		srcIP = iph[8:24]
		dstIP = iph[24:40]
	}

	seg := make([]byte, ipHdrLen+l4HdrLen+mss)
	offset := 0
	for segIdx := 0; offset < totalPayload; segIdx++ {
		chunk := totalPayload - offset
		if chunk > mss {
			chunk = mss
		}
		isLast := offset+chunk == totalPayload
		pktLen := ipHdrLen + l4HdrLen + chunk

		pkt := seg[:pktLen]
		copy(pkt[:ipHdrLen], iph)
		copy(pkt[ipHdrLen:ipHdrLen+l4HdrLen], l4)
		copy(pkt[ipHdrLen+l4HdrLen:], payload[offset:offset+chunk])

		// IP-заголовок
		if frame[0]>>4 == 4 {
			binary.BigEndian.PutUint16(pkt[2:4], uint16(pktLen))
			// ident сдвигается по сегментам (как делает ядро); DF-пакеты не трогаем
			if pkt[6]&0x40 == 0 {
				binary.BigEndian.PutUint16(pkt[4:6], ident+uint16(segIdx))
			}
			pkt[10], pkt[11] = 0, 0
			binary.BigEndian.PutUint16(pkt[10:12], foldSum(rawSum(pkt[:ipHdrLen])))
		}

		// L4-заголовок
		if l4HdrLen == 20 { // TCP
			binary.BigEndian.PutUint32(pkt[ipHdrLen+4:ipHdrLen+8], seq0+uint32(offset))
			// PSH только на последнем сегменте (как tcp_gso_segment в ядре)
			tcpFlags := pkt[ipHdrLen+13]
			if isLast {
				pkt[ipHdrLen+13] = tcpFlags | 0x08
			} else {
				pkt[ipHdrLen+13] = tcpFlags &^ 0x08
			}
		} else { // UDP
			binary.BigEndian.PutUint16(pkt[ipHdrLen+4:ipHdrLen+6], uint16(l4HdrLen+chunk))
		}
		// чексумма L4: полная (кадр приходил с CHECKSUM_PARTIAL);
		// поле checksum: TCP — смещение 16, UDP — 6
		csumOff := ipHdrLen + 16
		if l4HdrLen == 8 {
			csumOff = ipHdrLen + 6
		}
		pkt[csumOff], pkt[csumOff+1] = 0, 0
		sum := pseudoSum(srcIP, dstIP, proto, l4HdrLen+chunk) + rawSum(pkt[ipHdrLen:pktLen])
		binary.BigEndian.PutUint16(pkt[csumOff:csumOff+2], foldSum(sum))

		emit(pkt)
		offset += chunk
	}
	return nil
}

// finalizeIfNeeded — финализация чексуммы одиночного (не-GSO) пакета с
// NEEDS_CSUM. Возвращает пакет как есть, если финализация не требуется.
func finalizeIfNeeded(pkt []byte, info gsoInfo) []byte {
	if info.flags&vnetFlagNeedsCsum == 0 {
		return pkt
	}
	if err := finalizeChecksum(pkt, info); err != nil {
		// битая метадата — пакет придётся выкинуть, пусть вызывающий увидит
		// по логу; возвращаем nil как маркер
		return nil
	}
	return pkt
}
