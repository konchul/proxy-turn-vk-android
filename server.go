package main

import (
	"bytes"
	"crypto/cipher"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	neturl "net/url"
	"mime/multipart"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/pion/dtls/v3"
	"github.com/pion/dtls/v3/pkg/crypto/selfsign"
	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/hkdf"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/ipc"
	"golang.zx2c4.com/wireguard/tun"

	dtlsnet "github.com/pion/dtls/v3/pkg/net"
	pionudp "github.com/pion/transport/v4/udp"
)

const (
	wgIfaceName           = "wdtt0"
	wgServerAddr          = "10.66.66.1"
	wgClientAddr          = "10.66.66.2"
	wgClientCIDR          = wgClientAddr + "/32"
	wgServerCIDR          = wgServerAddr + "/16"
	defaultInternalWGPort = 56001
	wgMTU                 = 1280
	keepalive             = 25
)

var dns = "8.8.8.8"

// ==================== База данных и Бот ====================

type ClientDevice struct {
	DeviceID  string `json:"device_id"`
	IP        string `json:"ip"`
	PrivKey   string `json:"priv_key"`
	PubKey    string `json:"pub_key"`
	DownBytes int64  `json:"down_bytes"` // скачано устройством
	UpBytes   int64  `json:"up_bytes"`   // отдано устройством
}

type PasswordEntry struct {
	Label         string   `json:"label,omitempty"` // понятное имя в боте
	DeviceID      string   `json:"device_id"`       // Для обратной совместимости, если нужно
	DeviceIDs     []string `json:"device_ids"`  // Список привязанных deviceID
	MaxDevices    int      `json:"max_devices"` // Максимальное кол-во устройств (0 или 1 = 1 устройство)
	ExpiresAt     int64    `json:"expires_at"`  // unix timestamp
	DownBytes     int64    `json:"down_bytes"`  // скачано клиентом
	UpBytes       int64    `json:"up_bytes"`    // отдано клиентом
	VkHash        string   `json:"vk_hash,omitempty"`
	Ports         string   `json:"ports,omitempty"` // "dtls,wg,tun"
	IsDeactivated bool     `json:"is_deactivated,omitempty"`
}

func (entry *PasswordEntry) canConnectAndBind(deviceID string) bool {
	limit := entry.MaxDevices
	if limit <= 0 {
		limit = 1
	}

	// Сначала проверяем, привязано ли уже это устройство
	for _, id := range entry.DeviceIDs {
		if id == deviceID {
			return true
		}
	}

	// Для обратной совместимости
	if len(entry.DeviceIDs) == 0 && entry.DeviceID != "" {
		if entry.DeviceID == deviceID {
			entry.DeviceIDs = []string{deviceID}
			return true
		}
		if limit == 1 {
			return false
		}
		entry.DeviceIDs = append(entry.DeviceIDs, entry.DeviceID)
	}

	// Если есть свободное место под новое устройство
	if len(entry.DeviceIDs) < limit {
		entry.DeviceIDs = append(entry.DeviceIDs, deviceID)
		if len(entry.DeviceIDs) == 1 {
			entry.DeviceID = deviceID
		} else {
			entry.DeviceID = "multi"
		}
		return true
	}

	return false
}

// Трафик главного пароля (владельца)
var (
	mainPassDown int64
	mainPassUp   int64
)

// Онлайн-статус устройств
var (
	activeDevices   = make(map[string]int32) // deviceID -> кол-во активных коннектов
	activeDevicesMu sync.Mutex
)

type Database struct {
	MainPassword string                    `json:"main_password"`
	AdminID      string                    `json:"admin_id"`
	BotToken     string                    `json:"bot_token"`
	Passwords    map[string]*PasswordEntry `json:"passwords"`
	Devices      map[string]*ClientDevice  `json:"devices"`
}

var (
	db           *Database
	dbMutex      sync.Mutex
	dbFile       string
	globalWgDev  *device.Device
)

var serverWrapKeys = newWrapKeyStore()

const (
	passChars             = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghjkmnpqrstuvwxyz23456789"
	generatedPasswordLen  = 16
	maxGeneratedPasswords = 10
)

func generatePassword() string {
	b := make([]byte, generatedPasswordLen)
	randomBytes := make([]byte, len(b))
	if _, err := rand.Read(randomBytes); err != nil {
		now := time.Now().UnixNano()
		for i := range b {
			b[i] = passChars[int(now+int64(i))%len(passChars)]
		}
		return string(b)
	}
	for i, raw := range randomBytes {
		b[i] = passChars[int(raw)%len(passChars)]
	}
	return string(b)
}

func passwordEntryLabel(entry *PasswordEntry, pass string, index int) string {
	if entry != nil {
		if label := strings.TrimSpace(entry.Label); label != "" {
			return label
		}
	}
	if len(pass) >= 4 {
		return fmt.Sprintf("Доступ …%s", pass[len(pass)-4:])
	}
	return fmt.Sprintf("Доступ #%d", index)
}

func nextPasswordLabel() string {
	return fmt.Sprintf("Доступ %d", len(db.Passwords)+1)
}

var publicIP string = ""

func getPublicIP() string {
	if publicIP != "" {
		return publicIP
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("https://api.ipify.org")
	if err != nil {
		return "YOUR_SERVER_IP"
	}
	defer resp.Body.Close()
	ipBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "YOUR_SERVER_IP"
	}
	publicIP = string(bytes.TrimSpace(ipBytes))
	return publicIP
}

type wrapKeyEntry struct {
	id  string
	key []byte
}

type wrapKeyStore struct {
	mu      sync.RWMutex
	entries []wrapKeyEntry
}

func newWrapKeyStore() *wrapKeyStore {
	return &wrapKeyStore{}
}

func deriveWrapKey(password string) ([]byte, error) {
	if password == "" {
		return nil, errors.New("empty password")
	}
	key := make([]byte, wrapKeyLen)
	reader := hkdf.New(
		sha256.New,
		[]byte(password),
		[]byte("WDTT-WRAP-v1"),
		[]byte("rtp-obfs/chacha20poly1305"),
	)
	if _, err := io.ReadFull(reader, key); err != nil {
		return nil, fmt.Errorf("derive wrap key: %w", err)
	}
	return key, nil
}

func wrapKeyID(password string) string {
	sum := sha256.Sum256([]byte("WDTT-WRAP-ID-v1\x00" + password))
	return hex.EncodeToString(sum[:8])
}

func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

func (s *wrapKeyStore) SetPasswords(mainPassword string, generated []string) error {
	next := make([]wrapKeyEntry, 0, len(generated)+1)
	seen := make(map[string]struct{}, len(generated)+1)

	if mainPassword != "" {
		key, err := deriveWrapKey(mainPassword)
		if err != nil {
			return err
		}
		next = append(next, wrapKeyEntry{id: "main", key: key})
		seen["main"] = struct{}{}
	}

	for _, password := range generated {
		if password == "" {
			continue
		}
		id := "pass:" + wrapKeyID(password)
		if _, exists := seen[id]; exists {
			continue
		}
		key, err := deriveWrapKey(password)
		if err != nil {
			for _, entry := range next {
				zeroBytes(entry.key)
			}
			return err
		}
		next = append(next, wrapKeyEntry{id: id, key: key})
		seen[id] = struct{}{}
	}

	s.mu.Lock()
	old := s.entries
	s.entries = next
	s.mu.Unlock()
	for _, entry := range old {
		zeroBytes(entry.key)
	}
	return nil
}

func (s *wrapKeyStore) AddPassword(password string) error {
	key, err := deriveWrapKey(password)
	if err != nil {
		return err
	}
	id := "pass:" + wrapKeyID(password)

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, entry := range s.entries {
		if entry.id == id {
			zeroBytes(key)
			return nil
		}
	}
	s.entries = append(s.entries, wrapKeyEntry{id: id, key: key})
	return nil
}

func (s *wrapKeyStore) RemovePassword(password string) {
	id := "pass:" + wrapKeyID(password)

	s.mu.Lock()
	defer s.mu.Unlock()
	for i, entry := range s.entries {
		if entry.id != id {
			continue
		}
		zeroBytes(entry.key)
		copy(s.entries[i:], s.entries[i+1:])
		s.entries[len(s.entries)-1] = wrapKeyEntry{}
		s.entries = s.entries[:len(s.entries)-1]
		return
	}
}

func (s *wrapKeyStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.entries)
}

func (s *wrapKeyStore) Unwrap(raw, dst []byte) ([]byte, int, error) {
	if !obfsIsRTPPacket(raw) {
		return nil, 0, errors.New("wrap: non-obfs packet")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.entries) == 0 {
		return nil, 0, errors.New("wrap: no active keys")
	}
	for _, entry := range s.entries {
		m, err := obfsUnwrapPacket(entry.key, raw, dst)
		if err == nil {
			return append([]byte(nil), entry.key...), m, nil
		}
	}
	return nil, 0, errors.New("wrap: auth failed")
}

func refreshWrapKeysFromDBLocked() error {
	passwords := make([]string, 0, len(db.Passwords))
	for password, entry := range db.Passwords {
		if !isPasswordExpired(entry) {
			passwords = append(passwords, password)
		}
	}
	return serverWrapKeys.SetPasswords(db.MainPassword, passwords)
}

func reloadDB(wgDev *device.Device) error {
	dbMutex.Lock()
	defer dbMutex.Unlock()

	data, err := os.ReadFile(dbFile)
	if err != nil {
		return fmt.Errorf("read db file: %w", err)
	}

	oldDB := db

	newDB := &Database{
		Passwords: make(map[string]*PasswordEntry),
		Devices:   make(map[string]*ClientDevice),
	}
	if err := json.Unmarshal(data, newDB); err != nil {
		return fmt.Errorf("parse db json: %w", err)
	}

	newDB.MainPassword = oldDB.MainPassword
	newDB.AdminID = oldDB.AdminID
	newDB.BotToken = oldDB.BotToken

	db = newDB

	// Очищаем истёкшие
	cleanupExpiredPasswordsLocked(wgDev)

	// Находим удаленные устройства и удаляем их из WireGuard
	for devID, oldDev := range oldDB.Devices {
		if _, exists := db.Devices[devID]; !exists {
			removePeerFromWG(wgDev, oldDev)
		}
	}

	// Синхронизируем новые/оставшиеся устройства с WireGuard
	for _, dev := range db.Devices {
		upsertPeerInWG(wgDev, dev)
	}

	// Обновляем криптографические WRAP-ключи в памяти
	if err := refreshWrapKeysFromDBLocked(); err != nil {
		return fmt.Errorf("refresh wrap keys: %w", err)
	}

	return nil
}

