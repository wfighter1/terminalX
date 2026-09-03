// End-to-end smoke test: relay + agent + web on one machine.
//
// Prereqs (run from repo root):
//   go build -o bin/tx-relay ./cmd/tx-relay && go build -o bin/tx-agent ./cmd/tx-agent
//   (cd web && npm run build)
//   TX_ADMIN_PASSWORD=test ./bin/tx-relay --listen 127.0.0.1:8080 --data /tmp/txdata --web-dir web/dist &
//   node scripts/e2e.mjs            # pairs an agent itself via the API + `tx-agent pair`, then drives the UI
//
// Env: RELAY_URL (default http://127.0.0.1:8080), TX_ADMIN_PASSWORD (default test), AGENT_BIN (default ./bin/tx-agent)
import { chromium } from '/opt/node22/lib/node_modules/playwright/index.mjs';
import { spawn } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

const RELAY = process.env.RELAY_URL ?? 'http://127.0.0.1:8080';
const PASSWORD = process.env.TX_ADMIN_PASSWORD ?? 'test';
const AGENT_BIN = process.env.AGENT_BIN ?? './bin/tx-agent';
const OUT = process.env.E2E_OUT ?? '/tmp/claude-0/-home-user-terminalX/cefc0b20-d74d-594d-9eba-d5a9c6a9a0f3/scratchpad';

const log = (...a) => console.log('[e2e]', ...a);
const fail = (msg) => { console.error('[e2e] FAIL:', msg); process.exit(1); };

async function api(method, p, body, cookie) {
  const res = await fetch(RELAY + p, {
    method,
    headers: { 'Content-Type': 'application/json', ...(cookie ? { Cookie: cookie } : {}) },
    body: body ? JSON.stringify(body) : undefined,
  });
  const text = await res.text();
  return { status: res.status, body: text ? JSON.parse(text) : null, setCookie: res.headers.get('set-cookie') };
}

// 1) login via API to get a cookie for pairing
const login = await api('POST', '/api/login', { password: PASSWORD });
if (login.status !== 200) fail(`login ${login.status} ${JSON.stringify(login.body)}`);
const cookie = (login.setCookie ?? '').split(';')[0];
log('login ok');

// 2) create pairing code and pair an agent
const pair = await api('POST', '/api/pair/new', undefined, cookie);
if (pair.status !== 200 || !pair.body?.code) fail(`pair/new ${pair.status} ${JSON.stringify(pair.body)}`);
log('pair code', pair.body.code);
const cfgDir = fs.mkdtempSync(path.join(os.tmpdir(), 'tx-agent-'));
const cfg = path.join(cfgDir, 'agent.json');
const pairProc = spawn(AGENT_BIN, ['pair', '--relay', RELAY, '--code', pair.body.code, '--name', 'e2e-linux', '--config', cfg], { stdio: ['ignore', 'pipe', 'pipe'] });
let pairOut = '';
pairProc.stdout.on('data', (d) => (pairOut += d));
pairProc.stderr.on('data', (d) => (pairOut += d));
const pairCode = await new Promise((r) => pairProc.on('exit', r));
if (pairCode !== 0) fail(`tx-agent pair exit ${pairCode}: ${pairOut}`);
log('paired:', pairOut.trim().split('\n').slice(-2).join(' | '));

// 3) run the agent
const agent = spawn(AGENT_BIN, ['run', '--config', cfg], { stdio: ['ignore', 'pipe', 'pipe'] });
let agentLog = '';
agent.stdout.on('data', (d) => (agentLog += d));
agent.stderr.on('data', (d) => (agentLog += d));
const cleanup = () => { try { agent.kill(); } catch {} };
process.on('exit', cleanup);

// 4) drive the UI
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
const errors = [];
page.on('pageerror', (e) => errors.push(String(e)));
page.on('console', (m) => { if (m.type() === 'error') errors.push(m.text()); });
await page.goto(RELAY + '/');
await page.fill('input[type=password]', PASSWORD);
await page.click('button:has-text("登录")');
await page.waitForSelector('.device', { timeout: 15000 });
await page.waitForFunction(() => Array.from(document.querySelectorAll('.device-status')).some((el) => !el.classList.contains('off')), null, { timeout: 20000 }).catch(() => fail('device never came online\n' + agentLog));
log('device online');
await page.screenshot({ path: path.join(OUT, 'e2e-1-online.png') });

// new session: shell
await page.click('button:has-text("新会话")');
await page.selectOption('select >> nth=1', 'shell');
await page.fill('input[placeholder*="terminalx-relay"]', 'e2e-shell');
await page.click('button:has-text("启动")');
await page.waitForSelector('.tab[aria-selected="true"]', { timeout: 15000 });
await page.waitForSelector('.xterm', { timeout: 10000 });
await page.waitForTimeout(1500);
log('session opened');

// type via composer
const marker = 'hello-e2e-' + Date.now();
await page.fill('input[aria-label="整行输入"]', `echo ${marker}`);
await page.press('input[aria-label="整行输入"]', 'Enter');
await page.waitForFunction((m) => document.querySelector('.xterm')?.textContent?.includes(m), marker, { timeout: 10000 }).catch(() => fail('marker not echoed in terminal\n' + agentLog));
log('echo round-trip ok');
await page.screenshot({ path: path.join(OUT, 'e2e-2-echo.png') });

// key bar: Ctrl-C signal + Esc
await page.click('.unstick button:has-text("Ctrl-C")');
await page.waitForTimeout(300);

// reload → snapshot replay must contain the marker
await page.reload();
await page.waitForSelector('.device', { timeout: 15000 });
await page.click(`.sess:has-text("e2e-shell")`);
await page.waitForFunction((m) => document.querySelector('.xterm')?.textContent?.includes(m), marker, { timeout: 10000 }).catch(() => fail('snapshot replay missing marker\n' + agentLog));
log('snapshot replay ok');
await page.screenshot({ path: path.join(OUT, 'e2e-3-replay.png') });

// inbox page renders
await page.click('nav button:has-text("待确认")');
await page.waitForSelector('text=待确认收件箱');
await page.click('nav button:has-text("控制台")');

// close session
await page.click('button:has-text("结束会话")').catch(() => {});
page.once('dialog', (d) => d.accept());
await page.waitForTimeout(800);

if (errors.length) log('page errors:', errors);
await browser.close();
cleanup();
if (errors.some((e) => !/ERR_CONNECTION|Failed to load resource|WebSocket is closed/.test(e))) fail('page errors: ' + errors.join('\n'));
log('ALL OK');
process.exit(0);
