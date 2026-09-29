# -*- coding: utf-8 -*-
# Одноразовый скрипт: откатывает правки итерации-1 в server-baseline/raw.go
# и statistics.go, чтобы собрать базовый бинарник для A/B.
import io, os

os.chdir(os.path.dirname(os.path.abspath(__file__)))

p = os.path.join('server-baseline', 'raw.go')
s = io.open(p, encoding='utf-8').read()

def rep(old, new):
    global s
    if old not in s:
        raise SystemExit('NOT FOUND:\n' + old[:120])
    s = s.replace(old, new)

rep('''type downlinkWorker struct {
	conn     net.Conn
	deviceID string
	sendCh   chan []byte
	done     chan struct{}
	// Счётчики трафика сессии. Горячий путь инкрементирует атомики (uplink —
	// handleConnRaw, downlink — run ниже), flushRawDeviceTraffic раз в 10с
	// забирает дельты — никакой map+mutex на каждый пакет.
	upBytes   atomic.Int64
	downBytes atomic.Int64
}''', '''type downlinkWorker struct {
	conn     net.Conn
	deviceID string
	sendCh   chan []byte
	done     chan struct{}
}''')

rep('''			atomic.AddInt64(&totalBytesToClient, int64(len(pkt)))
			w.downBytes.Add(int64(len(pkt)))''', '''			atomic.AddInt64(&totalBytesToClient, int64(len(pkt)))
			addRawDownlinkBytes(w.deviceID, int64(len(pkt)))''')

rep('''type rawRouter struct {
	file            *os.File
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
var disconnectRawPrefix = []byte("DISCONNECT_RAW:")''', '''type rawRouter struct {
	file            *os.File
	mu              sync.RWMutex
	sessions        map[string]*rawClientSessions // keyed by assigned raw IP клиента
	uplinkErrLogged uint32                        // чтобы не заспамить лог при устойчивой ошибке записи
	firstUplink     uint32
	firstDownlink   uint32
	noSessionLogged uint32
}''')

rep('''	r := &rawRouter{file: tunFile, sessions: make(map[uint32]*rawClientSessions)}
	globalRawRouter.Store(r)
	go r.downlinkLoop()''', '''	r := &rawRouter{file: tunFile, sessions: make(map[string]*rawClientSessions)}
	go r.downlinkLoop()''')

rep('''		dst := binary.BigEndian.Uint32(pkt[16:20])
		w := r.pickDownlinkConn(dst, len(pkt))
		if w == nil {
			if atomic.CompareAndSwapUint32(&r.noSessionLogged, 0, 1) {
				log.Printf("[RAW] downlink: нет сессии для %s (пакет от интернета, но клиент не зарегистрирован)", net.IP(pkt[16:20]).String())
			}
			continue
		}
		if atomic.CompareAndSwapUint32(&r.firstDownlink, 0, 1) {
			log.Printf("[RAW] Первый downlink-пакет доставлен клиенту %s (%d байт)", net.IP(pkt[16:20]).String(), len(pkt))
		}''', '''		dst := net.IP(pkt[16:20]).String()
		w := r.pickDownlinkConn(dst, len(pkt))
		if w == nil {
			if atomic.CompareAndSwapUint32(&r.noSessionLogged, 0, 1) {
				log.Printf("[RAW] downlink: нет сессии для %s (пакет от интернета, но клиент не зарегистрирован)", dst)
			}
			continue
		}
		if atomic.CompareAndSwapUint32(&r.firstDownlink, 0, 1) {
			log.Printf("[RAW] Первый downlink-пакет доставлен клиенту %s (%d байт)", dst, len(pkt))
		}''')

