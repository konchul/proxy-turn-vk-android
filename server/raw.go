package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"
)

// ==================== Raw-IP роутер (без WireGuard) ====================
//
// Полностью параллельный WG-пути транспорт: клиент шлёт/получает сырые IP-
// пакеты прямо через RTP-obfs/TURN (см. -listen-raw), без WireGuard-протокола
// и без loopback-UDP-хопа в userspace WG. Аплинк — просто Write в TUN, ядро
// само маршрутизирует/NAT'ит дальше; даунлинк — один общий ридер TUN,
// раздающий пакеты по dst IP в нужную клиентскую сессию.

// downlinkChunkSizeFor — сколько подряд downlink-пакетов такого размера
// уходят через один и тот же воркер/TURN-relay, прежде чем переключиться на
// следующий. Как и в клиентском dispatcher.go (chunkSizeFor): без этого
// пакеты одного TCP-потока летят вперемешку через relay с разным latency →
// reorder на клиенте → cwnd collapse. Размер зависит от размера пакета по
// той же логике, что и на клиенте — крупные пакеты группируем покрупнее,
// мелкие (вероятно ACK встречного аплоада) переключаем/размазываем быстрее.
// Аплинк размазывается по воркерам естественно (каждый воркер пишет в общий
// TUN сам по себе), а вот даунлинк — один читатель (downlinkLoop), поэтому
// распределение по воркерам нужно делать явно.
const downlinkMaxDwellMS = 15

// downlinkChunkSizeFor зеркалит chunkSizeFor из go_client/dispatcher.go —
// это отдельный Go-модуль (собственный go.mod), общий код не переиспользуется,
// но пороги должны совпадать с клиентскими для симметричного поведения.
func downlinkChunkSizeFor(pktSize int) int {
	switch {
	case pktSize > 1100:
		return 64
	case pktSize >= 701:
		return 24
	case pktSize >= 301:
		return 8
	case pktSize >= 101:
		return 3
	default:
		return 1
	}
}

// downlinkWorkerBuf — глубина канала пакетов, ожидающих отправки через один
// relay-воркер. Тот же порядок величины, что клиентский returnChBuf (BDP при
// ~70-80 Мбит/с и RTT 50-60мс), с запасом на то, что здесь общий downlinkLoop
// раздаёт пакеты быстрее, чем один relay успевает их вытолкнуть в сеть.
const downlinkWorkerBuf = 256

// downlinkWorker — per-conn writer: раньше downlinkLoop писал в conn.Write
// синхронно сам, из единственного потока на весь сервер — если один relay
// подтормаживал, вставал весь downlink всех клиентов сразу. Теперь
// downlinkLoop только раскладывает пакеты по каналам (быстро, неблокирующе),
// а фактическую запись в каждый conn делает своя горутина — так запись в N
// relay идёт параллельно, и медленный конкретный relay не блокирует остальные.
type downlinkWorker struct {
	conn     net.Conn
	deviceID string
	sendCh   chan []byte
	done     chan struct{}
	// Счётчики трафика сессии. Горячий путь инкрементирует атомики (uplink —
	// handleConnRaw, downlink — run ниже), flushRawDeviceTraffic раз в 10с
	// забирает дельты — никакой map+mutex на каждый пакет.
	upBytes   atomic.Int64
	downBytes atomic.Int64
}

func newDownlinkWorker(conn net.Conn, deviceID string) *downlinkWorker {
	w := &downlinkWorker{
		conn:     conn,
		deviceID: deviceID,
		sendCh:   make(chan []byte, downlinkWorkerBuf),
		done:     make(chan struct{}),
	}
	go w.run()
	return w
}

func (w *downlinkWorker) run() {
	defer close(w.done)
	for pkt := range w.sendCh {
		if _, err := w.conn.Write(pkt); err == nil {
			atomic.AddInt64(&totalBytesToClient, int64(len(pkt)))
			w.downBytes.Add(int64(len(pkt)))
		}
		putBuf2048(pkt)
	}
}

