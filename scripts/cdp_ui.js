// Headless UI smoke test: screenshots of the login page and the logged-in app.
// Usage: node cdp_ui.js <baseURL> <cookieJar>
const http = require('http');
const fs = require('fs');
const { spawn } = require('child_process');
const path = require('path');
const ROOT = process.env.ECHO_ROOT || process.cwd();
const TMP = path.join(ROOT, '.tmp');

const base = process.argv[2] || 'http://127.0.0.1:8090';
const jar = process.argv[3] || path.join(TMP, 'echo.cookies');
const port = 9224;
const profile = path.join(TMP, 'chrome-ui');
fs.mkdirSync(profile, { recursive: true });
fs.mkdirSync(path.join(TMP, 'ui'), { recursive: true });

function sessionCookie() {
  const lines = fs.readFileSync(jar, 'utf8').split('\n');
  for (const line of lines) {
    if (line.includes('echo_session')) {
      const parts = line.split('\t');
      return parts[parts.length - 1].trim();
    }
  }
  throw new Error('no echo_session in ' + jar);
}

const chrome = spawn('google-chrome', [
  '--headless=new', '--disable-gpu', '--no-sandbox',
  `--user-data-dir=${profile}`, `--remote-debugging-port=${port}`,
  '--window-size=1280,900', 'about:blank',
], { stdio: 'ignore' });

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function getJSON(path) {
  return new Promise((resolve, reject) => {
    http.get({ host: '127.0.0.1', port, path }, (res) => {
      let data = '';
      res.on('data', (d) => (data += d));
      res.on('end', () => { try { resolve(JSON.parse(data)); } catch (e) { reject(e); } });
    }).on('error', reject);
  });
}

(async () => {
  let target = null;
  for (let i = 0; i < 50; i++) {
    try {
      const list = await getJSON('/json/list');
      target = list.find((t) => t.type === 'page');
      if (target) break;
    } catch (e) { /* retry */ }
    await sleep(200);
  }
  if (!target) { console.error('no page target'); chrome.kill(); process.exit(1); }

  const ws = new WebSocket(target.webSocketDebuggerUrl);
  const pending = new Map();
  let seq = 0;
  const events = [];
  const call = (method, params = {}) => new Promise((resolve) => {
    const id = ++seq;
    pending.set(id, resolve);
    ws.send(JSON.stringify({ id, method, params }));
  });
  ws.onmessage = (ev) => {
    const msg = JSON.parse(ev.data);
    if (msg.id && pending.has(msg.id)) { pending.get(msg.id)(msg.result); pending.delete(msg.id); }
    if (msg.method === 'Runtime.exceptionThrown') {
      const d = msg.params.exceptionDetails;
      events.push('EXCEPTION: ' + ((d.exception && d.exception.description) || d.text));
    }
    if (msg.method === 'Runtime.consoleAPICalled' && msg.params.type === 'error') {
      events.push('CONSOLE: ' + (msg.params.args || []).map((a) => a.value ?? a.description ?? '').join(' '));
    }
  };
  await new Promise((resolve) => { ws.onopen = resolve; });

  await call('Page.enable');
  await call('Runtime.enable');

  async function shot(name) {
    const res = await call('Page.captureScreenshot', { format: 'png' });
    fs.writeFileSync(path.join(TMP, 'ui', `${name}.png`), Buffer.from(res.data, 'base64'));
    console.log('saved', name);
  }
  async function state() {
    const res = await call('Runtime.evaluate', {
      expression: `JSON.stringify({
        title: document.title,
        authHidden: document.getElementById('auth-view')?.hidden,
        appHidden: document.getElementById('app-view')?.hidden,
        email: document.getElementById('user-email')?.textContent,
        text: document.body.innerText.slice(0, 300)
      })`,
      returnByValue: true,
    });
    return res.result.value;
  }

  // 1. login page
  await call('Page.navigate', { url: base + '/' });
  await sleep(2500);
  console.log('LOGIN STATE', await state());
  await shot('ui-login');

  // 2. inject session cookie and open search page
  const cookie = sessionCookie();
  await call('Network.enable');
  const setRes = await call('Network.setCookie', {
    name: 'echo_session', value: cookie, url: base, path: '/', httpOnly: true,
  });
  console.log('setCookie', JSON.stringify(setRes));
  const cookies = await call('Network.getCookies', { urls: [base] });
  console.log('cookies', JSON.stringify(cookies.cookies?.map((c) => c.name)));
  await call('Page.navigate', { url: base + '/?_=' + Date.now() + '#/search' });
  await sleep(3500);
  console.log('APP STATE', await state());
  await shot('ui-search');

  // 3. library tab
  await call('Page.navigate', { url: base + '/?_=' + Date.now() + '#/library' });
  await sleep(3000);
  console.log('LIBRARY STATE', await state());
  await shot('ui-library');

  // 4. audiobooks tab
  await call('Page.navigate', { url: base + '/?_=' + Date.now() + '#/audiobooks' });
  await sleep(3000);
  await shot('ui-audiobooks');

  console.log('EVENTS:\n' + (events.length ? events.join('\n') : '(none)'));
  chrome.kill();
  process.exit(0);
})().catch((e) => { console.error(e); chrome.kill(); process.exit(1); });