rep('''// pickDownlinkConn выбирает воркера для очередного downlink-пакета клиента
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
}''', '''// pickDownlinkConn выбирает воркера для очередного downlink-пакета клиента
// dst, размазывая нагрузку по всем его зарегистрированным воркерам
// адаптивными чанками (см. downlinkChunkSizeFor) с предохранителем
// downlinkMaxDwellMS на случай, если текущий relay начал тормозить.
func (r *rawRouter) pickDownlinkConn(dst string, pktSize int) *downlinkWorker {
	r.mu.Lock()
	defer r.mu.Unlock()
	cs := r.sessions[dst]
	if cs == nil || len(cs.workers) == 0 {
		return nil
	}
	if cs.rrIndex >= len(cs.workers) {
		cs.rrIndex = 0
	}

	now := time.Now().UnixMilli()
	if cs.chunkStartTs == 0 {
		cs.chunkStartTs = now
	} else if now-cs.chunkStartTs >= downlinkMaxDwellMS {
		cs.rrIndex = (cs.rrIndex + 1) % len(cs.workers)
		cs.rrCount = 0
		cs.chunkStartTs = now
	}

	w := cs.workers[cs.rrIndex]
	cs.rrCount++
	if cs.rrCount >= downlinkChunkSizeFor(pktSize) {
		cs.rrIndex = (cs.rrIndex + 1) % len(cs.workers)
		cs.rrCount = 0
		cs.chunkStartTs = now
	}
	return w
}

func (r *rawRouter) register(ip string, conn net.Conn, deviceID string) *downlinkWorker {
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

func (r *rawRouter) unregister(ip string, w *downlinkWorker) {
	r.mu.Lock()
	if cs := r.sessions[ip]; cs != nil {
		for i, existing := range cs.workers {
			if existing == w {
				cs.workers = append(cs.workers[:i], cs.workers[i+1:]...)
				break
			}
		}
		if cs.rrIndex >= len(cs.workers) {
			cs.rrIndex = 0
		}
		cs.rrCount = 0
		if len(cs.workers) == 0 {
			delete(r.sessions, ip)
		}
	}
	r.mu.Unlock()
	// stop() вне r.mu — ждёт завершения writer-горутины (после close(sendCh)
	// она дожигает уже поставленные в очередь пакеты), не держим лок роутера
	// на время этого ожидания.
	w.stop()
}''')

rep('''	untrackCredential := trackCredentialConnection(password, deviceID, clientConn)
	defer untrackCredential()

	assignedAddr := net.ParseIP(assignedIP).To4()
	var assignedU32 uint32
	if assignedAddr != nil {
		assignedU32 = binary.BigEndian.Uint32(assignedAddr)
	}
	dlWorker := router.register(assignedU32, clientConn, deviceID)
	defer router.unregister(assignedU32, dlWorker)''', '''	untrackCredential := trackCredentialConnection(password, deviceID, clientConn)
	defer untrackCredential()

	dlWorker := router.register(assignedIP, clientConn, deviceID)
	defer router.unregister(assignedIP, dlWorker)''')

rep('''	b := getBuf()
	defer putBuf(b)
	// В отличие от классического WG-пути (30 минут простоя — не страшно, это''', '''	b := getBuf()
	defer putBuf(b)
	assignedAddr := net.ParseIP(assignedIP).To4()
	// В отличие от классического WG-пути (30 минут простоя — не страшно, это''')

rep('''	const idleTimeout = 90 * time.Second
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
		nn, err := clientConn.Read(*b)''', '''	const idleTimeout = 90 * time.Second
	lastActivity := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		clientConn.SetReadDeadline(time.Now().Add(20 * time.Second))
		nn, err := clientConn.Read(*b)''')

rep('''		if bytes.HasPrefix((*b)[:nn], disconnectRawPrefix) {''', '''		if strings.HasPrefix(string((*b)[:nn]), "DISCONNECT_RAW:") {''')

rep('''		atomic.AddInt64(&totalBytesFromClient, int64(nn))
		dlWorker.upBytes.Add(int64(nn))''', '''		atomic.AddInt64(&totalBytesFromClient, int64(nn))
		addRawUplinkBytes(deviceID, int64(nn))''')

rep('''import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"''', '''import (
	"bytes"
	"context"
	"fmt"''')

io.open(p, 'w', encoding='utf-8', newline='').write(s)
print('raw.go reverted')