// enqueue кладёт пакет в очередь на отправку. Неблокирующе: если воркер
// отстаёт и канал переполнен, пакет дропается — как и раньше при синхронной
// записи, отставший relay не должен задерживать остальных или копить
// неограниченную очередь просроченных данных.
func (w *downlinkWorker) enqueue(pkt []byte) bool {
	select {
	case w.sendCh <- pkt:
		return true
	default:
		putBuf2048(pkt)
		return false
	}
}

func (w *downlinkWorker) stop() {
	close(w.sendCh)
	<-w.done
}

type rawClientSessions struct {
	workers      []*downlinkWorker
	rrIndex      int
	rrCount      int
	chunkStartTs int64 // unix millis начала текущего chunk'а — для downlinkMaxDwellMS
}

type rawRouter struct {
	file            *os.File
	gso             bool // TUN открыт с VNET_HDR: чтения с virtio-заголовком, записи с префиксом
	mu              sync.RWMutex
	sessions        map[uint32]*rawClientSessions // keyed by assigned raw IP клиента (IPv4 как uint32 — ноль аллокаций на lookup)
	uplinkErrLogged uint32                        // чтобы не заспамить лог при устойчивой ошибке записи
	firstUplink     uint32
	firstDownlink   uint32
	noSessionLogged uint32
}

// globalRawRouter — ссылка для flushRawDeviceTraffic (статистика), который
// живёт в statistics.go и не получает роутер через параметры.
var globalRawRouter atomic.Pointer[rawRouter]

// disconnectRawPrefix — маркер явного отключения raw-клиента. Сравнение
// bytes.HasPrefix по байтам пакета, без конвертации пакета в string.
var disconnectRawPrefix = []byte("DISCONNECT_RAW:")

// createRawTUNFile создаёт TUN-интерфейс напрямую через ioctl(TUNSETIFF), в
// обход golang.zx2c4.com/wireguard/tun.CreateTUN. Так надо специально: эта
// библиотека всегда запрашивает IFF_VNET_HDR (для GRO/GSO у настоящего
// WireGuard-устройства), и на современных ядрах он включается — тогда
// Read/Write ожидают virtio_net_hdr перед каждым пакетом. Нам нужны просто
// сырые IP-пакеты без какого-либо заголовка, поэтому TUN создаём сами с
// IFF_TUN|IFF_NO_PI и без IFF_VNET_HDR.
func createBasicTUNFile(name string, nonblock bool) (*os.File, error) {
	nfd, err := unix.Open("/dev/net/tun", unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open /dev/net/tun: %w", err)
	}

	ifr, err := unix.NewIfreq(name)
	if err != nil {
		unix.Close(nfd)
		return nil, fmt.Errorf("NewIfreq: %w", err)
	}
	ifr.SetUint16(unix.IFF_TUN | unix.IFF_NO_PI)
	if err := unix.IoctlIfreq(nfd, unix.TUNSETIFF, ifr); err != nil {
		unix.Close(nfd)
		return nil, fmt.Errorf("TUNSETIFF: %w", err)
	}

	if err := unix.SetNonblock(nfd, nonblock); err != nil {
		unix.Close(nfd)
		return nil, fmt.Errorf("SetNonblock: %w", err)
	}

	return os.NewFile(uintptr(nfd), "/dev/net/tun"), nil
}

