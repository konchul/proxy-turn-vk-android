package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

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

// rawTrafficCounter — накопитель трафика raw-режима для одного устройства.
type rawTrafficCounter struct {
	up   int64
	down int64
}

// rawClosedTraffic — остатки счётчиков сессий, закрытых между flush'ами
// (unregister переносит их сюда, чтобы трафик не потерялся). Запись только
// при закрытии сессии — редкое событие, не горячий путь.
var (
	rawClosedTrafficMu sync.Mutex
	rawClosedTraffic   = make(map[string]*rawTrafficCounter)
)

func addRawClosedBytes(deviceID string, up, down int64) {
	if deviceID == "" || deviceID == "unknown" {
		return
	}
	rawClosedTrafficMu.Lock()
	c := rawClosedTraffic[deviceID]
	if c == nil {
		c = &rawTrafficCounter{}
		rawClosedTraffic[deviceID] = c
	}
	c.up += up
	c.down += down
	rawClosedTrafficMu.Unlock()
}

// flushRawDeviceTraffic переносит накопленные с прошлого вызова байты в
// db.Devices/db.Passwords. Счётчики лежат атомиками в самих downlinkWorker'ах
// (uplink инкрементирует handleConnRaw, downlink — writer-горутина воркера),
// поэтому горячий путь не берёт ни глобальных локов, ни map-поисков; сюда
// попадают только дельты раз в 10 секунд (statsLoop) и при завершении.
func flushRawDeviceTraffic() {
	router := globalRawRouter.Load()
	deltas := make(map[string]*rawTrafficCounter)
	if router != nil {
		router.mu.RLock()
		for _, cs := range router.sessions {
			for _, w := range cs.workers {
				up := w.upBytes.Swap(0)
				down := w.downBytes.Swap(0)
				if up == 0 && down == 0 {
					continue
				}
				c := deltas[w.deviceID]
				if c == nil {
					c = &rawTrafficCounter{}
					deltas[w.deviceID] = c
				}
				c.up += up
				c.down += down
			}
		}
		router.mu.RUnlock()
	}

	rawClosedTrafficMu.Lock()
	if len(rawClosedTraffic) > 0 {
		closed := rawClosedTraffic
		rawClosedTraffic = make(map[string]*rawTrafficCounter)
		for deviceID, c := range closed {
			d := deltas[deviceID]
			if d == nil {
				deltas[deviceID] = &rawTrafficCounter{up: c.up, down: c.down}
			} else {
				d.up += c.up
				d.down += c.down
			}
		}
	}
	rawClosedTrafficMu.Unlock()

	if len(deltas) == 0 {
		return
	}

	dbMutex.Lock()
	for deviceID, c := range deltas {
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
	dbMutex.Unlock()
}

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

		dev := db.Devices[targetDevID]
		entry := generatedOwnerEntryLocked(dev, targetDevID)
		if entry != nil {
			entry.UpBytes += deltaRx
			entry.DownBytes += deltaTx
		}

		if entry == nil {
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
			flushRawDeviceTraffic()
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
