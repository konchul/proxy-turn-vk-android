package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	dtlsnet "github.com/pion/dtls/v3/pkg/net"
	pionudp "github.com/pion/transport/v4/udp"
	"golang.org/x/crypto/chacha20poly1305"
)

// ==================== RTP Обфускация ====================

type ObfsConfig struct {
	SSRC        uint32
	PayloadType uint8
	PaddingMax  int
}

var aeadCache sync.Map // ключ: struct{cipher obfsCipher; key [wrapKeyLen]byte}
var wrapCredentialBindings sync.Map

// obfsCipher — AEAD-алгоритм obfs-слоя. Фрейминг (RTP-заголовок, nonce из
// RTP-полей, padding) одинаков; отличается только шифр. ChaCha20-Poly1305 —
// исходный протокол (совместимость со старыми клиентами), AES-256-GCM —
// новый (AES-NI: в 2-3 раза быстрее на том же ключе и nonce-схеме).
type obfsCipher int

const (
	obfsChaCha20 obfsCipher = iota
	obfsAESGCM
)

const replayWindowSpan = uint64(4096 * 961)
const replaySlots = 8192 // степень двойки; в пакетах вдвое больше span (~4096), чтобы слот не переиспользовался внутри окна

// replayWindow — анти-replay фильтр на кольце фиксированного размера: O(1)
// на пакет, ноль аллокаций и без map-чисток (прежняя map[nonce]ts раз в 8192
// пакета делала полный проход под локом — спайк задержки).
//
// Дубликат ловится точным совпадением extended-ts в слоте idx = extended &
// (replaySlots-1): ts клиента строго монотонен по счётчику пакетов, поэтому
// extended биективен пакету. Слот переиспользуется через 8192 пакета —
// дальше span (4096 пакетов), и такие «старые» повторы отсекаются span-
// проверкой. accept() зовётся только из reader-горутины своей сессии.
type replayWindow struct {
	seen        [replaySlots]uint64 // extended ts последнего пакета в слоте
	occupied    [replaySlots / 64]uint64
	ssrc        uint32
	highestTime uint64
	initialized bool
}

