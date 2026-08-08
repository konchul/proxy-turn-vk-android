package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func setupAdminAPITest(t *testing.T) *http.ServeMux {
	t.Helper()
	dir := t.TempDir()
	initDB(dir, "deploy-secret", "", "")
	globalWgDev = nil
	mux := http.NewServeMux()
	registerAdminAPIRoutes(mux)
	return mux
}

func doAdminRequest(mux *http.ServeMux, method, path, adminPass string, form url.Values) *httptest.ResponseRecorder {
	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	} else {
		body = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if adminPass != "" {
		req.Header.Set("X-Admin-Password", adminPass)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestAdminAPIRejectsWrongOrMissingPassword(t *testing.T) {
	mux := setupAdminAPITest(t)

	rec := doAdminRequest(mux, http.MethodGet, "/admin/passwords", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no password: got status %d, want 401", rec.Code)
	}

	rec = doAdminRequest(mux, http.MethodGet, "/admin/passwords", "wrong-secret", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password: got status %d, want 401", rec.Code)
	}
}

func TestAdminAPICreateListDeactivateDelete(t *testing.T) {
	mux := setupAdminAPITest(t)
	const adminPass = "deploy-secret"

	// Create
	form := url.Values{"vk_hash": {"abc123"}, "days": {"7"}, "max_devices": {"2"}}
	rec := doAdminRequest(mux, http.MethodPost, "/admin/passwords", adminPass, form)
	if rec.Code != http.StatusOK {
		t.Fatalf("create: got status %d, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"vk_hash":"abc123"`) {
		t.Fatalf("create: response missing vk_hash: %s", rec.Body.String())
	}

	// List should now contain exactly one password
	rec = doAdminRequest(mux, http.MethodGet, "/admin/passwords", adminPass, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: got status %d, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"vk_hash":"abc123"`) {
		t.Fatalf("list: missing created password: %s", rec.Body.String())
	}

	var newPass string
	for pass := range db.Passwords {
		newPass = pass
	}
	if newPass == "" {
		t.Fatal("no password found in db after create")
	}

	// Deactivate
	rec = doAdminRequest(mux, http.MethodPost, "/admin/passwords/deactivate", adminPass, url.Values{"password": {newPass}})
	if rec.Code != http.StatusOK {
		t.Fatalf("deactivate: got status %d, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"is_deactivated":true`) {
		t.Fatalf("deactivate: expected is_deactivated=true: %s", rec.Body.String())
	}

	// Reactivate
	rec = doAdminRequest(mux, http.MethodPost, "/admin/passwords/activate", adminPass, url.Values{"password": {newPass}})
	if rec.Code != http.StatusOK {
		t.Fatalf("activate: got status %d, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"is_deactivated":false`) {
		t.Fatalf("activate: expected is_deactivated=false: %s", rec.Body.String())
	}

	// Delete
	rec = doAdminRequest(mux, http.MethodPost, "/admin/passwords/delete", adminPass, url.Values{"password": {newPass}})
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: got status %d, body=%s", rec.Code, rec.Body.String())
	}
	if _, exists := db.Passwords[newPass]; exists {
		t.Fatal("password still present in db after delete")
	}
}

func TestAdminAPICreateRequiresVkHash(t *testing.T) {
	mux := setupAdminAPITest(t)
	rec := doAdminRequest(mux, http.MethodPost, "/admin/passwords", "deploy-secret", url.Values{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got status %d, want 400", rec.Code)
	}
}

func TestAdminAPIDeactivateUnknownPasswordReturns404(t *testing.T) {
	mux := setupAdminAPITest(t)
	rec := doAdminRequest(mux, http.MethodPost, "/admin/passwords/deactivate", "deploy-secret", url.Values{"password": {"does-not-exist"}})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("got status %d, want 404", rec.Code)
	}
}

func TestAdminAPICreateWithLabel(t *testing.T) {
	mux := setupAdminAPITest(t)
	const adminPass = "deploy-secret"

	form := url.Values{"vk_hash": {"abc123"}, "label": {"Иван Петров"}}
	rec := doAdminRequest(mux, http.MethodPost, "/admin/passwords", adminPass, form)
	if rec.Code != http.StatusOK {
		t.Fatalf("create: got status %d, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"label":"Иван Петров"`) {
		t.Fatalf("create: expected custom label in response: %s", rec.Body.String())
	}
}

func TestAdminAPIUpdatePartialFields(t *testing.T) {
	mux := setupAdminAPITest(t)
	const adminPass = "deploy-secret"

	rec := doAdminRequest(mux, http.MethodPost, "/admin/passwords", adminPass, url.Values{"vk_hash": {"abc123"}, "max_devices": {"1"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("create: got status %d, body=%s", rec.Code, rec.Body.String())
	}
	var newPass string
	for pass := range db.Passwords {
		newPass = pass
	}
	if newPass == "" {
		t.Fatal("no password found in db after create")
	}

	// Update only the label — vk_hash and max_devices must stay unchanged.
	rec = doAdminRequest(mux, http.MethodPost, "/admin/passwords/update", adminPass, url.Values{"password": {newPass}, "label": {"Мария Иванова"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("update label: got status %d, body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"label":"Мария Иванова"`) {
		t.Fatalf("update label: expected new label: %s", body)
	}
	if !strings.Contains(body, `"vk_hash":"abc123"`) {
		t.Fatalf("update label: vk_hash should be unchanged: %s", body)
	}
	if !strings.Contains(body, `"max_devices":1`) {
		t.Fatalf("update label: max_devices should be unchanged: %s", body)
	}

	// Now update vk_hash and max_devices — label must stay as set above.
	rec = doAdminRequest(mux, http.MethodPost, "/admin/passwords/update", adminPass, url.Values{"password": {newPass}, "vk_hash": {"newhash"}, "max_devices": {"5"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("update hash: got status %d, body=%s", rec.Code, rec.Body.String())
	}
	body = rec.Body.String()
	if !strings.Contains(body, `"vk_hash":"newhash"`) {
		t.Fatalf("update hash: expected new vk_hash: %s", body)
	}
	if !strings.Contains(body, `"max_devices":5`) {
		t.Fatalf("update hash: expected new max_devices: %s", body)
	}
	if !strings.Contains(body, `"label":"Мария Иванова"`) {
		t.Fatalf("update hash: label should still be unchanged: %s", body)
	}
}

func TestAdminAPIUpdateUnknownPasswordReturns404(t *testing.T) {
	mux := setupAdminAPITest(t)
	rec := doAdminRequest(mux, http.MethodPost, "/admin/passwords/update", "deploy-secret", url.Values{"password": {"does-not-exist"}, "label": {"x"}})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("got status %d, want 404", rec.Code)
	}
}

func TestAdminAPIUnbindDeviceRemovesFromList(t *testing.T) {
	mux := setupAdminAPITest(t)
	const adminPass = "deploy-secret"

	rec := doAdminRequest(mux, http.MethodPost, "/admin/passwords", adminPass, url.Values{"vk_hash": {"abc123"}, "max_devices": {"2"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("create: got status %d, body=%s", rec.Code, rec.Body.String())
	}
	var newPass string
	for pass := range db.Passwords {
		newPass = pass
	}
	if newPass == "" {
		t.Fatal("no password found in db after create")
	}

	// Simulate two devices already bound to this password, as would happen
	// after real client connections.
	dbMutex.Lock()
	entry := db.Passwords[newPass]
	entry.DeviceIDs = []string{"device-a", "device-b"}
	entry.DeviceID = "multi"
	db.Devices["device-a"] = &ClientDevice{DeviceID: "device-a", IP: "10.66.0.2", PubKey: "AAAA"}
	db.Devices["device-b"] = &ClientDevice{DeviceID: "device-b", IP: "10.66.0.3", PubKey: "BBBB"}
	dbMutex.Unlock()

	rec = doAdminRequest(mux, http.MethodPost, "/admin/passwords/unbind-device", adminPass, url.Values{"password": {newPass}, "device_id": {"device-a"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("unbind: got status %d, body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "device-a") {
		t.Fatalf("unbind: device-a should be removed from device_ids: %s", body)
	}
	if !strings.Contains(body, "device-b") {
		t.Fatalf("unbind: device-b should still be bound: %s", body)
	}

	dbMutex.Lock()
	if _, exists := db.Devices["device-a"]; exists {
		dbMutex.Unlock()
		t.Fatal("device-a should be removed from db.Devices after unbind")
	}
	if _, exists := db.Devices["device-b"]; !exists {
		dbMutex.Unlock()
		t.Fatal("device-b should remain in db.Devices")
	}
	if len(entry.DeviceIDs) != 1 || entry.DeviceIDs[0] != "device-b" {
		dbMutex.Unlock()
		t.Fatalf("entry.DeviceIDs should contain only device-b, got %v", entry.DeviceIDs)
	}
	dbMutex.Unlock()
}

func TestAdminAPIUnbindDeviceUnknownPasswordReturns404(t *testing.T) {
	mux := setupAdminAPITest(t)
	rec := doAdminRequest(mux, http.MethodPost, "/admin/passwords/unbind-device", "deploy-secret", url.Values{"password": {"does-not-exist"}, "device_id": {"device-a"}})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("got status %d, want 404", rec.Code)
	}
}

func TestAdminAPIUnbindDeviceRequiresDeviceID(t *testing.T) {
	mux := setupAdminAPITest(t)
	const adminPass = "deploy-secret"

	rec := doAdminRequest(mux, http.MethodPost, "/admin/passwords", adminPass, url.Values{"vk_hash": {"abc123"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("create: got status %d, body=%s", rec.Code, rec.Body.String())
	}
	var newPass string
	for pass := range db.Passwords {
		newPass = pass
	}

	rec = doAdminRequest(mux, http.MethodPost, "/admin/passwords/unbind-device", adminPass, url.Values{"password": {newPass}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got status %d, want 400", rec.Code)
	}
}

// unbindDevices is a no-op (not an error) when the given device_id isn't
// actually bound to the password — it just filters entry.DeviceIDs, which
// stays unchanged. Verify the endpoint reflects that: 200 OK, list untouched.
func TestAdminAPIUnbindDeviceNotBoundIsNoop(t *testing.T) {
	mux := setupAdminAPITest(t)
	const adminPass = "deploy-secret"

	rec := doAdminRequest(mux, http.MethodPost, "/admin/passwords", adminPass, url.Values{"vk_hash": {"abc123"}, "max_devices": {"2"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("create: got status %d, body=%s", rec.Code, rec.Body.String())
	}
	var newPass string
	for pass := range db.Passwords {
		newPass = pass
	}

	dbMutex.Lock()
	entry := db.Passwords[newPass]
	entry.DeviceIDs = []string{"device-a"}
	db.Devices["device-a"] = &ClientDevice{DeviceID: "device-a", IP: "10.66.0.2", PubKey: "AAAA"}
	dbMutex.Unlock()

	rec = doAdminRequest(mux, http.MethodPost, "/admin/passwords/unbind-device", adminPass, url.Values{"password": {newPass}, "device_id": {"not-bound-device"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("unbind unbound device: got status %d, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "device-a") {
		t.Fatalf("unbind unbound device: device-a should remain bound: %s", rec.Body.String())
	}
}

func TestAdminAPIUpdateRejectsEmptyVkHash(t *testing.T) {
	mux := setupAdminAPITest(t)
	const adminPass = "deploy-secret"

	rec := doAdminRequest(mux, http.MethodPost, "/admin/passwords", adminPass, url.Values{"vk_hash": {"abc123"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("create: got status %d, body=%s", rec.Code, rec.Body.String())
	}
	var newPass string
	for pass := range db.Passwords {
		newPass = pass
	}

	rec = doAdminRequest(mux, http.MethodPost, "/admin/passwords/update", adminPass, url.Values{"password": {newPass}, "vk_hash": {""}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got status %d, want 400", rec.Code)
	}
}
