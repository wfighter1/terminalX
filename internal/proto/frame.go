// Package proto defines the terminalX wire protocol shared by the relay, the
// agent and (mirrored in TypeScript) the web console.
//
// Two planes travel over a single WebSocket connection:
//
//   - control plane: JSON text messages (see messages.go)
//   - data plane:    binary frames with an 18-byte header (this file)
//
// Data frame layout (big endian):
//
//	| ver u8 | type u8 | flags u8 | reserved u8 | sid u32 | seq u64 | len u16 | payload … |
//
// The relay only ever reads the header; the payload is opaque bytes from day
// one so that per-frame end-to-end encryption can be added later without
// changing the header.
package proto

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	// Version is the only frame version this build understands. Any other
	// value must make the receiver close the connection (fail-closed).
	Version = 1
	// HeaderSize is the fixed frame header length in bytes.
	HeaderSize = 18
	// MaxPayload is the largest payload a single frame may carry.
	MaxPayload = 65535
)

// FrameType identifies what the payload of a data frame carries.
type FrameType uint8

const (
	// FrameOutput carries PTY output bytes. Seq is the stream offset of the
	// first payload byte (a monotonic byte counter per session).
	FrameOutput FrameType = 1
	// FrameInput carries bytes to write to the PTY. Seq is unused (0).
	FrameInput FrameType = 2
	// FrameResize carries cols/rows (see ResizePayload). Seq is unused.
	FrameResize FrameType = 3
	// FrameSnapshot carries a replay of the tail of the scrollback buffer.
	// Seq is the stream offset that the end of the snapshot corresponds to,
	// so the client can continue from there with FrameOutput frames.
	FrameSnapshot FrameType = 4
	// FrameEOF signals the PTY process exited; payload is the exit code.
	FrameEOF FrameType = 5
	// FrameAck acknowledges consumption up to Seq (backpressure).
	FrameAck FrameType = 6
)

// Frame flags.
const (
	// FlagMore marks that more chunks of the same logical message follow
	// (used when a snapshot is split into several frames).
	FlagMore uint8 = 1 << 0
	// FlagReset asks the terminal to clear its screen state before applying
	// the payload (sent on the first snapshot chunk of an attach).
	FlagReset uint8 = 1 << 1
)

// Frame is a decoded data-plane frame.
type Frame struct {
	Type    FrameType
	Flags   uint8
	SID     uint32
	Seq     uint64
	Payload []byte
}

var (
	ErrShort      = errors.New("proto: frame too short")
	ErrVersion    = errors.New("proto: unsupported frame version")
	ErrLength     = errors.New("proto: payload length mismatch")
	ErrPayloadBig = errors.New("proto: payload exceeds 65535 bytes")
	ErrBadPayload = errors.New("proto: malformed payload")
)

// Marshal encodes the frame. It fails if the payload is larger than
// MaxPayload; use Chunk to split large payloads.
func (f Frame) Marshal() ([]byte, error) {
	if len(f.Payload) > MaxPayload {
		return nil, ErrPayloadBig
	}
	b := make([]byte, HeaderSize+len(f.Payload))
	b[0] = Version
	b[1] = byte(f.Type)
	b[2] = f.Flags
	b[3] = 0
	binary.BigEndian.PutUint32(b[4:8], f.SID)
	binary.BigEndian.PutUint64(b[8:16], f.Seq)
	binary.BigEndian.PutUint16(b[16:18], uint16(len(f.Payload)))
	copy(b[HeaderSize:], f.Payload)
	return b, nil
}

// Unmarshal decodes a frame. The payload is copied out of b.
func Unmarshal(b []byte) (Frame, error) {
	if len(b) < HeaderSize {
		return Frame{}, ErrShort
	}
	if b[0] != Version {
		return Frame{}, fmt.Errorf("%w: got %d", ErrVersion, b[0])
	}
	n := int(binary.BigEndian.Uint16(b[16:18]))
	if len(b) != HeaderSize+n {
		return Frame{}, ErrLength
	}
	f := Frame{
		Type:  FrameType(b[1]),
		Flags: b[2],
		SID:   binary.BigEndian.Uint32(b[4:8]),
		Seq:   binary.BigEndian.Uint64(b[8:16]),
	}
	f.Payload = append([]byte(nil), b[HeaderSize:]...)
	return f, nil
}

// PeekHeader validates the header and returns type and sid without copying
// the payload. The relay uses this to route frames by sid.
func PeekHeader(b []byte) (t FrameType, sid uint32, err error) {
	if len(b) < HeaderSize {
		return 0, 0, ErrShort
	}
	if b[0] != Version {
		return 0, 0, fmt.Errorf("%w: got %d", ErrVersion, b[0])
	}
	n := int(binary.BigEndian.Uint16(b[16:18]))
	if len(b) != HeaderSize+n {
		return 0, 0, ErrLength
	}
	return FrameType(b[1]), binary.BigEndian.Uint32(b[4:8]), nil
}

// Chunk splits payload into frames of at most MaxPayload bytes. All chunks
// but the last carry FlagMore; the first chunk additionally carries extra
// (e.g. FlagReset). seq is assigned to the first chunk and advanced by the
// chunk length for FrameOutput; for other types seq is repeated verbatim.
func Chunk(t FrameType, sid uint32, seq uint64, payload []byte, extra uint8) []Frame {
	if len(payload) == 0 {
		return []Frame{{Type: t, Flags: extra, SID: sid, Seq: seq}}
	}
	var out []Frame
	off := 0
	for off < len(payload) {
		end := off + MaxPayload
		if end > len(payload) {
			end = len(payload)
		}
		f := Frame{Type: t, SID: sid, Seq: seq, Payload: payload[off:end]}
		if off == 0 {
			f.Flags |= extra
		}
		if end < len(payload) {
			f.Flags |= FlagMore
		}
		if t == FrameOutput {
			seq += uint64(end - off)
		}
		out = append(out, f)
		off = end
	}
	return out
}

// ResizePayload encodes cols/rows for a FrameResize frame.
func ResizePayload(cols, rows uint16) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint16(b[0:2], cols)
	binary.BigEndian.PutUint16(b[2:4], rows)
	return b
}

// ParseResize decodes a FrameResize payload.
func ParseResize(p []byte) (cols, rows uint16, err error) {
	if len(p) != 4 {
		return 0, 0, ErrBadPayload
	}
	return binary.BigEndian.Uint16(p[0:2]), binary.BigEndian.Uint16(p[2:4]), nil
}

// EOFPayload encodes a process exit code for a FrameEOF frame.
func EOFPayload(code int32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, uint32(code))
	return b
}

// ParseEOF decodes a FrameEOF payload.
func ParseEOF(p []byte) (int32, error) {
	if len(p) != 4 {
		return 0, ErrBadPayload
	}
	return int32(binary.BigEndian.Uint32(p)), nil
}
