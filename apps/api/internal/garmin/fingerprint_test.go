package garmin

import (
	"strings"
	"testing"
)

// The body echoes the posted form, password included, so the fingerprint has
// to describe the page without quoting any of it.
func TestFingerprintNeverCarriesAValue(t *testing.T) {
	body := []byte(`<html><head><title>  Sign   In
	</title></head><body>
	<form><input type="text" name="username" value="rider@example.com"/>
	<input type="password" name="password" value="hunter2"/>
	<input type="hidden" name="_csrf" value="super-secret-token"/>
	</form></body></html>`)

	got := fingerprint(body)
	for _, secret := range []string{"hunter2", "super-secret-token", "rider@example.com"} {
		if strings.Contains(got, secret) {
			t.Fatalf("fingerprint leaked %q: %s", secret, got)
		}
	}
	for _, want := range []string{`title="Sign In"`, "bytes=", "username", "password", "_csrf"} {
		if !strings.Contains(got, want) {
			t.Errorf("fingerprint = %q, want it to mention %s", got, want)
		}
	}
}

// A title long enough to be a paragraph is a page that wants to explain
// something; the log does not need all of it.
func TestFingerprintTruncatesALongTitle(t *testing.T) {
	body := []byte("<title>" + strings.Repeat("x", 500) + "</title>")
	got := fingerprint(body)
	if len(got) > 200 {
		t.Errorf("fingerprint is %d chars: %s", len(got), got)
	}
	if !strings.Contains(got, "…") {
		t.Errorf("a long title was not truncated: %s", got)
	}
}

// No title, no form, no panic: an empty or binary body still describes itself.
func TestFingerprintHandlesNothing(t *testing.T) {
	if got := fingerprint(nil); !strings.Contains(got, "bytes=0") {
		t.Errorf("fingerprint(nil) = %q", got)
	}
}