func initDB(dir, mainPass, adminID, botToken string) {
	dbFile = filepath.Join(dir, "passwords.json")
	db = &Database{
		Passwords: make(map[string]*PasswordEntry),
		Devices:   make(map[string]*ClientDevice),
	}
	data, err := os.ReadFile(dbFile)
	if err == nil {
		json.Unmarshal(data, db)
	}
	if db.Passwords == nil {
		db.Passwords = make(map[string]*PasswordEntry)
	}
	if db.Devices == nil {
		db.Devices = make(map[string]*ClientDevice)
	}
	db.MainPassword = mainPass
	db.AdminID = adminID
	db.BotToken = botToken
	saveDB()
	if err := refreshWrapKeysFromDBLocked(); err != nil {
		log.Fatalf("[WRAP] init keys: %v", err)
	}
}

func saveDB() {
	data, _ := json.MarshalIndent(db, "", "  ")
	os.WriteFile(dbFile, data, 0600)
}

func isPasswordExpired(entry *PasswordEntry) bool {
	if entry == nil {
		return true
	}
	if entry.ExpiresAt == 0 {
		return false // бессрочный
	}
	return time.Now().Unix() > entry.ExpiresAt
}

func getNextIP() string {
	used := make(map[string]bool)
	for _, dev := range db.Devices {
		used[dev.IP] = true
	}
	for b3 := 0; b3 <= 255; b3++ {
		for b4 := 1; b4 <= 254; b4++ {
			ip := fmt.Sprintf("10.66.%d.%d", b3, b4)
			if ip == "10.66.66.1" {
				continue
			}
			if !used[ip] {
				return ip
			}
		}
	}
	return ""
}