# statistics.go: вернуть map+mutex версию
p2 = os.path.join('server-baseline', 'statistics.go')
s2 = io.open(p2, encoding='utf-8').read()

start = s2.index('// rawTrafficCounter')
end = s2.index('func updateTrafficFromWG()')
s2 = s2[:start] + '''// rawDeviceTraffic — per-device счётчики трафика raw-режима, копятся в
// памяти atomic'ами на горячем пути (каждый uplink/downlink пакет) и
// периодически сбрасываются в db.Devices/db.Passwords в statsLoop под
// dbMutex — так же, как updateTrafficFromWG делает для WireGuard. Прямая
// запись в БД на каждый пакет была бы недопустимо дорогой (db-мьютекс на
// тысячи pps от 380+ клиентов), а глобальные totalBytesFromClient/
// totalBytesToClient раньше не разбивались по устройствам вообще — Raw-
// трафик не попадал ни в бота, ни в /api/profile/status.
type rawTrafficCounter struct {
	up   int64
	down int64
}

var (
	rawDeviceTrafficMu sync.Mutex
	rawDeviceTraffic   = make(map[string]*rawTrafficCounter)
)

func addRawUplinkBytes(deviceID string, n int64) {
	if deviceID == "" || deviceID == "unknown" {
		return
	}
	rawDeviceTrafficMu.Lock()
	c := rawDeviceTraffic[deviceID]
	if c == nil {
		c = &rawTrafficCounter{}
		rawDeviceTraffic[deviceID] = c
	}
	c.up += n
	rawDeviceTrafficMu.Unlock()
}

func addRawDownlinkBytes(deviceID string, n int64) {
	if deviceID == "" || deviceID == "unknown" {
		return
	}
	rawDeviceTrafficMu.Lock()
	c := rawDeviceTraffic[deviceID]
	if c == nil {
		c = &rawTrafficCounter{}
		rawDeviceTraffic[deviceID] = c
	}
	c.down += n
	rawDeviceTrafficMu.Unlock()
}

// flushRawDeviceTraffic переносит накопленные с прошлого вызова байты в
// db.Devices/db.Passwords (вызывающий должен держать dbMutex — см. вызов в
// statsLoop, тот же паттерн, что updateTrafficFromWG под тем же локом).
func flushRawDeviceTrafficLocked() {
	rawDeviceTrafficMu.Lock()
	if len(rawDeviceTraffic) == 0 {
		rawDeviceTrafficMu.Unlock()
		return
	}
	snapshot := rawDeviceTraffic
	rawDeviceTraffic = make(map[string]*rawTrafficCounter)
	rawDeviceTrafficMu.Unlock()

	for deviceID, c := range snapshot {
		if c.up == 0 && c.down == 0 {
			continue
		}
		if dev, ok := db.Devices[deviceID]; ok {
			dev.UpBytes += c.up
			dev.DownBytes += c.down
			if entry := generatedOwnerEntryLocked(dev, deviceID); entry != nil {
				entry.UpBytes += c.up
				entry.DownBytes += c.down
			}
		}
	}
}

''' + s2[end:]

s2 = s2.replace('''			// Пишем server.log и периодически сохраняем БД на диск
			flushRawDeviceTraffic()
			dbMutex.Lock()''', '''			// Пишем server.log и периодически сохраняем БД на диск
			dbMutex.Lock()
			flushRawDeviceTrafficLocked()''')

io.open(p2, 'w', encoding='utf-8', newline='').write(s2)
print('statistics.go reverted')

# main.go: вернуть старый порядок flush
p3 = os.path.join('server-baseline', 'main.go')
s3 = io.open(p3, encoding='utf-8').read()
s3 = s3.replace('''			} else {
				cancel()
				flushRawDeviceTraffic()
				dbMutex.Lock()
				saveDB()
				dbMutex.Unlock()''', '''			} else {
				cancel()
				dbMutex.Lock()
				flushRawDeviceTrafficLocked()
				saveDB()
				dbMutex.Unlock()''')
io.open(p3, 'w', encoding='utf-8', newline='').write(s3)
print('main.go reverted')
print('ALL OK')
