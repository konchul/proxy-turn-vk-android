package main

import (
	"sync"
	"testing"
)

// withTestDB подменяет глобальную db на пустую тестовую на время теста и
// восстанавливает исходную по завершении — db используется без указателя на
// мьютекс синхронизации доступа к самой переменной (только dbMutex защищает
// её содержимое), но так как go test в этом пакете уже не запускает тесты
// параллельно с изменением db, подмена безопасна.
func withTestDB(t *testing.T) {
	t.Helper()
	old := db
	db = &Database{
		Passwords: make(map[string]*PasswordEntry),
		Devices:   make(map[string]*ClientDevice),
	}
	t.Cleanup(func() { db = old })
}

// resetRawTrafficState очищает глобальный in-memory буфер между тестами —
// иначе накопленное в одном тесте просочится в следующий.
func resetRawTrafficState(t *testing.T) {
	t.Helper()
	rawDeviceTrafficMu.Lock()
	rawDeviceTraffic = make(map[string]*rawTrafficCounter)
	rawDeviceTrafficMu.Unlock()
}

// TestAddRawTrafficFlushesToDevice проверяет весь путь: горячий путь
// (addRawUplinkBytes/addRawDownlinkBytes, как вызывается из handleConnRaw и
// downlinkWorker.run) накапливает байты в памяти, а flushRawDeviceTrafficLocked
// (как вызывается из statsLoop под dbMutex) переносит их в db.Devices —
// именно то звено, которого раньше не было для raw-режима вообще.
func TestAddRawTrafficFlushesToDevice(t *testing.T) {
	withTestDB(t)
	resetRawTrafficState(t)

	db.Devices["dev1"] = &ClientDevice{DeviceID: "dev1"}

	addRawUplinkBytes("dev1", 1000)
	addRawUplinkBytes("dev1", 500)
	addRawDownlinkBytes("dev1", 2000)

	dbMutex.Lock()
	flushRawDeviceTrafficLocked()
	dbMutex.Unlock()

	dev := db.Devices["dev1"]
	if dev.UpBytes != 1500 {
		t.Fatalf("dev.UpBytes = %d, want 1500", dev.UpBytes)
	}
	if dev.DownBytes != 2000 {
		t.Fatalf("dev.DownBytes = %d, want 2000", dev.DownBytes)
	}
}

// TestAddRawTrafficUpdatesPasswordEntryByDeviceIDs проверяет привязку через
// PasswordEntry.DeviceIDs (несколько устройств на одном сгенерированном
// пароле) — не только через устаревшее одиночное DeviceID.
func TestAddRawTrafficUpdatesPasswordEntryByDeviceIDs(t *testing.T) {
	withTestDB(t)
	resetRawTrafficState(t)

	db.Passwords["secret"] = &PasswordEntry{
		DeviceIDs: []string{"devA", "devB"},
	}

	addRawUplinkBytes("devB", 300)
	addRawDownlinkBytes("devB", 700)

	dbMutex.Lock()
	flushRawDeviceTrafficLocked()
	dbMutex.Unlock()

	entry := db.Passwords["secret"]
	if entry.UpBytes != 300 || entry.DownBytes != 700 {
		t.Fatalf("entry traffic = up=%d down=%d, want up=300 down=700", entry.UpBytes, entry.DownBytes)
	}
}

// TestAddRawTrafficLegacyDeviceIDField проверяет обратную совместимость с
// PasswordEntry.DeviceID (единственное устройство, старый формат БД) — см.
// комментарий "Для обратной совместимости" на структуре PasswordEntry.
func TestAddRawTrafficLegacyDeviceIDField(t *testing.T) {
	withTestDB(t)
	resetRawTrafficState(t)

	db.Passwords["legacy"] = &PasswordEntry{DeviceID: "devLegacy"}

	addRawUplinkBytes("devLegacy", 42)

	dbMutex.Lock()
	flushRawDeviceTrafficLocked()
	dbMutex.Unlock()

	if db.Passwords["legacy"].UpBytes != 42 {
		t.Fatalf("legacy entry UpBytes = %d, want 42", db.Passwords["legacy"].UpBytes)
	}
}

// TestAddRawTrafficIgnoresEmptyOrUnknownDeviceID: deviceID="" или "unknown"
// (дефолт при отсутствии/незавершённом парсинге GETCONF_RAW/AUTH в
// handleConnRaw) не должен создавать мусорные записи в rawDeviceTraffic —
// это была бы неограниченно растущая карта на каждый недоаутентифицированный
// коннект.
func TestAddRawTrafficIgnoresEmptyOrUnknownDeviceID(t *testing.T) {
	withTestDB(t)
	resetRawTrafficState(t)

	addRawUplinkBytes("", 100)
	addRawUplinkBytes("unknown", 100)
	addRawDownlinkBytes("", 100)
	addRawDownlinkBytes("unknown", 100)

	rawDeviceTrafficMu.Lock()
	n := len(rawDeviceTraffic)
	rawDeviceTrafficMu.Unlock()
	if n != 0 {
		t.Fatalf("rawDeviceTraffic содержит %d записей для пустого/unknown deviceID, ожидалось 0", n)
	}
}

// TestAddRawTrafficConcurrent гоняется с -race: горячий путь вызывается из
// многих горутин одновременно (handleConnRaw на каждого воркера + N
// downlinkWorker.run), как и в проде при 380+ активных клиентах.
func TestAddRawTrafficConcurrent(t *testing.T) {
	withTestDB(t)
	resetRawTrafficState(t)

	db.Devices["concurrent-dev"] = &ClientDevice{DeviceID: "concurrent-dev"}

	const goroutines = 50
	const perGoroutine = 200
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				addRawUplinkBytes("concurrent-dev", 1)
				addRawDownlinkBytes("concurrent-dev", 2)
			}
		}()
	}
	wg.Wait()

	dbMutex.Lock()
	flushRawDeviceTrafficLocked()
	dbMutex.Unlock()

	wantUp := int64(goroutines * perGoroutine)
	wantDown := int64(goroutines * perGoroutine * 2)
	dev := db.Devices["concurrent-dev"]
	if dev.UpBytes != wantUp {
		t.Fatalf("dev.UpBytes = %d, want %d", dev.UpBytes, wantUp)
	}
	if dev.DownBytes != wantDown {
		t.Fatalf("dev.DownBytes = %d, want %d", dev.DownBytes, wantDown)
	}
}

// TestFlushRawDeviceTrafficClearsInMemoryBuffer: после flush накопленное в
// памяти должно обнулиться — иначе повторный flush задвоил бы байты.
func TestFlushRawDeviceTrafficClearsInMemoryBuffer(t *testing.T) {
	withTestDB(t)
	resetRawTrafficState(t)

	db.Devices["dev1"] = &ClientDevice{DeviceID: "dev1"}
	addRawUplinkBytes("dev1", 100)

	dbMutex.Lock()
	flushRawDeviceTrafficLocked()
	flushRawDeviceTrafficLocked() // второй flush без нового трафика
	dbMutex.Unlock()

	if db.Devices["dev1"].UpBytes != 100 {
		t.Fatalf("dev.UpBytes = %d после двойного flush, want 100 (повторный flush не должен задваивать)", db.Devices["dev1"].UpBytes)
	}
}
