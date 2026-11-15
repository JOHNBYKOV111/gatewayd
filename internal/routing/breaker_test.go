package routing

import (
	"testing"
	"time"
)

func TestBreaker_ClosedToOpen(t *testing.T) {
	b := NewBreaker(3, time.Second)
	for i := 0; i < 3; i++ {
		if !b.Allow() {
			t.Fatalf("attempt %d: expected allow in closed state", i)
		}
		b.RecordFailure()
	}
	if b.State() != StateOpen {
		t.Errorf("state = %v, want open", b.State())
	}
}

func TestBreaker_OpenToHalfOpen(t *testing.T) {
	b := NewBreaker(1, 50*time.Millisecond)
	b.RecordFailure() // open
	if b.State() != StateOpen {
		t.Fatalf("state = %v, want open", b.State())
	}
	if b.Allow() {
		t.Fatal("expected deny in open state")
	}
	time.Sleep(60 * time.Millisecond)
	if !b.Allow() {
		t.Fatal("expected allow after timeout (half-open)")
	}
	if b.State() != StateHalfOpen {
		t.Errorf("state = %v, want half-open", b.State())
	}
}

func TestBreaker_HalfOpenToClosed(t *testing.T) {
	b := NewBreaker(1, 50*time.Millisecond)
	b.RecordFailure()
	time.Sleep(60 * time.Millisecond)
	b.Allow() // transition to half-open
	b.RecordSuccess()
	if b.State() != StateClosed {
		t.Errorf("state = %v, want closed", b.State())
	}
}

func TestBreaker_HalfOpenToOpen(t *testing.T) {
	b := NewBreaker(1, 50*time.Millisecond)
	b.RecordFailure()
	time.Sleep(60 * time.Millisecond)
	b.Allow() // half-open
	b.RecordFailure()
	if b.State() != StateOpen {
		t.Errorf("state = %v, want open", b.State())
	}
}
