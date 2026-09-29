package main

const (
	wgIfaceName           = "wdtt0"
	wgServerAddr          = "10.66.66.1"
	wgServerCIDR          = wgServerAddr + "/16"
	defaultInternalWGPort = 56001
	wgMTU                 = 1280
	keepalive             = 25

	// Raw-IP роутер (без WireGuard) — отдельный TUN/подсеть/NAT, полностью
	// параллельно WG-пути. Подсеть намеренно не пересекается с wgServerCIDR.
	rawIfaceName  = "wdttraw0"
	rawServerAddr = "10.70.66.1"
	rawServerCIDR = rawServerAddr + "/16"
	// Raw-режим не несёт WG data header (~32 байта) — только RTP-obfs (12 байт
	// заголовок + 16 байт AEAD tag + padding) и TURN framing. MTU 1400: худший
	// случай на проводе — video 1400+89=1489 < 1500 (audio 1400+53=1453),
	// т.е. +7.7% полезной нагрузки на пакет против прежних 1300. Меняется
	// флагом -raw-mtu.
)

// rawMTU — var (не const): меняется флагом -raw-mtu до старта сервера.
var rawMTU = 1350

var dns = "8.8.8.8"
