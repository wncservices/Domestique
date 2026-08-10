package api_test

import (
	"net/http"
	"testing"

	"github.com/wncservices/domestique/apps/api/internal/api"
	"github.com/wncservices/domestique/apps/api/internal/garmin"
)

// The point of the whole feature: an admin can make Garmin sign-in work
// without anybody editing an env file.
func TestAdminCanSetTheConsumerFromTheUI(t *testing.T) {
	h := newConnectHarness(t, true)
	noConsumer(t)

	before := decodeConnection(t, h.as("wilant", "admins", http.MethodGet, "/api/garmin/connection", ""))
	if before["canConnect"] != false {
		t.Fatalf("canConnect = %v before setting a consumer", before["canConnect"])
	}

	resp := h.as("wilant", "admins", http.MethodPut, "/api/garmin/consumer",
		`{"key":"pasted-key","secret":"pasted-secret"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := decodeConnection(t, resp)
	if body["configured"] != true || body["source"] != "settings" {
		t.Errorf("body = %v, want configured from settings", body)
	}

	// The sign-in form is now on offer...
	after := decodeConnection(t, h.as("wilant", "admins", http.MethodGet, "/api/garmin/connection", ""))
	if after["canConnect"] != true {
		t.Errorf("canConnect = %v after setting a consumer", after["canConnect"])
	}

	// ...and signing in uses the pair that was pasted, not an empty one.
	h.as("wilant", "admins", http.MethodPost, "/api/garmin/connection",
		`{"email":"r@example.com","password":"pw"}`)
	if h.garmin.consumer != (api.GarminConsumer{Key: "pasted-key", Secret: "pasted-secret"}) {
		t.Errorf("signed with %+v, want the pasted pair", h.garmin.consumer)
	}
}

// The value is a credential: it goes in and never comes back out.
func TestTheConsumerIsNeverReturned(t *testing.T) {
	h := newConnectHarness(t, true)
	h.as("wilant", "admins", http.MethodPut, "/api/garmin/consumer",
		`{"key":"secret-key-value","secret":"secret-secret-value"}`)

	for _, path := range []string{"/api/garmin/consumer", "/api/garmin/connection"} {
		resp := h.as("wilant", "admins", http.MethodGet, path, "")
		body := decodeConnection(t, resp)
		for name, value := range body {
			if text, ok := value.(string); ok &&
				(text == "secret-key-value" || text == "secret-secret-value") {
				t.Errorf("%s returned the consumer in %q", path, name)
			}
		}
	}

	// Nor is it in the database in clear.
	var rows int
	if err := h.db.Conn().QueryRow(
		`SELECT COUNT(*) FROM settings WHERE CAST(value AS TEXT) LIKE '%secret-key-value%'`).
		Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Error("the consumer is stored in clear")
	}
}

// What an admin sets in the UI is the more recent, more specific act, so it
// wins — and clearing it falls back to the environment rather than to nothing.
func TestStoredConsumerWinsAndClearingFallsBack(t *testing.T) {
	h := newConnectHarness(t, true) // the harness puts a pair in the environment

	body := decodeConnection(t, h.as("wilant", "admins", http.MethodGet, "/api/garmin/consumer", ""))
	if body["source"] != "environment" {
		t.Fatalf("source = %v, want environment to start with", body["source"])
	}

	h.as("wilant", "admins", http.MethodPut, "/api/garmin/consumer",
		`{"key":"pasted-key","secret":"pasted-secret"}`)
	h.as("wilant", "admins", http.MethodPost, "/api/garmin/connection",
		`{"email":"r@example.com","password":"pw"}`)
	if h.garmin.consumer.Key != "pasted-key" {
		t.Errorf("signed with %q, want the pasted key to win over the environment", h.garmin.consumer.Key)
	}

	resp := h.as("wilant", "admins", http.MethodDelete, "/api/garmin/consumer", "")
	body = decodeConnection(t, resp)
	if body["configured"] != true || body["source"] != "environment" {
		t.Errorf("after clearing, body = %v, want the environment pair back", body)
	}

	h.as("wilant", "admins", http.MethodPost, "/api/garmin/connection",
		`{"email":"r@example.com","password":"pw"}`)
	if h.garmin.consumer.Key != "test-consumer-key" {
		t.Errorf("signed with %q, want the environment key after clearing", h.garmin.consumer.Key)
	}
}

// A deployment-wide credential is not a rider's to change.
func TestOnlyAnAdminManagesTheConsumer(t *testing.T) {
	h := newConnectHarness(t, true)

	for _, tc := range []struct{ method, body string }{
		{http.MethodGet, ""},
		{http.MethodPut, `{"key":"k","secret":"s"}`},
		{http.MethodDelete, ""},
	} {
		resp := h.as("rider", "cyclists", tc.method, "/api/garmin/consumer", tc.body)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s as a rider = %d, want 403", tc.method, resp.StatusCode)
		}
	}

	// A rider is not told the consumer exists as a concept. They cannot set
	// it, cannot act on knowing it is missing, and "Garmin app keys" is
	// deployment plumbing — they get a plain "not set up yet" instead.
	noConsumer(t)
	body := decodeConnection(t, h.as("rider", "cyclists", http.MethodGet, "/api/garmin/connection", ""))
	if _, present := body["consumer"]; present {
		t.Errorf("a rider was shown the deployment's consumer: %v", body)
	}
	if body["unavailable"] == nil {
		t.Error("a rider was told nothing about why there is no sign-in")
	}
}

// Half a pair signs nothing, so it is refused rather than stored.
func TestConsumerNeedsBothHalves(t *testing.T) {
	h := newConnectHarness(t, true)

	for _, body := range []string{`{"key":"k"}`, `{"secret":"s"}`, `{"key":"","secret":""}`} {
		resp := h.as("wilant", "admins", http.MethodPut, "/api/garmin/consumer", body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", body, resp.StatusCode)
		}
	}

	if _, err := h.settings.Describe(api.SettingGarminConsumerKey); err == nil {
		t.Error("half a pair was stored")
	}
}

// Without an encryption key there is nowhere to keep it, so the form is not
// offered and the endpoint refuses rather than storing it in clear.
func TestConsumerNeedsAnEncryptionKey(t *testing.T) {
	h := newConnectHarness(t, false)
	noConsumer(t)

	body := decodeConnection(t, h.as("wilant", "admins", http.MethodGet, "/api/garmin/consumer", ""))
	if body["canManage"] != false {
		t.Errorf("canManage = %v without an encryption key", body["canManage"])
	}
	if body["unavailable"] == nil {
		t.Error("nothing said about why it cannot be managed")
	}

	resp := h.as("wilant", "admins", http.MethodPut, "/api/garmin/consumer",
		`{"key":"k","secret":"s"}`)
	if resp.StatusCode != http.StatusPreconditionFailed {
		t.Errorf("status = %d, want 412", resp.StatusCode)
	}
}

// The environment still works on its own, for a deployment that would rather
// keep this in Vault than in the database.
func TestEnvironmentConsumerAloneIsEnough(t *testing.T) {
	h := newConnectHarness(t, true)

	body := decodeConnection(t, h.as("wilant", "cyclists", http.MethodGet, "/api/garmin/connection", ""))
	if body["canConnect"] != true {
		t.Errorf("canConnect = %v with a consumer in the environment", body["canConnect"])
	}

	h.as("wilant", "cyclists", http.MethodPost, "/api/garmin/connection",
		`{"email":"r@example.com","password":"pw"}`)
	if h.garmin.consumer.Secret != "test-consumer-secret" {
		t.Errorf("signed with %+v, want the environment pair", h.garmin.consumer)
	}
}

// Sanity: the environment reader is what the harness and main.go both rely on.
func TestConsumerFromEnvNeedsBothHalves(t *testing.T) {
	t.Setenv(garmin.EnvConsumerKey, "k")
	t.Setenv(garmin.EnvConsumerSecret, "")
	if _, _, ok := garmin.ConsumerFromEnv(); ok {
		t.Error("half a pair in the environment counted as configured")
	}
}

// A pair that turns out to be wrong has to be replaceable from the UI, so an
// admin keeps the panel after setting one. Without this, the first save is a
// one-way door and the fix is an API call or a file edit.
func TestAdminKeepsTheConsumerPanelOnceConfigured(t *testing.T) {
	h := newConnectHarness(t, true)
	h.as("wilant", "admins", http.MethodPut, "/api/garmin/consumer",
		`{"key":"pasted-key","secret":"pasted-secret"}`)

	body := decodeConnection(t, h.as("wilant", "admins", http.MethodGet, "/api/garmin/connection", ""))
	if body["canConnect"] != true {
		t.Fatalf("canConnect = %v after setting a consumer", body["canConnect"])
	}
	consumer, ok := body["consumer"].(map[string]any)
	if !ok {
		t.Fatal("an admin lost the way to replace the consumer once it was set")
	}
	if consumer["canManage"] != true || consumer["source"] != "settings" {
		t.Errorf("consumer = %v, want manageable and from settings", consumer)
	}

	// A rider does not get it: it is not theirs to change, and offering it
	// would only produce a 403.
	riderBody := decodeConnection(t, h.as("rider", "cyclists", http.MethodGet, "/api/garmin/connection", ""))
	if _, present := riderBody["consumer"]; present {
		t.Error("a rider was shown the deployment's consumer panel")
	}
}
