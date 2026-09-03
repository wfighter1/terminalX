// Mirror of internal/proto/frame.go. Keep the two in sync.
//
// | ver u8 | type u8 | flags u8 | reserved u8 | sid u32 | seq u64 | len u16 | payload … |

export const FRAME_VERSION = 1;
export const HEADER_SIZE = 18;
export const MAX_PAYLOAD = 65535;

export const FrameType = {
  Output: 1,
  Input: 2,
  Resize: 3,
  Snapshot: 4,
  EOF: 5,
  Ack: 6,
} as const;
export type FrameTypeValue = (typeof FrameType)[keyof typeof FrameType];

export const FLAG_MORE = 1 << 0;
export const FLAG_RESET = 1 << 1;

export interface Frame {
  type: FrameTypeValue;
  flags: number;
  sid: number;
  seq: bigint;
  payload: Uint8Array;
}

export class FrameError extends Error {}

export function encodeFrame(f: Frame): Uint8Array {
  if (f.payload.length > MAX_PAYLOAD) throw new FrameError('payload exceeds 65535 bytes');
  const buf = new Uint8Array(HEADER_SIZE + f.payload.length);
  const dv = new DataView(buf.buffer);
  dv.setUint8(0, FRAME_VERSION);
  dv.setUint8(1, f.type);
  dv.setUint8(2, f.flags);
  dv.setUint8(3, 0);
  dv.setUint32(4, f.sid >>> 0, false);
  dv.setBigUint64(8, f.seq, false);
  dv.setUint16(16, f.payload.length, false);
  buf.set(f.payload, HEADER_SIZE);
  return buf;
}

export function decodeFrame(data: ArrayBuffer | Uint8Array): Frame {
  const buf = data instanceof Uint8Array ? data : new Uint8Array(data);
  if (buf.length < HEADER_SIZE) throw new FrameError('frame too short');
  const dv = new DataView(buf.buffer, buf.byteOffset, buf.byteLength);
  const ver = dv.getUint8(0);
  if (ver !== FRAME_VERSION) throw new FrameError(`unsupported frame version ${ver}`);
  const len = dv.getUint16(16, false);
  if (buf.length !== HEADER_SIZE + len) throw new FrameError('payload length mismatch');
  return {
    type: dv.getUint8(1) as FrameTypeValue,
    flags: dv.getUint8(2),
    sid: dv.getUint32(4, false),
    seq: dv.getBigUint64(8, false),
    payload: buf.slice(HEADER_SIZE),
  };
}

export function inputFrame(sid: number, bytes: Uint8Array): Uint8Array {
  return encodeFrame({ type: FrameType.Input, flags: 0, sid, seq: 0n, payload: bytes });
}

export function resizeFrame(sid: number, cols: number, rows: number): Uint8Array {
  const p = new Uint8Array(4);
  const dv = new DataView(p.buffer);
  dv.setUint16(0, cols, false);
  dv.setUint16(2, rows, false);
  return encodeFrame({ type: FrameType.Resize, flags: 0, sid, seq: 0n, payload: p });
}

export function ackFrame(sid: number, seq: bigint): Uint8Array {
  return encodeFrame({ type: FrameType.Ack, flags: 0, sid, seq, payload: new Uint8Array(0) });
}

export function parseEOF(payload: Uint8Array): number {
  if (payload.length !== 4) throw new FrameError('malformed EOF payload');
  return new DataView(payload.buffer, payload.byteOffset, 4).getInt32(0, false);
}

/** Split a large input into frames of at most MAX_PAYLOAD bytes. */
export function chunkInput(sid: number, bytes: Uint8Array): Uint8Array[] {
  const out: Uint8Array[] = [];
  for (let off = 0; off < bytes.length; off += MAX_PAYLOAD) {
    out.push(inputFrame(sid, bytes.subarray(off, Math.min(off + MAX_PAYLOAD, bytes.length))));
  }
  if (bytes.length === 0) out.push(inputFrame(sid, bytes));
  return out;
}
