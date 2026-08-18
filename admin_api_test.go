package main

import (
	"net/http/httptest"
	"testing"
)

func TestAdminAuthorizationUsesBearerToken(t *testing.T) {
	adminAuthMu.Lock()
	adminAuthAttempts = map[string]adminAuthAttempt{}
	adminAuthMu.Unlock()
	setAdminAPIToken("test-admin-token")

	legacy := httptest.NewRequest("GET", "/admin/passwords", nil)
	legacy.Header.Set("X-Admin-Password", "test-admin-token")
	if adminAuthorized(legacy) {
		t.Fatal("legacy header must not authorize")
	}

	wrong := httptest.NewRequest("GET", "/admin/passwords", nil)
	wrong.Header.Set("Authorization", "Bearer wrong-token")
	if adminAuthorized(wrong) {
		t.Fatal("wrong bearer token must not authorize")
	}

	valid := httptest.NewRequest("GET", "/admin/passwords", nil)
	valid.Header.Set("Authorization", "Bearer test-admin-token")
	if !adminAuthorized(valid) {
		t.Fatal("valid bearer token must authorize")
	}
}
