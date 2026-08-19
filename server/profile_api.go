package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

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

var profileChallenges = struct {
	sync.Mutex
	items map[string]time.Time
}{items: make(map[string]time.Time)}

func handleAPIProfileChallenge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		http.Error(w, `{"error":"Internal error"}`, http.StatusInternalServerError)
		return
	}
	nonce := base64.RawURLEncoding.EncodeToString(b)
	now := time.Now()
	profileChallenges.Lock()
	for value, expires := range profileChallenges.items {
		if !expires.After(now) {
			delete(profileChallenges.items, value)
		}
	}
	if len(profileChallenges.items) >= 8192 {
		profileChallenges.Unlock()
		http.Error(w, `{"error":"Too many requests"}`, http.StatusTooManyRequests)
		return
	}
	profileChallenges.items[nonce] = now.Add(time.Minute)
	profileChallenges.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"nonce": nonce})
}

func profileKeyID(password string) string {
	sum := sha256.Sum256([]byte("WDTT-PROFILE-ID-v1\x00" + password))
	return hex.EncodeToString(sum[:])
}

func authenticateProfileRequest(r *http.Request, action string) (string, string, bool) {
	deviceID := r.FormValue("device_id")
	nonce := r.FormValue("nonce")
	keyID := r.FormValue("key_id")
	proof, err := hex.DecodeString(r.FormValue("proof"))
	if deviceID == "" || nonce == "" || keyID == "" || err != nil || len(proof) != sha256.Size {
		return "", "", false
	}
	now := time.Now()
	profileChallenges.Lock()
	expires, exists := profileChallenges.items[nonce]
	delete(profileChallenges.items, nonce)
	profileChallenges.Unlock()
	if !exists || !expires.After(now) {
		return "", "", false
	}
	dbMutex.Lock()
	defer dbMutex.Unlock()
	for password, entry := range db.Passwords {
		if profileKeyID(password) != keyID || isPasswordExpired(entry) || entry.IsDeactivated {
			continue
		}
		mac := hmac.New(sha256.New, []byte(password))
		mac.Write([]byte(action + "\n" + deviceID + "\n" + nonce))
		if hmac.Equal(proof, mac.Sum(nil)) {
			return password, deviceID, true
		}
		return "", "", false
	}
	return "", "", false
}

func handleAPIProfileStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	password, deviceID, valid := authenticateProfileRequest(r, "status")
	if !valid {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}
	dbMutex.Lock()
	defer dbMutex.Unlock()
	entry, exists := db.Passwords[password]
	if !exists || isPasswordExpired(entry) || entry.IsDeactivated {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
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
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	password, deviceID, valid := authenticateProfileRequest(r, "unbind")
	if !valid {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}
	dbMutex.Lock()
	defer dbMutex.Unlock()
	entry, exists := db.Passwords[password]
	if !exists || isPasswordExpired(entry) || entry.IsDeactivated {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}
	unbindDevices(entry, deviceID)
	saveDB()

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"success":true}`))
}