func botLoop(token string, adminIDstr string, wgDev *device.Device) {
	if token == "" || adminIDstr == "" {
		return
	}
	adminID, _ := strconv.ParseInt(adminIDstr, 10, 64)
	if adminID == 0 {
		return
	}

	// Устанавливаем команды для синей кнопки Menu
	go func() {
		cmds := `{"commands":[{"command":"new","description":"Создать временный пароль"},{"command":"list","description":"Управление доступами"}]}`
		resp, err := http.Post(fmt.Sprintf("https://api.telegram.org/bot%s/setMyCommands", token), "application/json", strings.NewReader(cmds))
		if err == nil {
			resp.Body.Close()
		}
	}()

	offset := 0
	client := &http.Client{Timeout: 65 * time.Second}

	// Состояние ожидания ввода
	var waitingForDays bool
	var waitingForPorts bool
	var waitingForHash bool
	var targetPassword string

	var tempDays int
	var tempMaxDevs int
	var tempPorts string // "dtls,wg,tun"

	for {
		url := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?timeout=60&offset=%d", token, offset)
		resp, err := client.Get(url)
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}

		var res struct {
			Ok     bool `json:"ok"`
			Result []struct {
				UpdateID int `json:"update_id"`
				Message  *struct {
					Chat struct {
						ID int64 `json:"id"`
					} `json:"chat"`
					Text string `json:"text"`
				} `json:"message"`
				CallbackQuery *struct {
					ID      string `json:"id"`
					Data    string `json:"data"`
					Message struct {
						MessageID int `json:"message_id"`
						Chat      struct {
							ID int64 `json:"id"`
						} `json:"chat"`
					} `json:"message"`
				} `json:"callback_query"`
			} `json:"result"`
		}

		err = json.NewDecoder(resp.Body).Decode(&res)
		resp.Body.Close()
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}

		for _, u := range res.Result {
			offset = u.UpdateID + 1

			// ═══ Callback кнопки ═══
			if u.CallbackQuery != nil && u.CallbackQuery.Message.Chat.ID == adminID {
				data := u.CallbackQuery.Data
				answerCallback(token, u.CallbackQuery.ID)

				if strings.HasPrefix(data, "viewpass_") {
					// Просмотр деталей пароля
					pass := strings.TrimPrefix(data, "viewpass_")
					dbMutex.Lock()
					entry, exists := db.Passwords[pass]
					if !exists || entry == nil {
						dbMutex.Unlock()
						sendTelegram(token, adminID, "❌ Пароль не найден", nil)
						continue
					}
					txt := fmt.Sprintf("👤 *Имя:* %s\n🔑 *Пароль:* `%s`\n", passwordEntryLabel(entry, pass, 0), pass)
					if entry.VkHash != "" {
						ports := entry.Ports
						if ports == "" {
							ports = "56000,56001,9000"
						}
						pts := strings.Split(ports, ",")
						srvIP := getPublicIP()
						link := fmt.Sprintf("wdtt://%s:%s:%s:%s:%s:%s", srvIP, pts[0], pts[1], pts[2], pass, entry.VkHash)
						txt += fmt.Sprintf("🔗 *Быстрая ссылка:* `%s`\n", link)
					}
					if entry.IsDeactivated {
						txt += "🔴 Статус: *ДЕАКТИВИРОВАН*\n"
					} else {
						txt += "🟢 Статус: *АКТИВЕН*\n"
					}

					if entry.ExpiresAt > 0 {
						expireTime := time.Unix(entry.ExpiresAt, 0)
						remaining := time.Until(expireTime)
						if remaining > 0 {
							txt += fmt.Sprintf("⏰ Истекает: %s (через %dd)\n", expireTime.Format("02.01.2006"), int(remaining.Hours()/24))
						} else {
							txt += "⏰ *ИСТЁК* ❌\n"
						}
					} else {
						txt += "⏰ Бессрочный ♾\n"
					}

					txt += fmt.Sprintf("\n📊 *Трафик:*\n• Скачано: %.2f MB\n• Отдано: %.2f MB\n", float64(entry.DownBytes)/(1024*1024), float64(entry.UpBytes)/(1024*1024))

					limit := entry.MaxDevices
					if limit <= 0 {
						limit = 1
					}
					boundCount := len(entry.DeviceIDs)
					if boundCount == 0 && entry.DeviceID != "" {
						boundCount = 1
					}

					txt += fmt.Sprintf("\n📱 *Привязанные устройства* (%d/%d):\n", boundCount, limit)
					hasDevices := false

					// Legacy
					if entry.DeviceID != "" && len(entry.DeviceIDs) == 0 {
						hasDevices = true
						dev, devExists := db.Devices[entry.DeviceID]
						if devExists {
							txt += fmt.Sprintf("• ID: `%s`\n  IP: `%s`\n  📊 ↑%.1f MB / ↓%.1f MB\n", entry.DeviceID, dev.IP, float64(dev.UpBytes)/(1024*1024), float64(dev.DownBytes)/(1024*1024))
						} else {
							txt += fmt.Sprintf("• ID: `%s` (удалено)\n", entry.DeviceID)
						}
					}

					// Array
					for i, id := range entry.DeviceIDs {
						hasDevices = true
						dev, devExists := db.Devices[id]
						if devExists {
							txt += fmt.Sprintf("• [%d] ID: `%s`\n  IP: `%s`\n  📊 ↑%.1f MB / ↓%.1f MB\n", i+1, id, dev.IP, float64(dev.UpBytes)/(1024*1024), float64(dev.DownBytes)/(1024*1024))
						} else {
							txt += fmt.Sprintf("• [%d] ID: `%s` (удалено)\n", i+1, id)
						}
					}

					var kb []map[string]interface{}
					kb = append(kb, map[string]interface{}{
						"text":          "📂 Получить .conf файл",
						"callback_data": "getfile_" + pass,
					})
					if !hasDevices {
						txt += "_Ожидает первого подключения..._\n"
					} else {
						kb = append(kb, map[string]interface{}{
							"text":          "🗑 Отвязать ВСЕ устройства",
							"callback_data": "unbind_" + pass,
						})
					}

					dbMutex.Unlock()
					if entry.IsDeactivated {
						kb = append(kb, map[string]interface{}{
							"text":          "✅ Активировать",
							"callback_data": "react_" + pass,
						})
					} else {
						kb = append(kb, map[string]interface{}{
							"text":          "⏸ Деактивировать",
							"callback_data": "deact_" + pass,
						})
					}
					kb = append(kb, map[string]interface{}{
						"text":          "❌ Удалить пароль",
						"callback_data": "delpass_" + pass,
					})
					kb = append(kb, map[string]interface{}{
						"text":          "◀️ Назад к списку",
						"callback_data": "backlist",
					})
					var keyboard [][]map[string]interface{}
					for _, btn := range kb {
						keyboard = append(keyboard, []map[string]interface{}{btn})
					}
					sendTelegram(token, adminID, txt, map[string]interface{}{"inline_keyboard": keyboard})

				} else if strings.HasPrefix(data, "deact_") {
					pass := strings.TrimPrefix(data, "deact_")
					dbMutex.Lock()
					entry, exists := db.Passwords[pass]
					if exists && entry != nil {
						entry.IsDeactivated = true
						// Отключаем активные устройства от WG
						if entry.DeviceID != "" {
							dev, devExists := db.Devices[entry.DeviceID]
							if devExists {
								pubHex, _ := b64ToHex(dev.PubKey)
								wgDev.IpcSet(fmt.Sprintf("public_key=%s\nremove=true\n", pubHex))
							}
						}
						for _, id := range entry.DeviceIDs {
							dev, devExists := db.Devices[id]
							if devExists {
								pubHex, _ := b64ToHex(dev.PubKey)
								wgDev.IpcSet(fmt.Sprintf("public_key=%s\nremove=true\n", pubHex))
							}
						}
						saveDB()
					}
					dbMutex.Unlock()
					sendTelegram(token, adminID, fmt.Sprintf("⏸ Пароль `%s` деактивирован", pass), nil)

				} else if strings.HasPrefix(data, "react_") {
					pass := strings.TrimPrefix(data, "react_")
					dbMutex.Lock()
					entry, exists := db.Passwords[pass]
					if exists && entry != nil {
						entry.IsDeactivated = false
						saveDB()
					}
					dbMutex.Unlock()
					sendTelegram(token, adminID, fmt.Sprintf("✅ Пароль `%s` активирован", pass), nil)

				} else if data == "mainlink" {
					targetPassword = "main"
					var keyboard [][]map[string]interface{}
					keyboard = append(keyboard, []map[string]interface{}{
						{"text": "Да", "callback_data": "ports_def"},
						{"text": "Нет", "callback_data": "ports_custom"},
					})
					sendTelegram(token, adminID, "⚙️ Использовать стандартные порты для главного пароля (56000, 56001, 9000)?", map[string]interface{}{"inline_keyboard": keyboard})

				} else if data == "ports_def" {
					tempPorts = "56000,56001,9000"
					waitingForHash = true
					sendTelegram(token, adminID, "🔑 Укажите VK хеш (или несколько через запятую):", nil)

				} else if data == "ports_custom" {
					waitingForPorts = true
					sendTelegram(token, adminID, "⚙️ Укажите через запятую 3 порта (DTLS,WG,TUN):\nНапример: 56000,56001,9000", nil)

				} else if strings.HasPrefix(data, "getfile_") {
					pass := strings.TrimPrefix(data, "getfile_")
					dbMutex.Lock()
					entry, exists := db.Passwords[pass]
					if exists && entry != nil {
						srvIP := getPublicIP()
						configJSON := fmt.Sprintf(`{
  "name": "qWDTT - %s",
  "peer": "%s",
  "vkHashes": "%s",
  "workersPerHash": 16,
  "listenPort": 9000,
  "password": "%s"
}`, srvIP, srvIP, entry.VkHash, pass)
						dbMutex.Unlock()

						fileName := fmt.Sprintf("qwdtt_%s.conf", pass)
						sendTelegramFile(token, adminID, fileName, []byte(configJSON))
					} else {
						dbMutex.Unlock()
						sendTelegram(token, adminID, "❌ Пароль не найден", nil)
					}

				} else if strings.HasPrefix(data, "unbind_") {
					pass := strings.TrimPrefix(data, "unbind_")
					dbMutex.Lock()
					entry, exists := db.Passwords[pass]
					if exists && entry != nil {
						// Удаляем все привязанные устройства из WG и из хранилища
						if entry.DeviceID != "" {
							dev, devExists := db.Devices[entry.DeviceID]
							if devExists {
								pubHex, _ := b64ToHex(dev.PubKey)
								wgDev.IpcSet(fmt.Sprintf("public_key=%s\nremove=true\n", pubHex))
								delete(db.Devices, entry.DeviceID)
							}
							entry.DeviceID = ""
						}
						for _, id := range entry.DeviceIDs {
							dev, devExists := db.Devices[id]
							if devExists {
								pubHex, _ := b64ToHex(dev.PubKey)
								wgDev.IpcSet(fmt.Sprintf("public_key=%s\nremove=true\n", pubHex))
								delete(db.Devices, id)
							}
						}
						entry.DeviceIDs = nil
						saveDB()
					}
					dbMutex.Unlock()
					sendTelegram(token, adminID, fmt.Sprintf("✅ Все устройства отвязаны от пароля `%s`", pass), nil)

				} else if strings.HasPrefix(data, "delpass_") {
					pass := strings.TrimPrefix(data, "delpass_")
					dbMutex.Lock()
					entry, exists := db.Passwords[pass]
					if exists && entry != nil {
						if entry.DeviceID != "" {
							dev, devExists := db.Devices[entry.DeviceID]
							if devExists {
								pubHex, _ := b64ToHex(dev.PubKey)
								wgDev.IpcSet(fmt.Sprintf("public_key=%s\nremove=true\n", pubHex))
								delete(db.Devices, entry.DeviceID)
							}
						}
						for _, id := range entry.DeviceIDs {
							dev, devExists := db.Devices[id]
							if devExists {
								pubHex, _ := b64ToHex(dev.PubKey)
								wgDev.IpcSet(fmt.Sprintf("public_key=%s\nremove=true\n", pubHex))
								delete(db.Devices, id)
							}
						}
					}
					delete(db.Passwords, pass)
					serverWrapKeys.RemovePassword(pass)
					saveDB()
					dbMutex.Unlock()
					sendTelegram(token, adminID, fmt.Sprintf("✅ Пароль `%s` и его устройства удалены", pass), nil)

				} else if strings.HasPrefix(data, "deldev_") {
					devID := strings.TrimPrefix(data, "deldev_")
					dbMutex.Lock()
					dev, exists := db.Devices[devID]
					if exists {
						delete(db.Devices, devID)
						pubHex, _ := b64ToHex(dev.PubKey)
						wgDev.IpcSet(fmt.Sprintf("public_key=%s\nremove=true\n", pubHex))
						// Очищаем привязку из пароля
						for _, entry := range db.Passwords {
							if entry != nil {
								if entry.DeviceID == devID {
									entry.DeviceID = ""
								}
								newIDs := []string{}
								for _, id := range entry.DeviceIDs {
									if id != devID {
										newIDs = append(newIDs, id)
									}
								}
								entry.DeviceIDs = newIDs
							}
						}
						saveDB()
					}
					dbMutex.Unlock()
					sendTelegram(token, adminID, fmt.Sprintf("✅ Устройство `%s` удалено", devID), nil)

				} else if data == "backlist" {
					sendPasswordList(token, adminID, wgDev)
				}
			}

			// ═══ Текстовые команды ═══
			msg := u.Message
			if msg == nil || msg.Chat.ID != adminID {
				continue
			}

			cmd := strings.TrimSpace(msg.Text)

			// Обработка ввода количества дней
			if waitingForDays {
				waitingForDays = false
				parts := strings.Fields(cmd)
				if len(parts) == 0 {
					sendTelegram(token, adminID, "❌ Неверное значение. Укажите число от 1 до 365, или отправьте /new заново.", nil)
					continue
				}
				days, parseErr := strconv.Atoi(parts[0])
				if parseErr != nil || days < 1 || days > 365 {
					sendTelegram(token, adminID, "❌ Неверное значение. Укажите число от 1 до 365, или отправьте /new заново.", nil)
					continue
				}

				maxDevs := 1
				if len(parts) > 1 {
					if devs, err := strconv.Atoi(parts[1]); err == nil && devs >= 1 {
						maxDevs = devs
					}
				}

				tempDays = days
				tempMaxDevs = maxDevs
				
				var keyboard [][]map[string]interface{}
				keyboard = append(keyboard, []map[string]interface{}{
					{"text": "Да", "callback_data": "ports_def"},
					{"text": "Нет", "callback_data": "ports_custom"},
				})
				sendTelegram(token, adminID, "⚙️ Использовать стандартные порты (56000, 56001, 9000)?", map[string]interface{}{"inline_keyboard": keyboard})
				continue
			}

			if waitingForPorts {
				parts := strings.Split(cmd, ",")
				if len(parts) != 3 {
					sendTelegram(token, adminID, "❌ Неверный формат. Укажите 3 порта через запятую (например: 56000,56001,9000):", nil)
					continue
				}
				p1 := strings.TrimSpace(parts[0])
				p2 := strings.TrimSpace(parts[1])
				p3 := strings.TrimSpace(parts[2])
				
				if _, err := strconv.Atoi(p1); err != nil {
					sendTelegram(token, adminID, "❌ Неверный порт. Повторите ввод:", nil)
					continue
				}
				if _, err := strconv.Atoi(p2); err != nil {
					sendTelegram(token, adminID, "❌ Неверный порт. Повторите ввод:", nil)
					continue
				}
				if _, err := strconv.Atoi(p3); err != nil {
					sendTelegram(token, adminID, "❌ Неверный порт. Повторите ввод:", nil)
					continue
				}
				
				waitingForPorts = false
				tempPorts = fmt.Sprintf("%s,%s,%s", p1, p2, p3)
				waitingForHash = true
				sendTelegram(token, adminID, "🔑 Укажите VK хеш (или несколько через запятую):", nil)
				continue
			}

			if waitingForHash {
				hash := strings.ReplaceAll(cmd, " ", "")
				if strings.Contains(hash, "http") || strings.Contains(hash, "/") {
					sendTelegram(token, adminID, "❌ Пожалуйста, отправьте только хеш (или несколько хешей через запятую). Ссылки не поддерживаются.", nil)
					continue
				}
				if hash == "" {
					sendTelegram(token, adminID, "❌ Хеш не должен быть пустым.", nil)
					continue
				}
				waitingForHash = false

				if targetPassword == "main" {
					targetPassword = ""
					srvIP := getPublicIP()
					pts := strings.Split(tempPorts, ",")
					link := fmt.Sprintf("wdtt://%s:%s:%s:%s:%s:%s", srvIP, pts[0], pts[1], pts[2], db.MainPassword, hash)

					nameEsc := neturl.QueryEscape(fmt.Sprintf("qWDTT - Main (%s)", srvIP))
					peerEsc := neturl.QueryEscape(srvIP)
					hashesEsc := neturl.QueryEscape(hash)
					passEsc := neturl.QueryEscape(db.MainPassword)
					qwdttLink := fmt.Sprintf("qwdtt://config?name=%s&peer=%s&hashes=%s&workers=16&port=9000&pass=%s", nameEsc, peerEsc, hashesEsc, passEsc)

					msgText := fmt.Sprintf("🔗 *Ссылка для главного пароля:*\n`%s`\n\n🔗 *Быстрая ссылка qWDTT:* `%s`", link, qwdttLink)
					sendTelegram(token, adminID, msgText, nil)

					configJSON := fmt.Sprintf(`{
  "name": "qWDTT - Main (%s)",
  "peer": "%s",
  "vkHashes": "%s",
  "workersPerHash": 16,
  "listenPort": 9000,
  "password": "%s"
}`, srvIP, srvIP, hash, db.MainPassword)
					fileName := fmt.Sprintf("qwdtt_main_%s.conf", srvIP)
					sendTelegramFile(token, adminID, fileName, []byte(configJSON))
					continue
				}

				dbMutex.Lock()
				if cleanupExpiredPasswordsLocked(wgDev) > 0 {
					saveDB()
				}
				if len(db.Passwords) >= maxGeneratedPasswords {
					dbMutex.Unlock()
					sendTelegram(token, adminID, fmt.Sprintf("❌ Лимит паролей: максимум %d активных. Удалите ненужный пароль через /list.", maxGeneratedPasswords), nil)
					continue
				}
				newPass := ""
				for i := 0; i < 10; i++ {
					candidate := generatePassword()
					if _, exists := db.Passwords[candidate]; !exists {
						newPass = candidate
						break
					}
				}
				if newPass == "" {
					dbMutex.Unlock()
					sendTelegram(token, adminID, "❌ Не удалось создать уникальный пароль. Повторите /new.", nil)
					continue
				}
				if err := serverWrapKeys.AddPassword(newPass); err != nil {
					dbMutex.Unlock()
					sendTelegram(token, adminID, "❌ Не удалось создать WRAP-ключ для пароля. Повторите /new.", nil)
					continue
				}
				expiresAt := time.Now().Add(time.Duration(tempDays) * 24 * time.Hour).Unix()
				newLabel := nextPasswordLabel()
				db.Passwords[newPass] = &PasswordEntry{
					Label:      newLabel,
					ExpiresAt:  expiresAt,
					MaxDevices: tempMaxDevs,
					VkHash:     hash,
					Ports:      tempPorts,
				}
				saveDB()
				dbMutex.Unlock()

				expDate := time.Unix(expiresAt, 0).Format("02.01.2006")
				srvIP := getPublicIP()
				pts := strings.Split(tempPorts, ",")
				link := fmt.Sprintf("wdtt://%s:%s:%s:%s:%s:%s", srvIP, pts[0], pts[1], pts[2], newPass, hash)

				nameEsc := neturl.QueryEscape(newLabel)
				peerEsc := neturl.QueryEscape(srvIP)
				hashesEsc := neturl.QueryEscape(hash)
				passEsc := neturl.QueryEscape(newPass)
				qwdttLink := fmt.Sprintf("qwdtt://config?name=%s&peer=%s&hashes=%s&workers=16&port=9000&pass=%s", nameEsc, peerEsc, hashesEsc, passEsc)

				msgText := fmt.Sprintf("👤 Имя: *%s*\n🔑 Новый пароль:\n`%s`\n\n⏰ Действует %d дн. (до %s)\n📱 Лимит: %d устройств\nОжидает первого подключения\n\n🔗 *Быстрая ссылка qWDTT:* `%s`\n\n🔗 *Legacy ссылка:* `%s`", newLabel, newPass, tempDays, expDate, tempMaxDevs, qwdttLink, link)
				sendTelegram(token, adminID, msgText, nil)

				configJSON := fmt.Sprintf(`{
  "name": "%s",
  "peer": "%s",
  "vkHashes": "%s",
  "workersPerHash": 16,
  "listenPort": 9000,
  "password": "%s"
}`, newLabel, srvIP, hash, newPass)
				fileName := fmt.Sprintf("qwdtt_%s.conf", newPass)
				sendTelegramFile(token, adminID, fileName, []byte(configJSON))
				continue
			}

			if cmd == "/start" || cmd == "/help" {
				sendTelegram(token, adminID, "🤖 *qWDTT VPN Manager*\n\n/new — Создать пароль\n/list — Список паролей", nil)

			} else if strings.HasPrefix(cmd, "/new ") || cmd == "/new" {
				args := strings.Fields(strings.TrimPrefix(cmd, "/new"))
				if len(args) >= 1 {
					days, parseErr := strconv.Atoi(args[0])
					if parseErr == nil && days >= 1 && days <= 365 {
						maxDevs := 1
						if len(args) >= 2 {
							if devs, err := strconv.Atoi(args[1]); err == nil && devs >= 1 {
								maxDevs = devs
							}
						}

						tempDays = days
						tempMaxDevs = maxDevs

						var keyboard [][]map[string]interface{}
						keyboard = append(keyboard, []map[string]interface{}{
							{"text": "Да", "callback_data": "ports_def"},
							{"text": "Нет", "callback_data": "ports_custom"},
						})
						sendTelegram(token, adminID, "⚙️ Использовать стандартные порты (56000, 56001, 9000)?", map[string]interface{}{"inline_keyboard": keyboard})
						continue
					}
				}
				dbMutex.Lock()
				if cleanupExpiredPasswordsLocked(wgDev) > 0 {
					saveDB()
				}
				if len(db.Passwords) >= maxGeneratedPasswords {
					dbMutex.Unlock()
					sendTelegram(token, adminID, fmt.Sprintf("❌ Лимит паролей: максимум %d активных. Удалите ненужный пароль через /list.", maxGeneratedPasswords), nil)
					continue
				}
				dbMutex.Unlock()
				waitingForDays = true
				sendTelegram(token, adminID, "📅 Введите срок действия пароля в днях (1–365) и (опционально) лимит устройств через пробел:\n\n_Примеры:_\n`30` — месяц, 1 устройство\n`30 3` — месяц, до 3 устройств", nil)

			} else if cmd == "/list" {
				sendPasswordList(token, adminID, wgDev)
			}
		}
	}
}

