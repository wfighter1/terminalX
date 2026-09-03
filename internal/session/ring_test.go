package session

import (
	"bytes"
	"testing"
)

func TestRingWrapAndTail(t *testing.T) {
	r := NewRing(8)
	r.Write([]byte("abcde"))
	r.Write([]byte("fgh"))
	if got, _ := r.Tail(100); string(got) != "abcdefgh" {
		t.Fatalf("full: %q", got)
	}
	r.Write([]byte("ij")) // wraps: drops "ab"
	if got, seq := r.Tail(100); string(got) != "cdefghij" || seq != 10 {
		t.Fatalf("wrap: %q seq=%d", got, seq)
	}
	if got, _ := r.Tail(3); string(got) != "hij" {
		t.Fatalf("tail 3: %q", got)
	}
	r.Write([]byte("0123456789ABCDEF")) // larger than capacity
	if got, seq := r.Tail(100); string(got) != "89ABCDEF" || seq != 26 || r.Len() != 8 {
		t.Fatalf("big write: %q seq=%d len=%d", got, seq, r.Len())
	}
	if got, _ := r.Tail(0); len(got) != 0 {
		t.Fatalf("tail 0 should be empty")
	}
}

func TestRingDelta(t *testing.T) {
	r := NewRing(8)
	r.Write([]byte("abcdefghij")) // seq 10, retains "cdefghij" (offsets 2..10)
	tests := []struct {
		name    string
		lastSeq uint64
		want    string
		ok      bool
	}{
		{"equal to seq", 10, "", true},
		{"one behind", 9, "j", true},
		{"oldest retained", 2, "cdefghij", true},
		{"before window", 1, "", false},
		{"zero before window", 0, "", false},
		{"future", 11, "", false},
	}
	for _, tc := range tests {
		got, ok := r.Delta(tc.lastSeq)
		if ok != tc.ok || string(got) != tc.want {
			t.Errorf("%s: got %q ok=%v want %q ok=%v", tc.name, got, ok, tc.want, tc.ok)
		}
	}
	// Fresh ring: Delta(0) is an empty, valid delta.
	if d, ok := NewRing(4).Delta(0); !ok || len(d) != 0 {
		t.Errorf("empty ring delta(0): %q %v", d, ok)
	}
}

func TestRingLargeRandomConsistency(t *testing.T) {
	r := NewRing(1000)
	var all []byte
	for i := 0; i < 500; i++ {
		chunk := bytes.Repeat([]byte{byte('a' + i%26)}, 1+i%37)
		r.Write(chunk)
		all = append(all, chunk...)
	}
	got, seq := r.Tail(r.Cap())
	if seq != uint64(len(all)) {
		t.Fatalf("seq %d != %d", seq, len(all))
	}
	if !bytes.Equal(got, all[len(all)-1000:]) {
		t.Fatal("tail mismatch")
	}
	d, ok := r.Delta(seq - 300)
	if !ok || !bytes.Equal(d, all[len(all)-300:]) {
		t.Fatal("delta mismatch")
	}
}