// createRawTUNFile — gso=true добавляет IFF_VNET_HDR и включает offloads:
// ядро отдаёт GSO-суперкадры одним Read (даунлинк) и может GRO-склеивать
// TCP аплинка. Каждая запись/чтение несёт 10-байтовый virtio-заголовок.
// Возвращает gsoActive=false, если включить не удалось (фолбэк).
func createRawTUNFile(name string, gso bool) (*os.File, bool, error) {
	if !gso {
		f, err := createBasicTUNFile(name, false)
		return f, false, err
	}
	nfd, err := unix.Open("/dev/net/tun", unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, false, fmt.Errorf("open /dev/net/tun: %w", err)
	}
	ifr, err := unix.NewIfreq(name)
	if err != nil {
		unix.Close(nfd)
		return nil, false, fmt.Errorf("NewIfreq: %w", err)
	}
	ifr.SetUint16(unix.IFF_TUN | unix.IFF_NO_PI | unix.IFF_VNET_HDR)
	if err := unix.IoctlIfreq(nfd, unix.TUNSETIFF, ifr); err != nil {
		unix.Close(nfd)
		return nil, false, fmt.Errorf("TUNSETIFF: %w", err)
	}
	if err := enableTUNOffloads(nfd); err != nil {
		unix.Close(nfd)
		return nil, false, fmt.Errorf("TUNSETOFFLOAD: %w", err)
	}
	return os.NewFile(uintptr(nfd), "/dev/net/tun"), true, nil
}

func newRawRouter(gsoWanted bool) (*rawRouter, error) {
	runCmdSilent("ip", "link", "del", rawIfaceName)
	time.Sleep(100 * time.Millisecond)

	tunFile, gsoActive, err := createRawTUNFile(rawIfaceName, gsoWanted)
	if err != nil {
		return nil, fmt.Errorf("raw TUN: %w", err)
	}
	if gsoWanted && !gsoActive {
		log.Printf("[RAW] GSO недоступен (offload ioctl не прошёл), работаем без VNET_HDR")
	}

	for _, cmd := range [][]string{
		{"ip", "addr", "add", rawServerCIDR, "dev", rawIfaceName},
		{"ip", "link", "set", "mtu", fmt.Sprintf("%d", rawMTU), "dev", rawIfaceName},
		{"ip", "link", "set", rawIfaceName, "up"},
	} {
		out, err := runCmd(cmd[0], cmd[1:]...)
		if err != nil && !strings.Contains(out, "File exists") {
			tunFile.Close()
			return nil, fmt.Errorf("%s: %s", strings.Join(cmd, " "), out)
		}
	}

	if err := setupRawNAT(rawIfaceName); err != nil {
		tunFile.Close()
		return nil, err
	}

	r := &rawRouter{file: tunFile, gso: gsoActive, sessions: make(map[uint32]*rawClientSessions)}
	globalRawRouter.Store(r)
	go r.downlinkLoop()
	log.Printf("[RAW] TUN %s поднят (%s), MTU %d, GSO=%v", rawIfaceName, rawServerCIDR, rawMTU, gsoActive)
	return r, nil
}

func (r *rawRouter) downlinkLoop() {
	buf := make([]byte, 2048)
	if r.gso {
		buf = make([]byte, maxGSOFrame) // GSO-суперкадры до 64КБ за один Read
	}
	var noGsoLogged uint32
	for {
		n, err := r.file.Read(buf)
		if err != nil {
			log.Printf("[RAW] downlink остановлен: %v", err)
			return
		}
		if !r.gso {
			r.dispatchDownlink(buf[:n])
			continue
		}
		// GSO-путь: virtio-заголовок + IP-пакет или суперкадр
		info := parseVnetHdr(buf[:vnetHdrLen])
		frame := buf[vnetHdrLen:n]
		if (info.gsoType &^ gsoTypeECNBit) == gsoTypeNone {
			// одиночный пакет: чексумма может быть частичной — финализируем
			pkt := finalizeIfNeeded(frame, info)
			if pkt == nil {
				continue
			}
			r.dispatchDownlink(pkt)
			continue
		}
		segErr := segmentGSO(frame, info, rawMTU, func(seg []byte) {
			r.dispatchDownlink(seg)
		})
		if segErr != nil {
			if atomic.CompareAndSwapUint32(&noGsoLogged, 0, 1) {
				log.Printf("[RAW] GSO-кадр не сегментирован (%v) — дроп", err)
			}
		}
	}
}

