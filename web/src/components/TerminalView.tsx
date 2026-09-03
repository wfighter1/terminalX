import { useEffect, useRef } from 'react';
import { Terminal } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import { WebglAddon } from '@xterm/addon-webgl';
import { FLAG_MORE, FLAG_RESET, FrameType, ackFrame, chunkInput, parseEOF, resizeFrame, type Frame } from '../protocol/frame';
import { relay } from '../ws';

const enc = new TextEncoder();
const ACK_EVERY = 64 * 1024;

/**
 * One xterm instance per session. Stays mounted while the tab is open so the
 * screen state survives switching tabs; `active` toggles visibility and
 * triggers a refit + resize.
 */
export default function TerminalView({ sid, deviceId, active }: { sid: number; deviceId: string; active: boolean }) {
  const hostRef = useRef<HTMLDivElement>(null);
  const termRef = useRef<Terminal | null>(null);
  const fitRef = useRef<FitAddon | null>(null);

  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;
    const term = new Terminal({
      fontFamily: 'JetBrains Mono, Cascadia Mono, Consolas, Menlo, monospace',
      fontSize: 13,
      lineHeight: 1.2,
      scrollback: 10000,
      cursorBlink: true,
      allowProposedApi: true,
      windowsPty: { backend: 'conpty' },
      theme: { background: '#0A0D11', foreground: '#D7DEE7', cursor: '#D7DEE7' },
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(host);
    try {
      term.loadAddon(new WebglAddon());
    } catch {
      /* canvas fallback */
    }
    termRef.current = term;
    fitRef.current = fit;

    let unacked = 0;
    const onFrame = (f: Frame) => {
      switch (f.type) {
        case FrameType.Snapshot:
          if (f.flags & FLAG_RESET) term.reset();
          term.write(f.payload);
          if (!(f.flags & FLAG_MORE)) relay.setLastSeq(sid, f.seq);
          break;
        case FrameType.Output: {
          term.write(f.payload);
          relay.setLastSeq(sid, f.seq + BigInt(f.payload.length));
          unacked += f.payload.length;
          if (unacked >= ACK_EVERY) {
            unacked = 0;
            relay.sendFrame(ackFrame(sid, relay.getLastSeq(sid)));
          }
          break;
        }
        case FrameType.EOF: {
          let code = 0;
          try {
            code = parseEOF(f.payload);
          } catch {
            /* ignore */
          }
          term.write(`\r\n\x1b[2m[terminalX] 进程已退出，退出码 ${code}。会话保留，可从右侧「拉回」或关闭。\x1b[0m\r\n`);
          break;
        }
        default:
          break;
      }
    };

    const disp = term.onData((data) => {
      for (const fr of chunkInput(sid, enc.encode(data))) relay.sendFrame(fr);
    });
    const dispBin = term.onBinary((data) => {
      const bytes = new Uint8Array(data.length);
      for (let i = 0; i < data.length; i++) bytes[i] = data.charCodeAt(i) & 0xff;
      for (const fr of chunkInput(sid, bytes)) relay.sendFrame(fr);
    });

    const doFit = () => {
      if (!host.offsetParent && host.getClientRects().length === 0) return; // hidden
      try {
        fit.fit();
        relay.sendFrame(resizeFrame(sid, term.cols, term.rows));
      } catch {
        /* ignore */
      }
    };
    const ro = new ResizeObserver(() => doFit());
    ro.observe(host);

    // attach after first fit so the agent knows our size
    requestAnimationFrame(() => {
      doFit();
      relay.attach(deviceId, sid, onFrame);
    });

    return () => {
      ro.disconnect();
      disp.dispose();
      dispBin.dispose();
      relay.detach(sid);
      term.dispose();
      termRef.current = null;
      fitRef.current = null;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sid, deviceId]);

  useEffect(() => {
    if (!active) return;
    const term = termRef.current;
    const fit = fitRef.current;
    if (!term || !fit) return;
    const id = requestAnimationFrame(() => {
      try {
        fit.fit();
        relay.sendFrame(resizeFrame(sid, term.cols, term.rows));
        term.focus();
      } catch {
        /* ignore */
      }
    });
    return () => cancelAnimationFrame(id);
  }, [active, sid]);

  return <div className="term-slot" hidden={!active}><div ref={hostRef} style={{ height: '100%', width: '100%' }} /></div>;
}

/** Send raw bytes to a session's PTY (used by the key bar and the composer). */
export function sendKeys(sid: number, text: string): void {
  for (const fr of chunkInput(sid, enc.encode(text))) relay.sendFrame(fr);
}