func (w *replayWindow) accept(wire []byte) bool {
	if len(wire) < rtpHeaderLen {
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
	return getAEADFor(key, obfsChaCha20)
}

func getAEADFor(key []byte, c obfsCipher) (cipher.AEAD, error) {
	if len(key) != wrapKeyLen {
		return nil, fmt.Errorf("obfs: key must be %d bytes", wrapKeyLen)
	}
	// Ключ кэша — массив, а не string(key): string конвертация аллоцировала
	// на КАЖДЫЙ вызов, т.е. на каждый пакет горячего пути.
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

func evictAEAD(key []byte) {
	if len(key) == wrapKeyLen {
		var ck = struct {
			c obfsCipher
			k [wrapKeyLen]byte
		}{obfsChaCha20, [wrapKeyLen]byte{}}
		copy(ck.k[:], key)
		aeadCache.Delete(ck)
		ck.c = obfsAESGCM
		aeadCache.Delete(ck)
	}
}

// fastPRNG — xorshift64* для несекретной случайности (длина и байты RTP-
// padding — маскировочный шум, не криптоматериал). crypto/rand.Read здесь
// стоил два getrandom-сисколла на каждый исходящий пакет.
type fastPRNG struct {
	state uint64
}

func newFastPRNG() fastPRNG {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// время как фолбэк — качество зерна для padding некритично
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

type ObfsState struct {
	mu      sync.Mutex
	prng    fastPRNG
	initSeq uint16
	initTs  uint32
	count   uint64
}

func NewObfsConfig(mode string) (*ObfsConfig, error) {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return nil, err
	}
	pt := uint8(111)
	pad := 24
	if strings.EqualFold(strings.TrimSpace(mode), "video") {
		pt = 96
		pad = 60
	}
	return &ObfsConfig{
		SSRC:        binary.BigEndian.Uint32(buf[:]),
		PayloadType: pt,
		PaddingMax:  pad,
	}, nil
}

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

// rtpHeaderLen — bare 12-байтный RTP заголовок, без RFC 8285 extension.
// Должно совпадать байт-в-байт с go_client/obfs.go — отдельный Go-модуль,
// общий код не шарится.
const rtpHeaderLen = 12

// obfsFillNonce заполняет nonce на стеке вызывающего — прежняя obfsBuildNonce
// аллоцировала 12 байт на каждый пакет в обе стороны.
func obfsFillNonce(nonce *[12]byte, ssrc uint32, seq uint16, ts uint32) {
	binary.BigEndian.PutUint32(nonce[0:4], ssrc)
	binary.BigEndian.PutUint16(nonce[4:6], seq)
	binary.BigEndian.PutUint32(nonce[8:12], ts)
}

// obfsWrapPacket упаковывает payload в RTP-obfs кадр. dst переиспользуется
// как выходной буфер, если его вместимости хватает (передайте nil, чтобы
// всегда аллоцировать новый — используется клиентской стороной go_client,
// отдельным модулем, где эта функция не переиспользуется построчно).
func obfsWrapPacket(key, payload []byte, cfg *ObfsConfig, state *ObfsState, dst []byte) ([]byte, error) {
	return obfsWrapCipher(obfsChaCha20, key, payload, cfg, state, dst)
}

func obfsWrapPacketAES(key, payload []byte, cfg *ObfsConfig, state *ObfsState, dst []byte) ([]byte, error) {
	return obfsWrapCipher(obfsAESGCM, key, payload, cfg, state, dst)
}

func obfsWrapCipher(c obfsCipher, key, payload []byte, cfg *ObfsConfig, state *ObfsState, dst []byte) ([]byte, error) {
	if len(key) != wrapKeyLen {
		return nil, fmt.Errorf("obfs: key must be %d bytes (got %d)", wrapKeyLen, len(key))
	}
	if len(payload) == 0 {
		return nil, errors.New("obfs: empty payload")
	}
	state.mu.Lock()
	pc := state.count
	state.count++
	state.mu.Unlock()

	seq := state.initSeq + uint16(pc)
	ts := state.initTs + uint32(pc)*960 + uint32(pc>>16)

	var nonce [12]byte
	obfsFillNonce(&nonce, cfg.SSRC, seq, ts)
	padRand := 0
	if cfg.PaddingMax > 0 {
		padRand = int(state.prng.next()) % cfg.PaddingMax
	}
	padTotal := padRand + 1
	outLen := rtpHeaderLen + len(payload) + chacha20poly1305.Overhead + padTotal
	out := dst
	if cap(out) < outLen {
		out = make([]byte, outLen)
	} else {
		out = out[:outLen]
	}

	out[0] = 0x80 | 0x20 // V=2, X=0 (no extension), P=1 (padding present)
	out[1] = cfg.PayloadType & 0x7F
	binary.BigEndian.PutUint16(out[2:4], seq)
	binary.BigEndian.PutUint32(out[4:8], ts)
	binary.BigEndian.PutUint32(out[8:12], cfg.SSRC)

	aead, err := getAEADFor(key, c)
	if err != nil {
		return nil, fmt.Errorf("obfs: cipher init: %w", err)
	}
	sealed := aead.Seal(out[rtpHeaderLen:rtpHeaderLen], nonce[:], payload, out[:rtpHeaderLen])
	padStart := rtpHeaderLen + len(sealed)
	if padRand > 0 {
		state.prng.fill(out[padStart : padStart+padRand])
	}
	out[outLen-1] = byte(padTotal)
	return out, nil
}

func obfsUnwrapPacket(key, wire, dst []byte) (int, error) {
	return obfsUnwrapCipher(obfsChaCha20, key, wire, dst)
}

func obfsUnwrapPacketAES(key, wire, dst []byte) (int, error) {
	return obfsUnwrapCipher(obfsAESGCM, key, wire, dst)
}

func obfsUnwrapCipher(c obfsCipher, key, wire, dst []byte) (int, error) {
	if len(key) != wrapKeyLen {
		return 0, fmt.Errorf("obfs: key must be %d bytes (got %d)", wrapKeyLen, len(key))
	}
	if len(wire) < rtpHeaderLen+1 {
		return 0, errors.New("obfs: packet too short")
	}
	if (wire[0] >> 6) != 2 {
		return 0, errors.New("obfs: not RTP v2")
	}
	seq := binary.BigEndian.Uint16(wire[2:4])
	ts := binary.BigEndian.Uint32(wire[4:8])
	ssrc := binary.BigEndian.Uint32(wire[8:12])

	payloadEnd := len(wire)
	if wire[0]&0x20 != 0 {
		padLen := int(wire[len(wire)-1])
		if padLen == 0 || padLen > payloadEnd-rtpHeaderLen {
			return 0, fmt.Errorf("obfs: invalid padding length %d", padLen)
		}
		payloadEnd -= padLen
	}
	ciphertextLen := payloadEnd - rtpHeaderLen
	if ciphertextLen <= chacha20poly1305.Overhead {
		return 0, errors.New("obfs: no payload")
	}
	if ciphertextLen-chacha20poly1305.Overhead > len(dst) {
		return 0, errors.New("obfs: dst buffer too small")
	}
	var nonce [12]byte
	obfsFillNonce(&nonce, ssrc, seq, ts)
	aead, err := getAEADFor(key, c)
	if err != nil {
		return 0, fmt.Errorf("obfs: cipher init: %w", err)
	}
	plain, err := aead.Open(dst[:0], nonce[:], wire[rtpHeaderLen:payloadEnd], wire[:rtpHeaderLen])
	if err != nil {
		return 0, fmt.Errorf("obfs: auth: %w", err)
	}
	return len(plain), nil
}
func obfsIsRTPPacket(wire []byte) bool {
	if len(wire) < rtpHeaderLen+1 {
		return false
	}
	if (wire[0] >> 6) != 2 {
		return false
	}
	pt := wire[1] & 0x7F
	return pt == 111 || pt == 96
}

// socketBufSize — размер SO_RCVBUF/SO_SNDBUF на общем UDP-сокете листенера.
// Без явной установки сокет сидит на дефолте ядра (обычно ~212KB), хотя
// net.core.rmem_max/wmem_max уже подняты sysctl'ом (см. enableBBR). При
// сотнях одновременных клиентов на одном сокете дефолтный буфер — источник
// молчаливых ENOBUFS-дропов под всплеском нагрузки.
const socketBufSize = 8 * 1024 * 1024

// listenBatchIO — батчинг системных вызовов на UDP-сокете (pion
// BatchIOConfig → recvmmsg на приёме и sendmmsg на отправке через
// golang.org/x/net/ipv4). WriteBatchInterval = верхняя граница добавочной
// задержки на пакет при низкой нагрузке (флаш раз в interval при неполном
// батче); под нагрузкой батчи заполняются и флашатся раньше.
var listenBatchIO = pionudp.BatchIOConfig{
	Enable:             true,
	ReadBatchSize:      64,
	WriteBatchSize:     64,
	WriteBatchInterval: 200 * time.Microsecond,
}

func listenWrapped(addr *net.UDPAddr, keys *wrapKeyStore, batch bool) (dtlsnet.PacketListener, error) {
	return listenWrappedCipher(addr, keys, batch, obfsChaCha20)
}

// listenWrappedCipher — листенер с выбором AEAD: ChaCha (совместимость) или
// AES-GCM (новый протокол, -listen-raw-aes). Фрейминг идентичен.
func listenWrappedCipher(addr *net.UDPAddr, keys *wrapKeyStore, batch bool, c obfsCipher) (dtlsnet.PacketListener, error) {
	if keys == nil || keys.Count() == 0 {
		return nil, errors.New("wrap: no active keys")
	}
	lc := pionudp.ListenConfig{ReadBufferSize: socketBufSize, WriteBufferSize: socketBufSize}
	if batch {
		lc.Batch = listenBatchIO
	}
	inner, err := lc.Listen("udp", addr)
	if err != nil {
		return nil, fmt.Errorf("wrap: udp listen: %w", err)
	}
	return &wrapPacketListener{
		inner:  dtlsnet.PacketListenerFromListener(inner),
		keys:   keys,
		cipher: c,
	}, nil
}

type wrapPacketListener struct {
	inner  dtlsnet.PacketListener
	keys   *wrapKeyStore
	cipher obfsCipher
}

func (l *wrapPacketListener) Accept() (net.PacketConn, net.Addr, error) {
	pc, addr, err := l.inner.Accept()
	if err != nil {
		return pc, addr, err
	}
	return &wrapPacketConn{inner: pc, keys: l.keys, cipher: l.cipher}, addr, nil
}

func (l *wrapPacketListener) Close() error   { return l.inner.Close() }
func (l *wrapPacketListener) Addr() net.Addr { return l.inner.Addr() }

type wrapPacketConn struct {
	inner     net.PacketConn
	keys      *wrapKeyStore
	cipher    obfsCipher
	key       []byte
	keyID     string
	bindingID string
	selected  int32
	authLog   int32
	obfsCfg   *ObfsConfig
	obfsWrite *ObfsState
	replay    replayWindow
}

func wrapConnectionBindingID(local, remote net.Addr) string {
	if local == nil || remote == nil {
		return ""
	}
	return local.String() + "|" + remote.String()
}

func connectionCredentialMatches(conn net.Conn, password string) bool {
	if conn == nil || password == "" {
		return false
	}
	id := wrapConnectionBindingID(conn.LocalAddr(), conn.RemoteAddr())
	value, ok := wrapCredentialBindings.Load(id)
	return ok && value == "pass:"+wrapKeyID(password)
}

// wrapReadBufPool — промежуточный буфер под RTP-заголовок+AEAD-тег+padding
// поверх p (см. ReadFrom ниже). Раньше аллоцировался заново на КАЖДЫЙ входящий
// пакет — при сотнях клиентов и тысячах pps это заметное GC-давление в
// downlink-пути. p приходит из bufPool (1600 байт, см. handleConn/handleConnRaw),
// поэтому 1700 с запасом хватает под len(p)+80.
var wrapReadBufPool = sync.Pool{
	New: func() interface{} {
		b := make([]byte, 1700)
		return &b
	},
}

func (c *wrapPacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	// Extra space for RTP header (12) + AEAD tag (16) + padding.
	bufPtr := wrapReadBufPool.Get().(*[]byte)
	defer wrapReadBufPool.Put(bufPtr)
	need := len(p) + 80
	if cap(*bufPtr) < need {
		*bufPtr = make([]byte, need)
	}
	buf := (*bufPtr)[:need]
	n, addr, err := c.inner.ReadFrom(buf)
	if err != nil {
		return 0, addr, err
	}
	raw := buf[:n]

	if atomic.LoadInt32(&c.selected) == 0 {
		key, keyID, m, uErr := c.keys.Unwrap(raw, p, c.cipher)
		if uErr != nil {
			if atomic.CompareAndSwapInt32(&c.authLog, 0, 1) {
				log.Printf("[WRAP] Отказ: RTP AEAD auth failed from %s (keys=%d)", addr.String(), c.keys.Count())
			}
			return 0, addr, uErr
		}
		cfg, cfgErr := NewObfsConfig("audio")
		if cfgErr != nil {
			return 0, addr, fmt.Errorf("wrap: random config: %w", cfgErr)
		}
		writeState, stateErr := NewObfsState()
		if stateErr != nil {
			return 0, addr, fmt.Errorf("wrap: random state: %w", stateErr)
		}
		c.key = append([]byte(nil), key...) // Клонируем ключ в независимую память!
		c.keyID = keyID
		c.bindingID = wrapConnectionBindingID(c.LocalAddr(), addr)
		wrapCredentialBindings.Store(c.bindingID, keyID)
		c.obfsCfg = cfg
		if len(raw) > 1 {
			c.obfsCfg.PayloadType = raw[1] & 0x7F
			if c.obfsCfg.PayloadType == 96 {
				c.obfsCfg.PaddingMax = 60
			}
		}
		c.obfsWrite = writeState
		atomic.StoreInt32(&c.selected, 1)
		if !c.replay.accept(raw) {
			return 0, addr, errors.New("wrap: replay")
		}
		if atomic.CompareAndSwapInt32(&c.authLog, 0, 1) {
			log.Printf("[WRAP] OK: ключ выбран для %s (keys=%d)", addr.String(), c.keys.Count())
		}
		return m, addr, nil
	}

	m, uErr := c.unwrap(raw, p)
	if uErr != nil {
		// Если расшифровка старым ключом провалилась — возможно, пароль обновился!
		// Пробуем пере-верифицировать пакет по всем активным ключам
		key, keyID, m2, uErr2 := c.keys.Unwrap(raw, p, c.cipher)
		if uErr2 == nil {
			if !bytes.Equal(key, c.key) {
				c.replay = replayWindow{}
			}
			if !c.replay.accept(raw) {
				return 0, addr, errors.New("wrap: replay")
			}
			cfg, cfgErr := NewObfsConfig("audio")
			if cfgErr != nil {
				return 0, addr, fmt.Errorf("wrap: random config: %w", cfgErr)
			}
			writeState, stateErr := NewObfsState()
			if stateErr != nil {
				return 0, addr, fmt.Errorf("wrap: random state: %w", stateErr)
			}
			c.key = append([]byte(nil), key...) // На лету обновляем ключ сессии!
			c.keyID = keyID
			c.bindingID = wrapConnectionBindingID(c.LocalAddr(), addr)
			wrapCredentialBindings.Store(c.bindingID, keyID)
			c.obfsCfg = cfg
			if len(raw) > 1 {
				c.obfsCfg.PayloadType = raw[1] & 0x7F
				if c.obfsCfg.PayloadType == 96 {
					c.obfsCfg.PaddingMax = 60
				}
			}
			c.obfsWrite = writeState
			log.Printf("[WRAP] Обновлен ключ на лету для %s (пароль изменился/обновился)", addr.String())
			return m2, addr, nil
		}
		return 0, addr, fmt.Errorf("obfs unwrap: %w", uErr)
	}
	if !c.replay.accept(raw) {
		return 0, addr, errors.New("wrap: replay")
	}
	return m, addr, nil
}

func (c *wrapPacketConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	if atomic.LoadInt32(&c.selected) == 0 || len(c.key) != wrapKeyLen {
		return 0, errors.New("wrap: key not selected")
	}
	if c.obfsCfg == nil || c.obfsWrite == nil {
		cfg, cfgErr := NewObfsConfig("audio")
		if cfgErr != nil {
			return 0, fmt.Errorf("wrap: random config: %w", cfgErr)
		}
		writeState, stateErr := NewObfsState()
		if stateErr != nil {
			return 0, fmt.Errorf("wrap: random state: %w", stateErr)
		}
		c.obfsCfg = cfg
		c.obfsWrite = writeState
	}
	bufPtr := wrapReadBufPool.Get().(*[]byte)
	defer wrapReadBufPool.Put(bufPtr)
	wrapped, wErr := c.wrap(p, c.obfsCfg, c.obfsWrite, *bufPtr)
	if wErr != nil {
		return 0, fmt.Errorf("obfs wrap: %w", wErr)
	}
	if _, err := c.inner.WriteTo(wrapped, addr); err != nil {
		return 0, err
	}
	return len(p), nil
}

// unwrap/wrap — вызов obfs-примитива по шифру листенера, на котором живёт
// соединение.
func (c *wrapPacketConn) unwrap(wire, dst []byte) (int, error) {
	if c.cipher == obfsAESGCM {
		return obfsUnwrapPacketAES(c.key, wire, dst)
	}
	return obfsUnwrapPacket(c.key, wire, dst)
}

func (c *wrapPacketConn) wrap(payload []byte, cfg *ObfsConfig, state *ObfsState, dst []byte) ([]byte, error) {
	if c.cipher == obfsAESGCM {
		return obfsWrapPacketAES(c.key, payload, cfg, state, dst)
	}
	return obfsWrapPacket(c.key, payload, cfg, state, dst)
}

func (c *wrapPacketConn) Close() error {
	if c.bindingID != "" {
		wrapCredentialBindings.Delete(c.bindingID)
	}
	evikey := c.key
	c.key = nil
	zeroBytes(evikey)
	return c.inner.Close()
}
func (c *wrapPacketConn) LocalAddr() net.Addr                { return c.inner.LocalAddr() }
func (c *wrapPacketConn) SetDeadline(t time.Time) error      { return c.inner.SetDeadline(t) }
func (c *wrapPacketConn) SetReadDeadline(t time.Time) error  { return c.inner.SetReadDeadline(t) }
func (c *wrapPacketConn) SetWriteDeadline(t time.Time) error { return c.inner.SetWriteDeadline(t) }