func removePeerFromWG(wgDev *device.Device, dev *ClientDevice) {
	if wgDev == nil || dev == nil || dev.PubKey == "" {
		return
	}
	pubHex, err := b64ToHex(dev.PubKey)
	if err != nil {
		return
	}
	wgDev.IpcSet(fmt.Sprintf("public_key=%s\nremove=true\n", pubHex))
}

func upsertPeerInWG(wgDev *device.Device, dev *ClientDevice) {
	if wgDev == nil || dev == nil || dev.PubKey == "" || dev.IP == "" {
		return
	}
	pubHex, err := b64ToHex(dev.PubKey)
	if err != nil {
		return
	}
	wgDev.IpcSet(fmt.Sprintf("public_key=%s\nallowed_ip=%s/32\n", pubHex, dev.IP))
}

func cleanupExpiredPasswordsLocked(wgDev *device.Device) int {
	removed := 0
	for p, entry := range db.Passwords {
		if isPasswordExpired(entry) {
			if entry != nil && entry.DeviceID != "" {
				removePeerFromWG(wgDev, db.Devices[entry.DeviceID])
				delete(db.Devices, entry.DeviceID)
			}
			delete(db.Passwords, p)
			serverWrapKeys.RemovePassword(p)
			removed++
		}
	}
	return removed
}

func cleanupExpiredPasswords(wgDev *device.Device) int {
	dbMutex.Lock()
	defer dbMutex.Unlock()
	removed := cleanupExpiredPasswordsLocked(wgDev)
	if removed > 0 {
		saveDB()
	}
	return removed
}

func expiredPasswordJanitor(ctx context.Context, wgDev *device.Device) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if removed := cleanupExpiredPasswords(wgDev); removed > 0 {
				log.Printf("[DB] Удалено истёкших паролей: %d", removed)
			}
		}
	}
}

func syncPersistedPeersToWG(wgDev *device.Device) {
	dbMutex.Lock()
	defer dbMutex.Unlock()
	count := 0
	for _, dev := range db.Devices {
		upsertPeerInWG(wgDev, dev)
		count++
	}
	if count > 0 {
		log.Printf("[WG] Восстановлено сохранённых устройств: %d", count)
	}
}

func sendPasswordList(token string, adminID int64, wgDev *device.Device) {
	dbMutex.Lock()
	defer dbMutex.Unlock()

	// Очистка истёкших
	if cleanupExpiredPasswordsLocked(wgDev) > 0 {
		saveDB()
	}

	txt := "🔐 *Пароли:*\n\n"
	txt += fmt.Sprintf("🔒 Главный: `%s` (владелец)\n\n", db.MainPassword)

	var inlineKb []map[string]interface{}
	inlineKb = append(inlineKb, map[string]interface{}{
		"text":          "🔗 Ссылка на главный пароль",
		"callback_data": "mainlink",
	})

	if len(db.Passwords) == 0 {
		txt += "_Нет сгенерированных паролей._\n"
	} else {
		txt += fmt.Sprintf("_Активно: %d/%d_\n\n", len(db.Passwords), maxGeneratedPasswords)
		index := 0
		for p, entry := range db.Passwords {
			index++
			label := passwordEntryLabel(entry, p, index)
			status := "🟢"
			if entry.DeviceID != "" || len(entry.DeviceIDs) > 0 {
				status = "🔗"
			}
			expiry := "♾"
			if entry.ExpiresAt > 0 {
				remaining := time.Until(time.Unix(entry.ExpiresAt, 0))
				if remaining > 0 {
					expiry = fmt.Sprintf("%dd", int(remaining.Hours()/24)+1)
				} else {
					expiry = "❌"
				}
			}
			txt += fmt.Sprintf("%s *%s* (%s)\n", status, label, expiry)
			inlineKb = append(inlineKb, map[string]interface{}{
				"text":          "🔍 " + label,
				"callback_data": "viewpass_" + p,
			})
		}
	}

	txt += "\n🟢 = свободен | 🔗 = привязан"

	var replyMarkup interface{}
	if len(inlineKb) > 0 {
		var keyboard [][]map[string]interface{}
		for _, btn := range inlineKb {
			keyboard = append(keyboard, []map[string]interface{}{btn})
		}
		replyMarkup = map[string]interface{}{"inline_keyboard": keyboard}
	}
	sendTelegram(token, adminID, txt, replyMarkup)
}

func answerCallback(token, callbackID string) {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/answerCallbackQuery", token)
	payload := map[string]interface{}{"callback_query_id": callbackID}
	body, _ := json.Marshal(payload)
	http.Post(url, "application/json", bytes.NewBuffer(body))
}

func maskPassword(pass string) string {
	if len(pass) <= 3 {
		return pass
	}
	return pass[:3] + "****"
}

func sendTelegram(token string, chatID int64, text string, replyMarkup interface{}) {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)
	payload := map[string]interface{}{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "Markdown",
	}
	if replyMarkup != nil {
		payload["reply_markup"] = replyMarkup
	}
	body, _ := json.Marshal(payload)
	http.Post(url, "application/json", bytes.NewBuffer(body))
}

func sendTelegramFile(token string, chatID int64, fileName string, fileContent []byte) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("document", fileName)
	if err != nil {
		log.Println("[BOT] Error creating form file:", err)
		return
	}
	_, err = part.Write(fileContent)
	if err != nil {
		log.Println("[BOT] Error writing file content:", err)
		return
	}
	err = writer.WriteField("chat_id", strconv.FormatInt(chatID, 10))
	if err != nil {
		log.Println("[BOT] Error writing chat_id field:", err)
		return
	}
	err = writer.Close()
	if err != nil {
		log.Println("[BOT] Error closing writer:", err)
		return
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendDocument", token)
	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		log.Println("[BOT] Error creating HTTP request:", err)
		return
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Println("[BOT] Error sending file to Telegram:", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		log.Printf("[BOT] sendTelegramFile failed with status %d: %s\n", resp.StatusCode, string(respBody))
	}
}

// ==================== Пул буферов ====================

var bufPool = sync.Pool{
	New: func() interface{} {
		b := make([]byte, 1600)
		return &b
	},
}

func getBuf() *[]byte  { return bufPool.Get().(*[]byte) }
func putBuf(b *[]byte) { bufPool.Put(b) }

// ==================== Оптимизация ====================

