package session

import "sync"

// Ring is a fixed-capacity byte ring buffer that also tracks the absolute
// stream offset (seq) of the bytes it holds. seq is the total number of
// bytes ever written; the buffer holds bytes [seq-Len(), seq).
type Ring struct {
	mu    sync.Mutex
	buf   []byte
	start int    // index of the oldest byte
	n     int    // bytes currently stored
	seq   uint64 // total bytes written so far
}

// NewRing allocates a ring buffer of the given capacity.
func NewRing(capacity int) *Ring {
	if capacity <= 0 {
		panic("session: ring capacity must be > 0")
	}
	return &Ring{buf: make([]byte, capacity)}
}

// Write appends p, overwriting the oldest bytes when full. It returns the
// stream offset of the first byte of p.
func (r *Ring) Write(p []byte) (startSeq uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	startSeq = r.seq
	r.seq += uint64(len(p))
	c := len(r.buf)
	if len(p) >= c {
		// Only the tail of p survives; the ring becomes exactly full.
		copy(r.buf, p[len(p)-c:])
		r.start = 0
		r.n = c
		return startSeq
	}
	end := (r.start + r.n) % c
	first := copy(r.buf[end:], p)
	if first < len(p) {
		copy(r.buf, p[first:])
	}
	if r.n+len(p) > c {
		overflow := r.n + len(p) - c
		r.start = (r.start + overflow) % c
		r.n = c
	} else {
		r.n += len(p)
	}
	return startSeq
}

// Seq returns the current stream offset (total bytes written).
func (r *Ring) Seq() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.seq
}

// Len returns how many bytes are currently retained.
func (r *Ring) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.n
}

// Cap returns the capacity.
func (r *Ring) Cap() int { return len(r.buf) }

// Tail returns a copy of the last max bytes (or fewer if not that many are
// stored) together with the stream offset that corresponds to the end of the
// returned slice.
func (r *Ring) Tail(max int) (data []byte, endSeq uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if max > r.n {
		max = r.n
	}
	if max < 0 {
		max = 0
	}
	return r.copyLast(max), r.seq
}

// Delta returns the bytes from stream offset lastSeq up to the current seq.
// ok is false when lastSeq is outside the retained window (older than the
// oldest byte, or in the future); the caller should then send a snapshot.
// A lastSeq equal to the current seq yields an empty delta with ok=true.
func (r *Ring) Delta(lastSeq uint64) (data []byte, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	oldest := r.seq - uint64(r.n)
	if lastSeq < oldest || lastSeq > r.seq {
		return nil, false
	}
	return r.copyLast(int(r.seq - lastSeq)), true
}

// copyLast copies the last k stored bytes; caller holds mu.
func (r *Ring) copyLast(k int) []byte {
	out := make([]byte, k)
	if k == 0 {
		return out
	}
	c := len(r.buf)
	from := (r.start + r.n - k) % c
	first := copy(out, r.buf[from:])
	if first < k {
		copy(out[first:], r.buf[:k-first])
	}
	return out
}
