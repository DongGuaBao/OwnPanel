package panel

import (
	"strconv"
	"testing"
	"time"
)

func TestLoginLimiterBoundsAndExpiresSources(t *testing.T) {
	limiter := loginLimiter{entries: map[string]loginAttempts{}}
	now := time.Now()
	for i := 0; i < maxLoginSources; i++ {
		if !limiter.allow(strconv.Itoa(i), now) {
			t.Fatal("capacity reached too early")
		}
	}
	if limiter.allow("overflow", now) {
		t.Fatal("source capacity exceeded")
	}
	for i := 1; i < 10; i++ {
		if !limiter.allow("0", now) {
			t.Fatal("attempt budget reached too early")
		}
	}
	if limiter.allow("0", now) {
		t.Fatal("attempt budget exceeded")
	}
	if !limiter.allow("overflow", now.Add(loginWindow)) || len(limiter.entries) != 1 {
		t.Fatal("expired sources did not free capacity")
	}
}
