package ratelimit

import (
	"testing"
	"time"
)

func TestAllowsUpToMaxThenRefuses(t *testing.T) {
	l := New(3, time.Minute)

	for i := 0; i < 3; i++ {
		if !l.Allow("rider") {
			t.Fatalf("attempt %d: Allow = false, want true", i+1)
		}
	}
	if l.Allow("rider") {
		t.Fatal("4th attempt: Allow = true, want false")
	}
}

func TestKeysAreIndependent(t *testing.T) {
	l := New(1, time.Minute)

	if !l.Allow("wilant") {
		t.Fatal("first attempt for wilant: Allow = false, want true")
	}
	if !l.Allow("other") {
		t.Fatal("first attempt for other: Allow = false, want true (independent key)")
	}
	if l.Allow("wilant") {
		t.Fatal("second attempt for wilant: Allow = true, want false")
	}
}

func TestWindowResetsAfterItExpires(t *testing.T) {
	l := New(1, 10*time.Millisecond)

	if !l.Allow("rider") {
		t.Fatal("first attempt: Allow = false, want true")
	}
	if l.Allow("rider") {
		t.Fatal("second attempt within window: Allow = true, want false")
	}

	time.Sleep(15 * time.Millisecond)
	if !l.Allow("rider") {
		t.Fatal("attempt after window expired: Allow = false, want true")
	}
}

func TestEmptyKeyAlwaysAllowed(t *testing.T) {
	l := New(1, time.Minute)

	for i := 0; i < 5; i++ {
		if !l.Allow("") {
			t.Fatalf("attempt %d with empty key: Allow = false, want true", i+1)
		}
	}
}

func TestOldBucketsAreEvicted(t *testing.T) {
	l := New(1, 10*time.Millisecond)

	l.Allow("stale")
	time.Sleep(25 * time.Millisecond) // past 2x the window
	l.Allow("fresh")                  // triggers eviction

	l.mu.Lock()
	_, staleStillThere := l.buckets["stale"]
	l.mu.Unlock()
	if staleStillThere {
		t.Fatal("stale bucket survived eviction")
	}
}