func enableBBR() {
	log.Println("[SYS] Оптимизация TCP...")
	out, _ := runCmd("bash", "-c", "sysctl net.ipv4.tcp_congestion_control")
	if strings.Contains(out, "bbr") {
		log.Println("[SYS] BBR уже активен ✓")
		return
	}
	cmds := [][]string{
		{"sysctl", "-w", "net.core.default_qdisc=fq"},
		{"sysctl", "-w", "net.ipv4.tcp_congestion_control=bbr"},
		{"sysctl", "-w", "net.core.rmem_max=25165824"},
		{"sysctl", "-w", "net.core.wmem_max=25165824"},
		{"sysctl", "-w", "net.ipv4.tcp_rmem=4096 87380 25165824"},
		{"sysctl", "-w", "net.ipv4.tcp_wmem=4096 65536 25165824"},
	}
	for _, cmd := range cmds {
		runCmd(cmd[0], cmd[1:]...)
	}
	log.Println("[SYS] BBR включен ✓")
}

// ==================== Статистика ====================

var (
	totalBytesFromClient int64
	totalBytesToClient   int64
	activeConns          int32
	totalConns           int64
	natType              string = "Инициализация..."
	serverStartTime      time.Time
	lastWGStats          = make(map[string]struct{ rx, tx int64 })
)

func updateTrafficFromWG() {
	if globalWgDev == nil {
		return
	}
	ipcOut, err := globalWgDev.IpcGet()
	if err != nil {
		return
	}

	dbMutex.Lock()
	defer dbMutex.Unlock()

	var currentPub string
	var rx, tx int64

	processPeer := func(pub string, currentRx, currentTx int64) {
		if pub == "" {
			return
		}
		last := lastWGStats[pub]
		deltaRx := currentRx - last.rx
		deltaTx := currentTx - last.tx
		if deltaRx < 0 || deltaTx < 0 {
			deltaRx = currentRx
			deltaTx = currentTx
		}
		lastWGStats[pub] = struct{ rx, tx int64 }{currentRx, currentTx}

		if deltaRx == 0 && deltaTx == 0 {
			return
		}

		var targetDevID string
		for devID, dev := range db.Devices {
			h, _ := b64ToHex(dev.PubKey)
			if h == pub {
				targetDevID = devID
				dev.UpBytes += deltaRx
				dev.DownBytes += deltaTx
				break
			}
		}

		if targetDevID == "" {
			return
		}

		foundEntry := false
		for _, entry := range db.Passwords {
			for _, id := range entry.DeviceIDs {
				if id == targetDevID {
					entry.UpBytes += deltaRx
					entry.DownBytes += deltaTx
					foundEntry = true
					break
				}
			}
			if entry.DeviceID == targetDevID {
				entry.UpBytes += deltaRx
				entry.DownBytes += deltaTx
				foundEntry = true
			}
		}

		if !foundEntry {
			atomic.AddInt64(&mainPassUp, deltaRx)
			atomic.AddInt64(&mainPassDown, deltaTx)
		}
	}

	for _, line := range strings.Split(ipcOut, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "public_key=") {
			processPeer(currentPub, rx, tx)
			currentPub = strings.TrimPrefix(line, "public_key=")
			rx, tx = 0, 0
		} else if strings.HasPrefix(line, "rx_bytes=") {
			fmt.Sscanf(line, "rx_bytes=%d", &rx)
		} else if strings.HasPrefix(line, "tx_bytes=") {
			fmt.Sscanf(line, "tx_bytes=%d", &tx)
		}
	}
	processPeer(currentPub, rx, tx)
}

func statsLoop(ctx context.Context, configDir string) {
	serverStartTime = time.Now()
	statsFile := filepath.Join(configDir, "server.log")
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	saveTicks := 0

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			updateTrafficFromWG()
			fromC := atomic.LoadInt64(&totalBytesFromClient)
			toC := atomic.LoadInt64(&totalBytesToClient)
			active := atomic.LoadInt32(&activeConns)
			total := atomic.LoadInt64(&totalConns)
			uptime := time.Since(serverStartTime)

			log.Printf("[СТАТ] Активных: %d | Всего: %d | NAT: %s | ↑%.2f МБ | ↓%.2f МБ",
				active, total, natType,
				float64(fromC)/1024/1024,
				float64(toC)/1024/1024,
			)

			// Пишем server.log и периодически сохраняем БД на диск
			dbMutex.Lock()
			numPasswords := len(db.Passwords)
			numDevices := len(db.Devices)
			saveTicks++
			if saveTicks >= 6 { // 6 * 10 секунд = 60 секунд
				saveTicks = 0
				saveDB()
			}
			dbMutex.Unlock()

			uptimeStr := formatUptime(uptime)
			downGB := float64(toC) / (1024 * 1024 * 1024)
			upGB := float64(fromC) / (1024 * 1024 * 1024)

			statsJSON, _ := json.Marshal(map[string]interface{}{
				"active":    active,
				"total":     total,
				"nat":       natType,
				"uptime":    uptimeStr,
				"down_gb":   fmt.Sprintf("%.2f", downGB),
				"up_gb":     fmt.Sprintf("%.2f", upGB),
				"passwords": numPasswords,
				"devices":   numDevices,
				"timestamp": time.Now().Unix(),
			})
			os.WriteFile(statsFile, statsJSON, 0644)
		}
	}
}

func formatUptime(d time.Duration) string {
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	if days > 0 {
		return fmt.Sprintf("%dд %dч %dм", days, hours, mins)
	}
	if hours > 0 {
		return fmt.Sprintf("%dч %dм", hours, mins)
	}
	return fmt.Sprintf("%dм", mins)
}

// ==================== Утилиты ====================

