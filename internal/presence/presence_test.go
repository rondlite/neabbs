package presence

import (
	"errors"
	"fmt"
	"testing"
)

func TestCapsAndLines(t *testing.T) {
	r := NewRegistry()

	s1, err := r.Add("fp1")
	if err != nil || s1.Line != 1 {
		t.Fatalf("first session: %v line=%d", err, s1.Line)
	}
	s2, _ := r.Add("fp1")
	s3, _ := r.Add("fp1")
	if s2.Line != 2 || s3.Line != 3 {
		t.Fatalf("lines %d %d, want 2 3", s2.Line, s3.Line)
	}
	if _, err := r.Add("fp1"); !errors.Is(err, ErrTooManySessions) {
		t.Fatalf("4th session for fp1: %v", err)
	}

	r.Remove(s2)
	s4, _ := r.Add("fp1")
	if s4.Line != 2 {
		t.Fatalf("lowest free line = %d, want 2", s4.Line)
	}

	// Fill past 24 lines: overflow gets line 0.
	for i := 0; i < 30; i++ {
		s, err := r.Add(fmt.Sprintf("fp-%d", i))
		if err != nil {
			t.Fatal(err)
		}
		if i >= 21 && s.Line != 0 { // 3 already held by fp1
			t.Fatalf("session %d got line %d, want 0 (overflow)", i, s.Line)
		}
	}
	if r.LinesBusy() != 24 {
		t.Fatalf("LinesBusy = %d, want 24", r.LinesBusy())
	}
}

func TestGlobalCap(t *testing.T) {
	r := NewRegistry()
	for i := 0; i < MaxGlobal; i++ {
		if _, err := r.Add(fmt.Sprintf("fp-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.Add("one-more"); !errors.Is(err, ErrFull) {
		t.Fatalf("want ErrFull, got %v", err)
	}
}