// dispatchDownlink — маршрут одиночного IP-пакета клиентскому воркеру.
// Вызывается и из обычного цикла чтения, и из GSO-сегментатора.
func (r *rawRouter) dispatchDownlink(pkt []byte) {
	if len(pkt) < 20 || pkt[0]>>4 != 4 {
		return // короткий пакет или не IPv4 — raw-режим IPv6 не поддерживает, как и WG-путь
	}
	dst := binary.BigEndian.Uint32(pkt[16:20])
	w := r.pickDownlinkConn(dst, len(pkt))
	if w == nil {
		if atomic.CompareAndSwapUint32(&r.noSessionLogged, 0, 1) {
			log.Printf("[RAW] downlink: нет сессии для %s (пакет от интернета, но клиент не зарегистрирован)", net.IP(pkt[16:20]).String())
		}
		return
	}
	if atomic.CompareAndSwapUint32(&r.firstDownlink, 0, 1) {
		log.Printf("[RAW] Первый downlink-пакет доставлен клиенту %s (%d байт)", net.IP(pkt[16:20]).String(), len(pkt))
	}
	// Копируем в pooled-буфер: pkt живёт в общем buf, который readLoop
	// тут же перезапишет следующим Read — writer-горутина воркера должна
	// получить свою независимую копию, раз запись теперь асинхронная.
	out := getBuf2048()[:len(pkt)]
	copy(out, pkt)
	w.enqueue(out)
}

// pickDownlinkConn выбирает воркера для очередного downlink-пакета клиента
// dst, размазывая нагрузку по всем его зарегистрированным воркерам
// адаптивными чанками (см. downlinkChunkSizeFor) с предохранителем
// downlinkMaxDwellMS на случай, если текущий relay начал тормозить.
//
// dst — IPv4-адрес как uint32 прямо из байтов пакета: hot path без
// net.IP.String()/аллокаций. Карта читается под RLock, а ротация
// rrIndex/rrCount/chunkStartTs крутится только здесь — downlinkLoop
// единственный её писатель (unregister после выноса ресета эти поля не
// трогает), поэтому мутации безопасны без write-lock.
func (r *rawRouter) pickDownlinkConn(dst uint32, pktSize int) *downlinkWorker {
	r.mu.RLock()
	cs := r.sessions[dst]
	var workers []*downlinkWorker
	if cs != nil {
		workers = cs.workers
	}
	r.mu.RUnlock()
	if len(workers) == 0 {
		return nil
	}
	if cs.rrIndex >= len(workers) {
		cs.rrIndex = 0
	}

	now := time.Now().UnixMilli()
	if cs.chunkStartTs == 0 {
		cs.chunkStartTs = now
	} else if now-cs.chunkStartTs >= downlinkMaxDwellMS {
		cs.rrIndex = (cs.rrIndex + 1) % len(workers)
		cs.rrCount = 0
		cs.chunkStartTs = now
	}

	w := workers[cs.rrIndex]
	cs.rrCount++
	if cs.rrCount >= downlinkChunkSizeFor(pktSize) {
		cs.rrIndex = (cs.rrIndex + 1) % len(workers)
		cs.rrCount = 0
		cs.chunkStartTs = now
	}
	return w
}

func (r *rawRouter) register(ip uint32, conn net.Conn, deviceID string) *downlinkWorker {
	w := newDownlinkWorker(conn, deviceID)
	r.mu.Lock()
	cs := r.sessions[ip]
	if cs == nil {
		cs = &rawClientSessions{}
		r.sessions[ip] = cs
	}
	cs.workers = append(cs.workers, w)
	r.mu.Unlock()
	return w
}

