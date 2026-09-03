package proto

import (
	"bytes"
	"errors"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	f := Frame{Type: FrameOutput, Flags: FlagMore, SID: 0x01020304, Seq: 1 << 40, Payload: []byte("hello 世界")}
	b, err := f.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != HeaderSize+len(f.Payload) {
		t.Fatalf("len=%d", len(b))
	}
	if b[0] != Version {
		t.Fatalf("version byte %d", b[0])
	}
	g, err := Unmarshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if g.Type != f.Type || g.Flags != f.Flags || g.SID != f.SID || g.Seq != f.Seq || !bytes.Equal(g.Payload, f.Payload) {
		t.Fatalf("mismatch: %+v vs %+v", g, f)
	}
	ty, sid, err := PeekHeader(b)
	if err != nil || ty != FrameOutput || sid != f.SID {
		t.Fatalf("peek: %v %v %v", ty, sid, err)
	}
}

func TestFrameFailClosed(t *testing.T) {
	f := Frame{Type: FrameInput, SID: 1, Payload: []byte("x")}
	b, _ := f.Marshal()
	b[0] = 2
	if _, err := Unmarshal(b); !errors.Is(err, ErrVersion) {
		t.Fatalf("want ErrVersion, got %v", err)
	}
	if _, _, err := PeekHeader(b); !errors.Is(err, ErrVersion) {
		t.Fatalf("want ErrVersion, got %v", err)
	}
	if _, err := Unmarshal(b[:5]); !errors.Is(err, ErrShort) {
		t.Fatalf("want ErrShort, got %v", err)
	}
	b[0] = Version
	if _, err := Unmarshal(append(b, 'y')); !errors.Is(err, ErrLength) {
		t.Fatalf("want ErrLength, got %v", err)
	}
	big := Frame{Type: FrameOutput, Payload: make([]byte, MaxPayload+1)}
	if _, err := big.Marshal(); !errors.Is(err, ErrPayloadBig) {
		t.Fatalf("want ErrPayloadBig, got %v", err)
	}
}

func TestChunk(t *testing.T) {
	payload := make([]byte, MaxPayload*2+10)
	for i := range payload {
		payload[i] = byte(i)
	}
	fs := Chunk(FrameOutput, 7, 100, payload, FlagReset)
	if len(fs) != 3 {
		t.Fatalf("chunks=%d", len(fs))
	}
	if fs[0].Flags&FlagReset == 0 || fs[0].Flags&FlagMore == 0 {
		t.Fatalf("first flags %b", fs[0].Flags)
	}
	if fs[1].Flags&FlagReset != 0 || fs[1].Flags&FlagMore == 0 {
		t.Fatalf("middle flags %b", fs[1].Flags)
	}
	if fs[2].Flags&FlagMore != 0 {
		t.Fatalf("last flags %b", fs[2].Flags)
	}
	if fs[0].Seq != 100 || fs[1].Seq != 100+MaxPayload || fs[2].Seq != 100+2*MaxPayload {
		t.Fatalf("seqs %d %d %d", fs[0].Seq, fs[1].Seq, fs[2].Seq)
	}
	var joined []byte
	for _, f := range fs {
		joined = append(joined, f.Payload...)
	}
	if !bytes.Equal(joined, payload) {
		t.Fatal("payload not reassembled")
	}
	empty := Chunk(FrameEOF, 1, 5, nil, 0)
	if len(empty) != 1 || empty[0].Seq != 5 {
		t.Fatalf("empty chunk %+v", empty)
	}
	snap := Chunk(FrameSnapshot, 1, 999, make([]byte, MaxPayload+1), FlagReset)
	if snap[0].Seq != 999 || snap[1].Seq != 999 {
		t.Fatalf("snapshot seq must repeat: %d %d", snap[0].Seq, snap[1].Seq)
	}
}

func TestPayloadHelpers(t *testing.T) {
	c, r, err := ParseResize(ResizePayload(120, 40))
	if err != nil || c != 120 || r != 40 {
		t.Fatalf("resize %d %d %v", c, r, err)
	}
	if _, _, err := ParseResize([]byte{1}); err == nil {
		t.Fatal("want error")
	}
	code, err := ParseEOF(EOFPayload(-1))
	if err != nil || code != -1 {
		t.Fatalf("eof %d %v", code, err)
	}
}

func TestMsgRoundTrip(t *testing.T) {
	m := Msg{T: TSessionOpen, DeviceID: "d1", ReqID: "r1", Open: &OpenRequest{Shell: "bash", Tool: ToolClaude, Name: "x", Cols: 100, Rows: 30}}
	b, err := m.Encode()
	if err != nil {
		t.Fatal(err)
	}
	g, err := Decode(b)
	if err != nil || g.T != TSessionOpen || g.Open == nil || g.Open.Cols != 100 || g.DeviceID != "d1" {
		t.Fatalf("decode %+v %v", g, err)
	}
}