func runCmd(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func runCmdSilent(name string, args ...string) string {
	out, _ := exec.Command(name, args...).CombinedOutput()
	return strings.TrimSpace(string(out))
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func isNetTimeout(err error) bool {
	ne, ok := err.(net.Error)
	return ok && ne.Timeout()
}

func getDefaultInterface() string {
	out := runCmdSilent("bash", "-c", "ip route show default | awk '/default/ {print $5}' | head -1")
	if out != "" {
		return strings.TrimSpace(out)
	}
	out = runCmdSilent("bash", "-c", "ip -o link show | awk -F': ' '{print $2}' | grep -v -E 'lo|wg|tun|wdtt' | head -1")
	if out != "" {
		return strings.TrimSpace(out)
	}
	return "eth0"
}

// ==================== Ключи ====================

type wgKeys struct {
	serverPrivate, serverPublic, clientPrivate, clientPublic string
}

func b64ToHex(s string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return "", err
	}
	if len(b) != 32 {
		return "", fmt.Errorf("key length %d != 32", len(b))
	}
	return hex.EncodeToString(b), nil
}

func generateKeyPair() (privB64, pubB64 string, err error) {
	var priv [32]byte
	if _, err := rand.Read(priv[:]); err != nil {
		return "", "", err
	}
	priv[0] &= 248
	priv[31] = (priv[31] & 127) | 64
	pub, err := curve25519.X25519(priv[:], curve25519.Basepoint)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(priv[:]),
		base64.StdEncoding.EncodeToString(pub), nil
}

func loadOrGenerateKeys(dir string) (*wgKeys, error) {
	f := filepath.Join(dir, "wg-keys.dat")
	if data, err := os.ReadFile(f); err == nil {
		lines := strings.Split(strings.TrimSpace(string(data)), "\n")
		if len(lines) >= 4 {
			keys := &wgKeys{
				serverPrivate: strings.TrimSpace(lines[0]),
				serverPublic:  strings.TrimSpace(lines[1]),
				clientPrivate: strings.TrimSpace(lines[2]),
				clientPublic:  strings.TrimSpace(lines[3]),
			}
			for _, k := range []string{keys.serverPrivate, keys.serverPublic,
				keys.clientPrivate, keys.clientPublic} {
				if _, err := b64ToHex(k); err != nil {
					goto generate
				}
			}
			log.Printf("[WG] Ключи загружены из %s", f)
			return keys, nil
		}
	}
generate:
	log.Println("[WG] Генерирую новые ключи...")
	sPriv, sPub, err := generateKeyPair()
	if err != nil {
		return nil, err
	}
	cPriv, cPub, err := generateKeyPair()
	if err != nil {
		return nil, err
	}
	keys := &wgKeys{sPriv, sPub, cPriv, cPub}
	os.MkdirAll(dir, 0700)
	os.WriteFile(f, []byte(fmt.Sprintf("%s\n%s\n%s\n%s\n",
		keys.serverPrivate, keys.serverPublic,
		keys.clientPrivate, keys.clientPublic)), 0600)
	log.Printf("[WG] Ключи сохранены в %s", f)
	return keys, nil
}

// ==================== NAT ====================

func setupFullConeNAT(wgIface string) error {
	log.Println("[NAT] ══════════════════════════════════════")

	os.WriteFile("/proc/sys/net/ipv4/ip_forward", []byte("1"), 0644)

	extIface := getDefaultInterface()
	log.Printf("[NAT] Внешний: %s", extIface)

	switch {
	case commandExists("iptables"):
		for i := 0; i < 5; i++ {
			exec.Command("iptables", "-t", "nat", "-D", "POSTROUTING", "-s", wgServerCIDR, "-o", extIface, "-m", "comment", "--comment", "WDTT_MANAGED", "-j", "MASQUERADE").Run()
		}
		exec.Command("iptables", "-t", "nat", "-I", "POSTROUTING", "1", "-s", wgServerCIDR, "-o", extIface, "-m", "comment", "--comment", "WDTT_MANAGED", "-j", "MASQUERADE").Run()
		natType = "MASQUERADE iptables ✅"
		setupForwardRules(wgIface)
	case commandExists("nft"):
		setupNftNAT(extIface)
		natType = "MASQUERADE nft ✅"
		setupForwardRules(wgIface)
	default:
		natType = "NAT не настроен: нет iptables/nft"
		log.Printf("[NAT] WARNING: %s", natType)
	}

	log.Printf("[NAT] Режим: %s", natType)
	log.Println("[NAT] ══════════════════════════════════════")
	return nil
}

func setupNftNAT(extIface string) {
	exec.Command("nft", "add", "table", "ip", "wdtt").Run()
	exec.Command("nft", "add", "chain", "ip", "wdtt", "postrouting", "{ type nat hook postrouting priority 100; }").Run()
	exec.Command("nft", "add", "rule", "ip", "wdtt", "postrouting", "ip", "saddr", wgServerCIDR, "oifname", extIface, "masquerade").Run()
}

func setupForwardRules(wgIface string) {
	if commandExists("iptables") {
		for i := 0; i < 5; i++ {
			exec.Command("iptables", "-D", "FORWARD", "-i", wgIface, "-m", "comment", "--comment", "WDTT_MANAGED", "-j", "ACCEPT").Run()
			exec.Command("iptables", "-D", "FORWARD", "-o", wgIface, "-m", "comment", "--comment", "WDTT_MANAGED", "-j", "ACCEPT").Run()
		}
		exec.Command("iptables", "-A", "FORWARD", "-i", wgIface, "-m", "comment", "--comment", "WDTT_MANAGED", "-j", "ACCEPT").Run()
		exec.Command("iptables", "-A", "FORWARD", "-o", wgIface, "-m", "comment", "--comment", "WDTT_MANAGED", "-j", "ACCEPT").Run()
		return
	}
	if commandExists("nft") {
		exec.Command("nft", "add", "table", "inet", "wdtt").Run()
		exec.Command("nft", "add", "chain", "inet", "wdtt", "forward", "{ type filter hook forward priority 0; policy accept; }").Run()
		exec.Command("nft", "add", "rule", "inet", "wdtt", "forward", "iifname", wgIface, "accept").Run()
		exec.Command("nft", "add", "rule", "inet", "wdtt", "forward", "oifname", wgIface, "accept").Run()
	}
}

// ==================== WireGuard ====================

func startUserspaceWG(keys *wgKeys, wgPort int) (*device.Device, error) {
	runCmdSilent("ip", "link", "del", wgIfaceName)
	time.Sleep(100 * time.Millisecond)

	tunDev, err := tun.CreateTUN(wgIfaceName, wgMTU)
	if err != nil {
		return nil, fmt.Errorf("CreateTUN: %w", err)
	}

	ifaceName, err := tunDev.Name()
	if err != nil {
		tunDev.Close()
		return nil, fmt.Errorf("TUN name: %w", err)
	}

	logger := device.NewLogger(device.LogLevelError, "[WG] ")
	bind := conn.NewDefaultBind()
	dev := device.NewDevice(tunDev, bind, logger)

	serverPrivHex, _ := b64ToHex(keys.serverPrivate)

	if err := dev.IpcSet(fmt.Sprintf(
		"private_key=%s\nlisten_port=%d\n",
		serverPrivHex, wgPort,
	)); err != nil {
		dev.Close()
		return nil, fmt.Errorf("IpcSet: %w", err)
	}

	for _, d := range db.Devices {
		pubHex, _ := b64ToHex(d.PubKey)
		if pubHex != "" {
			dev.IpcSet(fmt.Sprintf("public_key=%s\nallowed_ip=%s/32\n", pubHex, d.IP))
		}
	}

	if err := dev.Up(); err != nil {
		dev.Close()
		return nil, fmt.Errorf("device.Up: %w", err)
	}

	if err := configureInterface(ifaceName); err != nil {
		dev.Close()
		return nil, err
	}

	if err := setupFullConeNAT(ifaceName); err != nil {
		dev.Close()
		return nil, err
	}

	go func() {
		uapiFile, err := ipc.UAPIOpen(ifaceName)
		if err != nil {
			return
		}
		uapi, err := ipc.UAPIListen(ifaceName, uapiFile)
		if err != nil {
			return
		}
		defer uapi.Close()
		for {
			c, err := uapi.Accept()
			if err != nil {
				return
			}
			go dev.IpcHandle(c)
		}
	}()

	log.Printf("[WG] Запущен на порту %d", wgPort)
	return dev, nil
}

func configureInterface(ifaceName string) error {
	for _, cmd := range [][]string{
		{"ip", "addr", "add", wgServerCIDR, "dev", ifaceName},
		{"ip", "link", "set", "mtu", fmt.Sprintf("%d", wgMTU), "dev", ifaceName},
		{"ip", "link", "set", ifaceName, "up"},
	} {
		out, err := runCmd(cmd[0], cmd[1:]...)
		if err != nil && !strings.Contains(out, "File exists") {
			return fmt.Errorf("%s: %s", strings.Join(cmd, " "), out)
		}
	}
	return nil
}

func buildClientConfig(serverPublic, clientPrivate, clientIP, clientPort string) string {
	return fmt.Sprintf(`[Interface]
PrivateKey = %s
Address = %s/32
DNS = %s
MTU = %d

[Peer]
PublicKey = %s
AllowedIPs = 0.0.0.0/0
Endpoint = 127.0.0.1:%s
PersistentKeepalive = %d`,
		clientPrivate, clientIP, dns, wgMTU,
		serverPublic, clientPort, keepalive,
	)
}

// ==================== HTTP Control API ====================

func unbindDevices(entry *PasswordEntry, targetDeviceID string) {
	if targetDeviceID == "" {
		if entry.DeviceID != "" {
			removeDeviceFromSystem(entry.DeviceID)
			entry.DeviceID = ""
		}
		for _, id := range entry.DeviceIDs {
			removeDeviceFromSystem(id)
		}
		entry.DeviceIDs = nil
	} else {
		if entry.DeviceID == targetDeviceID {
			entry.DeviceID = ""
		}
		newIDs := []string{}
		for _, id := range entry.DeviceIDs {
			if id == targetDeviceID {
				removeDeviceFromSystem(id)
			} else {
				newIDs = append(newIDs, id)
			}
		}
		entry.DeviceIDs = newIDs
		if len(entry.DeviceIDs) == 1 {
			entry.DeviceID = entry.DeviceIDs[0]
		} else if len(entry.DeviceIDs) > 1 {
			entry.DeviceID = "multi"
		}
	}
}

func removeDeviceFromSystem(devID string) {
	dev, exists := db.Devices[devID]
	if !exists {
		return
	}
	delete(db.Devices, devID)
	if globalWgDev != nil {
		pubHex, _ := b64ToHex(dev.PubKey)
		globalWgDev.IpcSet(fmt.Sprintf("public_key=%s\nremove=true\n", pubHex))
	}
}

func handleAPIProfileStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	password := r.FormValue("password")
	if password == "" {
		http.Error(w, `{"error":"Missing password"}`, http.StatusBadRequest)
		return
	}

	dbMutex.Lock()
	defer dbMutex.Unlock()

	entry, exists := db.Passwords[password]
	if !exists || isPasswordExpired(entry) {
		http.Error(w, `{"error":"Unauthorized or expired password"}`, http.StatusUnauthorized)
		return
	}

	maxDevs := entry.MaxDevices
	if maxDevs <= 0 {
		maxDevs = 1
	}

	boundDevices := len(entry.DeviceIDs)
	if boundDevices == 0 && entry.DeviceID != "" {
		boundDevices = 1
	}

	deviceID := r.FormValue("device_id")
	isCurrentBound := false

	activeCount := 0
	activeDevicesMu.Lock()
	if len(entry.DeviceIDs) == 0 && entry.DeviceID != "" {
		if entry.DeviceID == deviceID {
			isCurrentBound = true
		}
		if count := activeDevices[entry.DeviceID]; count > 0 {
			activeCount = 1
		}
	} else {
		for _, id := range entry.DeviceIDs {
			if id == deviceID {
				isCurrentBound = true
			}
			if count := activeDevices[id]; count > 0 {
				activeCount++
			}
		}
	}
	activeDevicesMu.Unlock()

	resp := map[string]interface{}{
		"max_devices":      maxDevs,
		"bound_devices":    boundDevices,
		"active_devices":   activeCount,
		"is_current_bound": isCurrentBound,
		"expires_at":       entry.ExpiresAt,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func handleAPIProfileUnbind(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	password := r.FormValue("password")
	deviceID := r.FormValue("device_id")
	if password == "" {
		http.Error(w, `{"error":"Missing password"}`, http.StatusBadRequest)
		return
	}

	dbMutex.Lock()
	defer dbMutex.Unlock()

	entry, exists := db.Passwords[password]
	if !exists || isPasswordExpired(entry) {
		http.Error(w, `{"error":"Unauthorized or expired password"}`, http.StatusUnauthorized)
		return
	}

	unbindDevices(entry, deviceID)
	saveDB()

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"success":true}`))
}

// ==================== Main ====================

func main() {
	listen := flag.String("listen", "0.0.0.0:56000", "DTLS адрес")
	wgPort := flag.Int("wg-port", defaultInternalWGPort, "WireGuard UDP порт")
	configDir := flag.String("config-dir", "/etc/wdtt", "директория конфигурации")
	mainPass := flag.String("password", "", "пароль владельца")
	adminID := flag.String("admin", "", "Telegram Admin ID")
	botToken := flag.String("bot-token", "", "Telegram Bot Token")
	dnsFlag := flag.String("dns", "8.8.8.8", "DNS серверы для клиентов")
	flag.Parse()
	dns = *dnsFlag

	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	log.Println("══════════════════════════════════════════")
	log.Println("   WDTT Server v2 (Multi-User)")
	log.Println("══════════════════════════════════════════")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sig := make(chan os.Signal, 2)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	var wgDev *device.Device
	go func() {
		for s := range sig {
			if s == syscall.SIGHUP {
				log.Println("[SYS] Получен сигнал SIGHUP. Перезагрузка базы паролей...")
				if wgDev == nil {
					log.Println("[ERR] WireGuard еще не запущен, пропуск SIGHUP")
					continue
				}
				if err := reloadDB(wgDev); err != nil {
					log.Printf("[ERR] Ошибка перезагрузки базы паролей: %v", err)
				} else {
					log.Println("[SYS] База паролей успешно перезагружена! Активных ключей в памяти:", serverWrapKeys.Count())
				}
			} else {
				cancel()
				time.Sleep(2 * time.Second)
				os.Exit(0)
			}
		}
	}()

	initDB(*configDir, *mainPass, *adminID, *botToken)

	keys, err := loadOrGenerateKeys(*configDir)
	if err != nil {
		log.Fatalf("[WG] Ключи: %v", err)
	}

	enableBBR()

	wgDev, err = startUserspaceWG(keys, *wgPort)
	if err != nil {
		log.Fatalf("[WG] Запуск: %v", err)
	}
	globalWgDev = wgDev
	if removed := cleanupExpiredPasswords(wgDev); removed > 0 {
		log.Printf("[DB] Удалено истёкших паролей при старте: %d", removed)
	}
	syncPersistedPeersToWG(wgDev)
	defer func() {
		wgDev.Close()
		runCmdSilent("ip", "link", "del", wgIfaceName)
	}()

	go statsLoop(ctx, *configDir)
	go expiredPasswordJanitor(ctx, wgDev)
	go botLoop(*botToken, *adminID, wgDev)

	// Запуск HTTP Control API
	go func() {
		mux := http.NewServeMux()
		mux.HandleFunc("/api/profile/status", handleAPIProfileStatus)
		mux.HandleFunc("/api/profile/unbind", handleAPIProfileUnbind)

		log.Printf("[API] Запуск HTTP API на %s (TCP)...", *listen)
		if err := http.ListenAndServe(*listen, mux); err != nil {
			log.Printf("[API] [ERR] Ошибка запуска HTTP API: %v", err)
		}
	}()

	addr, _ := net.ResolveUDPAddr("udp", *listen)
	cert, _ := selfsign.GenerateSelfSigned()
	if serverWrapKeys.Count() == 0 {
		log.Fatalf("[WRAP] нет активных паролей для WRAP")
	}

	wrapListener, err := listenWrapped(addr, serverWrapKeys)
	if err != nil {
		log.Fatalf("[WRAP] %v", err)
	}

	listener, err := dtls.NewListenerWithOptions(wrapListener, dtls.WithCertificates(cert), dtls.WithExtendedMasterSecret(dtls.RequireExtendedMasterSecret), dtls.WithCipherSuites(dtls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256), dtls.WithConnectionIDGenerator(dtls.RandomCIDGenerator(8)), dtls.WithMTU(1100))
	if err != nil {
		log.Fatalf("[DTLS] %v", err)
	}
	context.AfterFunc(ctx, func() { listener.Close() })

	wgEndpoint := fmt.Sprintf("127.0.0.1:%d", *wgPort)

	log.Printf("   DTLS: %s | WG: %s | NAT: %s", *listen, wgEndpoint, natType)
	log.Printf("   WRAP: password HKDF + RTP AEAD | keys: %d", serverWrapKeys.Count())
	log.Println("[SERVER] Готов")

	var wg sync.WaitGroup
	for {
		dtlsConn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				wg.Wait()
				return
			default:
			}
			continue
		}
		wg.Add(1)
		go func(c net.Conn) {
			defer wg.Done()
			defer c.Close()
			handleConn(ctx, c, wgEndpoint, wgDev, keys)
		}(dtlsConn)
	}
}

// ==================== Обработка соединений ====================

func handleConn(ctx context.Context, clientConn net.Conn, wgEndpoint string, wgDev *device.Device, keys *wgKeys) {
	atomic.AddInt64(&totalConns, 1)

	var connDeviceID string

	dtlsConn, ok := clientConn.(*dtls.Conn)
	if !ok {
		return
	}

	hctx, hcancel := context.WithTimeout(ctx, 60*time.Second)
	if err := dtlsConn.HandshakeContext(hctx); err != nil {
		hcancel()
		log.Printf("[DTLS] [ERR] Handshake failed from %s: %v", clientConn.RemoteAddr().String(), err)
		return
	}
	hcancel()

	atomic.AddInt32(&activeConns, 1)
	defer atomic.AddInt32(&activeConns, -1)

	buf := make([]byte, 1600)
	clientConn.SetReadDeadline(time.Now().Add(30 * time.Second))
	n, err := clientConn.Read(buf)
	if err != nil {
		return
	}
	clientConn.SetReadDeadline(time.Time{})

	firstPacket := buf[:n]
	firstStr := string(firstPacket)

	if strings.HasPrefix(firstStr, "GETCONF:") {
		parts := strings.Split(strings.TrimSpace(strings.TrimPrefix(firstStr, "GETCONF:")), "|")
		clientPort := "9000"
		deviceID := "unknown"
		password := ""
		if len(parts) > 0 {
			clientPort = parts[0]
		}
		if len(parts) > 1 {
			deviceID = parts[1]
		}
		if len(parts) > 2 {
			password = parts[2]
		}

		dbMutex.Lock()

		// Проверяем пароль
		isMainPass := password != "" && password == db.MainPassword
		entry, isGenPass := db.Passwords[password]
		valid := isMainPass || (isGenPass && !isPasswordExpired(entry))

		// Для сгенерированных паролей — проверяем привязку к устройству
		if valid && isGenPass && entry.IsDeactivated {
			clientConn.Write([]byte("DENIED:deactivated"))
			log.Printf("[WG] Отказ: пароль %s деактивирован, запрос от %s", maskPassword(password), deviceID)
			dbMutex.Unlock()
		} else if valid && isGenPass && !entry.canConnectAndBind(deviceID) {
			// Достигнут лимит устройств или привязано к другому устройству
			clientConn.Write([]byte("DENIED:device_mismatch"))
			log.Printf("[WG] Отказ: пароль %s достиг лимита устройств (%d), запрос от %s", maskPassword(password), entry.MaxDevices, deviceID)
			dbMutex.Unlock()
		} else if valid {
			connDeviceID = deviceID

			// Сохраняем БД, так как canConnectAndBind мог внести привязку нового устройства
			if isGenPass {
				saveDB()
			}

			dev, exists := db.Devices[deviceID]
			if !exists {
				dev = &ClientDevice{DeviceID: deviceID, IP: getNextIP()}
				privB64, pubB64, keyErr := generateKeyPair()
				if keyErr == nil && dev.IP != "" {
					dev.PrivKey = privB64
					dev.PubKey = pubB64
					db.Devices[deviceID] = dev
					saveDB()
					log.Printf("[WG] Новое устройство %s (IP: %s)", deviceID, dev.IP)
				} else {
					dev = nil
				}
			}
			if dev != nil {
				upsertPeerInWG(wgDev, dev)
				clientConn.Write([]byte(buildClientConfig(keys.serverPublic, dev.PrivKey, dev.IP, clientPort)))
			} else {
				clientConn.Write([]byte("NOCONF"))
			}
			dbMutex.Unlock()
		} else {
			if isGenPass && isPasswordExpired(entry) {
				clientConn.Write([]byte("DENIED:expired"))
				log.Printf("[WG] Отказ: пароль %s истёк, от %s", maskPassword(password), deviceID)
			} else {
				clientConn.Write([]byte("DENIED:wrong_password"))
				log.Printf("[WG] Отказ (неверный пароль) от %s", deviceID)
			}
			dbMutex.Unlock()
		}

		clientConn.SetReadDeadline(time.Now().Add(5 * time.Minute))
		n, err = clientConn.Read(buf)
		if err != nil {
			return
		}
		clientConn.SetReadDeadline(time.Time{})
		firstPacket = buf[:n]
		firstStr = string(firstPacket)
	} else if strings.HasPrefix(firstStr, "AUTH:") {
		parts := strings.Split(strings.TrimSpace(strings.TrimPrefix(firstStr, "AUTH:")), "|")
		deviceID := "unknown"
		if len(parts) > 0 {
			deviceID = parts[0]
		}

		connDeviceID = deviceID

		clientConn.SetReadDeadline(time.Now().Add(5 * time.Minute))
		n, err = clientConn.Read(buf)
		if err != nil {
			return
		}
		clientConn.SetReadDeadline(time.Time{})
		firstPacket = buf[:n]
		firstStr = string(firstPacket)
	}

	if firstStr == "READY" {
		clientConn.Write([]byte("READY_OK"))
		clientConn.SetReadDeadline(time.Now().Add(10 * time.Minute))
		n, err = clientConn.Read(buf)
		if err != nil {
			return
		}
		clientConn.SetReadDeadline(time.Time{})
		firstPacket = buf[:n]
	}

	// WG прокси
	wgConn, err := net.Dial("udp", wgEndpoint)
	if err != nil {
		return
	}
	defer wgConn.Close()

	if uc, ok := wgConn.(*net.UDPConn); ok {
		uc.SetReadBuffer(2 * 1024 * 1024)
		uc.SetWriteBuffer(2 * 1024 * 1024)
	}

	if _, err := wgConn.Write(firstPacket); err != nil {
		return
	}
	atomic.AddInt64(&totalBytesFromClient, int64(len(firstPacket)))

	// Трекинг онлайн-статуса
	if connDeviceID != "" {
		activeDevicesMu.Lock()
		activeDevices[connDeviceID]++
		activeDevicesMu.Unlock()
		defer func() {
			activeDevicesMu.Lock()
			activeDevices[connDeviceID]--
			if activeDevices[connDeviceID] <= 0 {
				delete(activeDevices, connDeviceID)
			}
			activeDevicesMu.Unlock()
		}()
	}

	pctx, pcancel := context.WithCancel(ctx)
	defer pcancel()

	context.AfterFunc(pctx, func() {
		clientConn.SetDeadline(time.Now())
		wgConn.SetDeadline(time.Now())
	})

	var proxyWg sync.WaitGroup
	proxyWg.Add(2)

	// Клиент → WG
	go func() {
		defer proxyWg.Done()
		defer pcancel()
		b := getBuf()
		defer putBuf(b)
		for {
			select {
			case <-pctx.Done():
				return
			default:
			}
			clientConn.SetReadDeadline(time.Now().Add(30 * time.Minute))
			nn, err := clientConn.Read(*b)
			if err != nil {
				return
			}
			// Skip DTLS keepalive packets (1-byte 0xFF ping from client)
			if nn == 1 && (*b)[0] == 0xFF {
				continue
			}
			atomic.AddInt64(&totalBytesFromClient, int64(nn))
			// Учёт трафика теперь происходит через IpcGet()

			if _, err := wgConn.Write((*b)[:nn]); err != nil {
				return
			}
		}
	}()

	// WG → Клиент
	go func() {
		defer proxyWg.Done()
		defer pcancel()
		b := getBuf()
		defer putBuf(b)
		for {
			select {
			case <-pctx.Done():
				return
			default:
			}
			wgConn.SetReadDeadline(time.Now().Add(30 * time.Minute))
			nn, err := wgConn.Read(*b)
			if err != nil {
				if isNetTimeout(err) {
					if pctx.Err() != nil {
						return
					}
					continue
				}
				return
			}
			atomic.AddInt64(&totalBytesToClient, int64(nn))
			// Учёт трафика теперь происходит через IpcGet()

			if _, err := clientConn.Write((*b)[:nn]); err != nil {
				return
			}
		}
	}()

	proxyWg.Wait()
}

const (
	wrapNonceLen = 12
	wrapKeyLen   = 32
)

// ==================== RTP Обфускация ====================

type ObfsConfig struct {
	SSRC        uint32
	PayloadType uint8
	PaddingMax  int
}
var aeadCache sync.Map

func getAEAD(key []byte) (cipher.AEAD, error) {
	if len(key) != wrapKeyLen {
		return nil, fmt.Errorf("obfs: key must be %d bytes", wrapKeyLen)
	}
	keyStr := string(key)
	if val, ok := aeadCache.Load(keyStr); ok {
		return val.(cipher.AEAD), nil
	}
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, err
	}
	aeadCache.Store(keyStr, aead)
	return aead, nil
}

type ObfsState struct {
	mu         sync.Mutex
	seq        uint16
	timestamp  uint32
	lastPacket time.Time
	count      uint64
}

func NewObfsConfig(mode string) *ObfsConfig {
	var buf [4]byte
	rand.Read(buf[:])
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
	}
}

func NewObfsState() *ObfsState {
	var buf [6]byte
	rand.Read(buf[:])
	return &ObfsState{
		seq:       binary.BigEndian.Uint16(buf[0:2]),
		timestamp: binary.BigEndian.Uint32(buf[2:6]),
	}
}

func rtpTimestampStep(elapsed time.Duration, jitter byte) uint32 {
	samples := int64(elapsed) * 48000 / int64(time.Second)
	if samples < 120 {
		samples = 120
	} else if samples > 2880 {
		samples = 2880
	}
	samples = ((samples + 60) / 120) * 120
	samples += int64(int(jitter)%3-1) * 120
	if samples < 120 {
		samples = 120
	} else if samples > 2880 {
		samples = 2880
	}
	return uint32(samples)
}

func rtpSequenceStep(selector byte) uint16 {
	if selector&0x7F == 0 {
		return 2 + uint16(selector>>7)
	}
	return 1
}

func (s *ObfsState) nextHeader(now time.Time, jitter, sequenceSelector byte) (uint16, uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	seq := s.seq
	if s.count > 0 {
		s.timestamp += rtpTimestampStep(now.Sub(s.lastPacket), jitter)
	}
	timestamp := s.timestamp
	s.seq += rtpSequenceStep(sequenceSelector)
	s.lastPacket = now
	s.count++
	return seq, timestamp
}

func useRTPPadding(selector byte) bool {
	return selector&0x03 == 0
}

func useRTPMarker(selector byte) bool {
	return selector&0x3F == 0
}

func obfsBuildNonce(ssrc uint32, seq uint16, ts uint32) []byte {
	n := make([]byte, 12)
	binary.BigEndian.PutUint32(n[0:4], ssrc)
	binary.BigEndian.PutUint16(n[4:6], seq)
	binary.BigEndian.PutUint32(n[8:12], ts)
	return n
}

func obfsWrapPacket(key, payload []byte, cfg *ObfsConfig, state *ObfsState) ([]byte, error) {
	if len(key) != wrapKeyLen {
		return nil, fmt.Errorf("obfs: key must be %d bytes (got %d)", wrapKeyLen, len(key))
	}
	if len(payload) == 0 {
		return nil, errors.New("obfs: empty payload")
	}

	var randomBytes [5]byte
	if _, err := rand.Read(randomBytes[:]); err != nil {
		return nil, fmt.Errorf("obfs: random RTP metadata: %w", err)
	}
	seq, ts := state.nextHeader(time.Now(), randomBytes[2], randomBytes[4])

	nonce := obfsBuildNonce(cfg.SSRC, seq, ts)
	padTotal := 0
	padRand := 0
	if cfg.PaddingMax > 0 && useRTPPadding(randomBytes[0]) {
		padRand = int(randomBytes[1]) % cfg.PaddingMax
		padTotal = padRand + 1
	}
	outLen := 12 + len(payload) + chacha20poly1305.Overhead + padTotal
	out := make([]byte, outLen)

	out[0] = 0x80
	if padTotal > 0 {
		out[0] |= 0x20
	}
	out[1] = cfg.PayloadType & 0x7F
	if useRTPMarker(randomBytes[3]) {
		out[1] |= 0x80
	}
	binary.BigEndian.PutUint16(out[2:4], seq)
	binary.BigEndian.PutUint32(out[4:8], ts)
	binary.BigEndian.PutUint32(out[8:12], cfg.SSRC)

	aead, err := getAEAD(key)
	if err != nil {
		return nil, fmt.Errorf("obfs: cipher init: %w", err)
	}
	sealed := aead.Seal(out[12:12], nonce, payload, out[:12])
	padStart := 12 + len(sealed)
	if padRand > 0 {
		if _, err := rand.Read(out[padStart : padStart+padRand]); err != nil {
			return nil, fmt.Errorf("obfs: random padding bytes: %w", err)
		}
	}
	if padTotal > 0 {
		out[outLen-1] = byte(padTotal)
	}
	return out, nil
}

func obfsUnwrapPacket(key, wire, dst []byte) (int, error) {
	if len(key) != wrapKeyLen {
		return 0, fmt.Errorf("obfs: key must be %d bytes (got %d)", wrapKeyLen, len(key))
	}
	if len(wire) < 13 {
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
		if padLen == 0 || padLen > payloadEnd-12 {
			return 0, fmt.Errorf("obfs: invalid padding length %d", padLen)
		}
		payloadEnd -= padLen
	}
	ciphertextLen := payloadEnd - 12
	if ciphertextLen <= chacha20poly1305.Overhead {
		return 0, errors.New("obfs: no payload")
	}
	if ciphertextLen-chacha20poly1305.Overhead > len(dst) {
		return 0, errors.New("obfs: dst buffer too small")
	}
	nonce := obfsBuildNonce(ssrc, seq, ts)
	aead, err := getAEAD(key)
	if err != nil {
		return 0, fmt.Errorf("obfs: cipher init: %w", err)
	}
	plain, err := aead.Open(dst[:0], nonce, wire[12:payloadEnd], wire[:12])
	if err != nil {
		return 0, fmt.Errorf("obfs: auth: %w", err)
	}
	return len(plain), nil
}
func obfsIsRTPPacket(wire []byte) bool {
	if len(wire) < 13 {
		return false
	}
	if (wire[0] >> 6) != 2 {
		return false
	}
	pt := wire[1] & 0x7F
	return pt == 111 || pt == 96
}

func listenWrapped(addr *net.UDPAddr, keys *wrapKeyStore) (dtlsnet.PacketListener, error) {
	if keys == nil || keys.Count() == 0 {
		return nil, errors.New("wrap: no active keys")
	}
	inner, err := pionudp.Listen("udp", addr)
	if err != nil {
		return nil, fmt.Errorf("wrap: udp listen: %w", err)
	}
	return &wrapPacketListener{
		inner: dtlsnet.PacketListenerFromListener(inner),
		keys:  keys,
	}, nil
}

type wrapPacketListener struct {
	inner dtlsnet.PacketListener
	keys  *wrapKeyStore
}

func (l *wrapPacketListener) Accept() (net.PacketConn, net.Addr, error) {
	pc, addr, err := l.inner.Accept()
	if err != nil {
		return pc, addr, err
	}
	return &wrapPacketConn{inner: pc, keys: l.keys}, addr, nil
}

func (l *wrapPacketListener) Close() error   { return l.inner.Close() }
func (l *wrapPacketListener) Addr() net.Addr { return l.inner.Addr() }

type wrapPacketConn struct {
	inner     net.PacketConn
	keys      *wrapKeyStore
	key       []byte
	selected  int32
	authLog   int32
	obfsCfg   *ObfsConfig
	obfsWrite *ObfsState
}

func (c *wrapPacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	// Extra space for RTP header (12) + AEAD tag (16) + padding.
	buf := make([]byte, len(p)+80)
	n, addr, err := c.inner.ReadFrom(buf)
	if err != nil {
		return 0, addr, err
	}
	raw := buf[:n]

	if atomic.LoadInt32(&c.selected) == 0 {
		key, m, uErr := c.keys.Unwrap(raw, p)
		if uErr != nil {
			if atomic.CompareAndSwapInt32(&c.authLog, 0, 1) {
				log.Printf("[WRAP] Отказ: RTP AEAD auth failed from %s (keys=%d)", addr.String(), c.keys.Count())
			}
			return 0, addr, uErr
		}
		c.key = append([]byte(nil), key...) // Клонируем ключ в независимую память!
		c.obfsCfg = NewObfsConfig("audio")
		if len(raw) > 1 {
			c.obfsCfg.PayloadType = raw[1] & 0x7F
			if c.obfsCfg.PayloadType == 96 {
				c.obfsCfg.PaddingMax = 60
			}
		}
		c.obfsWrite = NewObfsState()
		atomic.StoreInt32(&c.selected, 1)
		if atomic.CompareAndSwapInt32(&c.authLog, 0, 1) {
			log.Printf("[WRAP] OK: ключ выбран для %s (keys=%d)", addr.String(), c.keys.Count())
		}
		return m, addr, nil
	}

	m, uErr := obfsUnwrapPacket(c.key, raw, p)
	if uErr != nil {
		// Если расшифровка старым ключом провалилась — возможно, пароль обновился!
		// Пробуем пере-верифицировать пакет по всем активным ключам
		key, m2, uErr2 := c.keys.Unwrap(raw, p)
		if uErr2 == nil {
			c.key = append([]byte(nil), key...) // На лету обновляем ключ сессии!
			c.obfsCfg = NewObfsConfig("audio")
			if len(raw) > 1 {
				c.obfsCfg.PayloadType = raw[1] & 0x7F
				if c.obfsCfg.PayloadType == 96 {
					c.obfsCfg.PaddingMax = 60
				}
			}
			c.obfsWrite = NewObfsState()
			log.Printf("[WRAP] Обновлен ключ на лету для %s (пароль изменился/обновился)", addr.String())
			return m2, addr, nil
		}
		return 0, addr, fmt.Errorf("obfs unwrap: %w", uErr)
	}
	return m, addr, nil
}

func (c *wrapPacketConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	if atomic.LoadInt32(&c.selected) == 0 || len(c.key) != wrapKeyLen {
		return 0, errors.New("wrap: key not selected")
	}
	if c.obfsCfg == nil || c.obfsWrite == nil {
		c.obfsCfg = NewObfsConfig("audio")
		c.obfsWrite = NewObfsState()
	}
	wrapped, wErr := obfsWrapPacket(c.key, p, c.obfsCfg, c.obfsWrite)
	if wErr != nil {
		return 0, fmt.Errorf("obfs wrap: %w", wErr)
	}
	if _, err := c.inner.WriteTo(wrapped, addr); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *wrapPacketConn) Close() error                       { return c.inner.Close() }
func (c *wrapPacketConn) LocalAddr() net.Addr                { return c.inner.LocalAddr() }
func (c *wrapPacketConn) SetDeadline(t time.Time) error      { return c.inner.SetDeadline(t) }
func (c *wrapPacketConn) SetReadDeadline(t time.Time) error  { return c.inner.SetReadDeadline(t) }
func (c *wrapPacketConn) SetWriteDeadline(t time.Time) error { return c.inner.SetWriteDeadline(t) }