func (r *rawRouter) unregister(ip uint32, w *downlinkWorker) {
	r.mu.Lock()
	if cs := r.sessions[ip]; cs != nil {
		for i, existing := range cs.workers {
			if existing == w {
				cs.workers = append(cs.workers[:i], cs.workers[i+1:]...)
				break
			}
		}
		if len(cs.workers) == 0 {
			delete(r.sessions, ip)
		}
	}
	r.mu.Unlock()
	// Остатки счётчиков умирающей сессии — в общую копилку, иначе трафик
	// между последним flush'ем и закрытием потеряется (см. flushRawDeviceTraffic).
	if up := w.upBytes.Swap(0); up > 0 {
		addRawClosedBytes(w.deviceID, up, 0)
	}
	if down := w.downBytes.Swap(0); down > 0 {
		addRawClosedBytes(w.deviceID, 0, down)
	}
	// rrIndex/rrCount здесь не трогаем: их пишет только downlinkLoop, а
	// bounds-check в pickDownlinkConn страхует от выхода за границу.
	// stop() вне r.mu — ждёт завершения writer-горутины (после close(sendCh)
	// она дожигает уже поставленные в очередь пакеты), не держим лок роутера
	// на время этого ожидания.
	w.stop()
}

// writeUplink записывает пакет в TUN. В gso-режиме pkt УЖЕ лежит со
// смещением vnetHdrLen в буфере b (вызывающий), а b[:vnetHdrLen] заполнен
// нулями (gso_type NONE, без csum-offload — семантика идентична режиму
// без VNET_HDR). Зато ядро может GRO-склеивать TCP-сегменты клиента.
func (r *rawRouter) writeUplink(b []byte) error {
	_, err := r.file.Write(b)
	return err
}

