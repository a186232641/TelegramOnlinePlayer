package auth

import (
	"testing"
	"time"
)

func TestLimiter_LockAfterMaxFails(t *testing.T) {
	l := NewLoginLimiter(3, 15*time.Minute, time.Hour)
	now := time.Now()

	for i := 0; i < 3; i++ {
		l.RecordFail("1.2.3.4", now.Add(time.Duration(i)*time.Second))
	}
	locked, retry := l.Locked("1.2.3.4", now.Add(3*time.Second))
	if !locked {
		t.Fatal("expected locked")
	}
	if retry <= 0 || retry > 15*time.Minute {
		t.Fatalf("retry duration odd: %v", retry)
	}
}

func TestLimiter_NotLockedUnderThreshold(t *testing.T) {
	l := NewLoginLimiter(3, time.Minute, time.Hour)
	now := time.Now()
	l.RecordFail("ip", now)
	l.RecordFail("ip", now)
	locked, _ := l.Locked("ip", now)
	if locked {
		t.Fatal("should not be locked at 2 fails")
	}
}

func TestLimiter_SuccessClears(t *testing.T) {
	l := NewLoginLimiter(3, time.Minute, time.Hour)
	now := time.Now()
	l.RecordFail("ip", now)
	l.RecordFail("ip", now)
	l.RecordSuccess("ip")
	l.RecordFail("ip", now)
	locked, _ := l.Locked("ip", now)
	if locked {
		t.Fatal("should be fresh after success")
	}
}

func TestLimiter_WindowExpiry(t *testing.T) {
	l := NewLoginLimiter(3, time.Minute, time.Minute)
	now := time.Now()
	l.RecordFail("ip", now)
	l.RecordFail("ip", now.Add(2*time.Minute))
	l.RecordFail("ip", now.Add(2*time.Minute))
	locked, _ := l.Locked("ip", now.Add(2*time.Minute))
	if locked {
		t.Fatal("old fail should not count toward lock")
	}
}
