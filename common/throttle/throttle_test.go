package throttle

import (
	"testing"
	"time"
)

func TestWait_DisabledIsFree(t *testing.T) {
	Set("test-disabled", 0)
	start := time.Now()
	Wait("test-disabled")
	Wait("test-disabled")
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("disabled throttle should not sleep, took %s", elapsed)
	}
}

func TestWait_EnforcesInterval(t *testing.T) {
	Set("test-interval", 120*time.Millisecond)
	Wait("test-interval")
	start := time.Now()
	Wait("test-interval")
	elapsed := time.Since(start)
	if elapsed < 100*time.Millisecond {
		t.Fatalf("expected >=100ms between requests, got %s", elapsed)
	}
}

func TestInterval_IsPerProvider(t *testing.T) {
	Set("provider-a", time.Second)
	Set("provider-b", 0)
	if Interval("provider-a") != time.Second {
		t.Fatal("provider-a interval lost")
	}
	if Interval("provider-b") != 0 {
		t.Fatal("provider-b interval should be 0")
	}
}