// handleConnRaw обслуживает клиента в raw-IP режиме: сначала GETCONF_RAW
// (пароль + deviceID), выдаём IP/DNS/MTU, дальше просто перекачиваем сырые
// IP-пакеты между activeConn и общим TUN-роутером. Никакого WireGuard.
func handleConnRaw(ctx context.Context, clientConn net.Conn, router *rawRouter) {
	atomic.AddInt64(&totalConns, 1)
	atomic.AddInt32(&activeConns, 1)
	defer atomic.AddInt32(&activeConns, -1)

	buf := make([]byte, 1600)
	if err := clientConn.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return
	}
	n, err := clientConn.Read(buf)
	if err != nil {
		return
	}
	clientConn.SetReadDeadline(time.Time{})

	first := string(buf[:n])
	// Как и в классическом handleConn: только ОДИН воркер в группе шлёт
	// GETCONF_RAW (просит IP/DNS/MTU), остальные шлют просто AUTH — их тоже
	// нужно принимать и регистрировать в роутере, иначе из 9 воркеров группы
	// остаётся рабочим максимум один.
	isGetConf := strings.HasPrefix(first, "GETCONF_RAW:")
	isAuth := strings.HasPrefix(first, "AUTH:")
	if !isGetConf && !isAuth {
		return
	}
	var parts []string
	if isGetConf {
		parts = strings.Split(strings.TrimSpace(strings.TrimPrefix(first, "GETCONF_RAW:")), "|")
	} else {
		parts = strings.Split(strings.TrimSpace(strings.TrimPrefix(first, "AUTH:")), "|")
	}
	deviceID := "unknown"
	password := ""
	if len(parts) > 0 {
		deviceID = parts[0]
	}
	if len(parts) > 1 {
		password = parts[1]
	}
	if !connectionCredentialMatches(clientConn, password) {
		clientConn.Write([]byte("DENIED:wrong_password"))
		return
	}

	var assignedIP string

	if isGetConf {
		// Только этот воркер проверяет пароль и (при необходимости) создаёт
		// устройство — как и в классическом GETCONF-пути handleConn.
		dbMutex.Lock()
		isMainPass := password != "" && password == db.MainPassword
		entry, isGenPass := db.Passwords[password]
		valid := isMainPass || (isGenPass && !isPasswordExpired(entry))
		if valid && isGenPass && entry.IsDeactivated {
			dbMutex.Unlock()
			clientConn.Write([]byte("DENIED:deactivated"))
			return
		}
		if valid && isGenPass && !entry.canConnectAndBind(deviceID) {
			dbMutex.Unlock()
			clientConn.Write([]byte("DENIED:device_mismatch"))
			return
		}
		if !valid {
			dbMutex.Unlock()
			if isGenPass && isPasswordExpired(entry) {
				clientConn.Write([]byte("DENIED:expired"))
			} else {
				clientConn.Write([]byte("DENIED:wrong_password"))
			}
			return
		}
		if isGenPass {
			saveDB()
		}

		dev, exists := db.Devices[deviceID]
		if !exists {
			dev = &ClientDevice{DeviceID: deviceID, IP: getNextIP(), RawIP: getNextRawIP()}
			setDeviceOwner(dev, password)
			dev.RawOwnerID = wrapKeyID(password)
			db.Devices[deviceID] = dev
			saveDB()
		} else {
			changed := false
			setDeviceOwner(dev, password)
			dev.RawOwnerID = wrapKeyID(password)
			changed = true
			if dev.RawIP == "" {
				dev.RawIP = getNextRawIP()
				changed = true
			}
			if changed {
				saveDB()
			}
		}
		assignedIP = dev.RawIP
		dbMutex.Unlock()

		if assignedIP == "" {
			clientConn.Write([]byte("NOCONF"))
			return
		}
		if _, err := clientConn.Write([]byte(fmt.Sprintf("RAWCONF:%s|%s|%d", assignedIP, dns, rawMTU))); err != nil {
			return
		}
	} else {
		dbMutex.Lock()
		isMainPass := password != "" && password == db.MainPassword
		entry, isGenPass := db.Passwords[password]
		valid := isMainPass || (isGenPass && !isPasswordExpired(entry) && !entry.IsDeactivated)
		bound := isMainPass
		if valid && isGenPass {
			bound = entry.DeviceID == deviceID
			if !bound {
				for _, id := range entry.DeviceIDs {
					if id == deviceID {
						bound = true
						break
					}
				}
			}
		}
		if !valid || !bound {
			dbMutex.Unlock()
			clientConn.Write([]byte("DENIED:device_mismatch"))
			return
		}
		dev, exists := db.Devices[deviceID]
		if exists {
			setDeviceOwner(dev, password)
			dev.RawOwnerID = wrapKeyID(password)
		}
		if exists && dev.RawIP == "" {
			dev.RawIP = getNextRawIP()
			saveDB()
		}
		if exists {
			assignedIP = dev.RawIP
		}
		dbMutex.Unlock()

		if assignedIP == "" {
			// Устройство ещё не создано GETCONF_RAW-воркером — подождём следующей попытки.
			return
		}
	}
	untrackCredential := trackCredentialConnection(password, deviceID, clientConn)
	defer untrackCredential()

	assignedAddr := net.ParseIP(assignedIP).To4()
	var assignedU32 uint32
	if assignedAddr != nil {
		assignedU32 = binary.BigEndian.Uint32(assignedAddr)
	}
	dlWorker := router.register(assignedU32, clientConn, deviceID)
	defer router.unregister(assignedU32, dlWorker)
	log.Printf("[RAW] Сессия %s зарегистрирована (ip=%s, getConf=%v)", deviceID, assignedIP, isGetConf)
	defer log.Printf("[RAW] Сессия %s (ip=%s) завершена", deviceID, assignedIP)

	activeDevicesMu.Lock()
	activeDevices[deviceID]++
	activeDevicesMu.Unlock()
	defer func() {
		activeDevicesMu.Lock()
		activeDevices[deviceID]--
		if activeDevices[deviceID] <= 0 {
			delete(activeDevices, deviceID)
		}
		activeDevicesMu.Unlock()
	}()

	b := getBuf()
	defer putBuf(b)
	// gso-режим: пакет читается со смещением vnetHdrLen, b[:vnetHdrLen] —
	// нулевой virtio-заголовок для записи в TUN (заполняется один раз ниже)
	uplinkOffset := 0
	if router.gso {
		uplinkOffset = vnetHdrLen
		for i := 0; i < vnetHdrLen; i++ {
			(*b)[i] = 0
		}
	}
	// В отличие от классического WG-пути (30 минут простоя — не страшно, это
	// просто неиспользуемая горутина), мёртвая raw-сессия продолжает висеть в
	// r.sessions[ip].workers и отравляет round-robin в pickDownlinkConn для
	// НОВЫХ переподключений того же устройства. Читаем с коротким таймаутом
	// и реально закрываем сессию, если дольше idleTimeout не было ни данных,
	// ни keepalive (клиент шлёт keepalive каждые 15с — см. keepaliveInterval
	// в session.go), вместо того чтобы просто бесконечно перевзводить дедлайн.
	const idleTimeout = 90 * time.Second
	// SetReadDeadline — setsockopt на каждый вызов, на пакете это лишний
	// сисколл. Перевзводим не чаще раза в 5с: дедлайн всегда 15-20с впереди,
	// поведение idle-детекта не меняется.
	const deadlineArmInterval = 5 * time.Second
	deadline := time.Now().Add(20 * time.Second)
	nextArm := time.Now().Add(deadlineArmInterval)
	clientConn.SetReadDeadline(deadline)
	lastActivity := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if time.Now().After(nextArm) {
			clientConn.SetReadDeadline(time.Now().Add(20 * time.Second))
			nextArm = time.Now().Add(deadlineArmInterval)
		}
		nn, err := clientConn.Read((*b)[uplinkOffset:])
		if err != nil {
			if isNetTimeout(err) {
				if ctx.Err() != nil {
					return
				}
				if time.Since(lastActivity) > idleTimeout {
					return
				}
				continue
			}
			return
		}
		lastActivity = time.Now()
		// Keepalive: первый байт 0xFF, размер переменный (25-44 байта,
		// имитация OPUS-тишины — см. keepaliveMinSize/MaxSize в go_client/
		// session.go). Раньше был жёстко 1 байт; первый байт настоящего
		// IPv4-пакета всегда 0x45 (версия 4, IHL 5) и никогда не 0xFF, так
		// что разбор по первому байту безопасен для любой длины.
		if nn > 0 && (*b)[uplinkOffset] == 0xFF {
			// keepalive: эхо 1 байта — клиент видит живой даунлинк и не
			// убивает здоровую сессию по таймауту чтения; мёртвую (сервер
			// закрыл по idle, сеть сменилась) детектит за один интервал.
			clientConn.Write((*b)[uplinkOffset : uplinkOffset+1])
			continue
		}
		if bytes.HasPrefix((*b)[uplinkOffset:uplinkOffset+nn], disconnectRawPrefix) {
			// Клиент явно сообщил об отключении — сразу освобождаем слот,
			// не дожидаясь idleTimeout (см. комментарий выше и session.go).
			return
		}
		if nn < 20 || (*b)[uplinkOffset]>>4 != 4 || assignedAddr == nil || !bytes.Equal((*b)[uplinkOffset+12:uplinkOffset+16], assignedAddr) {
			continue
		}
		atomic.AddInt64(&totalBytesFromClient, int64(nn))
		dlWorker.upBytes.Add(int64(nn))
		if wErr := router.writeUplink((*b)[:uplinkOffset+nn]); wErr != nil {
			if atomic.CompareAndSwapUint32(&router.uplinkErrLogged, 0, 1) {
				log.Printf("[RAW] Ошибка записи в TUN (ip=%s): %v", assignedIP, wErr)
			}
		} else if atomic.CompareAndSwapUint32(&router.firstUplink, 0, 1) {
			log.Printf("[RAW] Первый uplink-пакет от %s записан в TUN (%d байт)", assignedIP, nn)
		}
	}
}
