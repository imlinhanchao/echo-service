// UI check for the per-voice TTS sample button in the audiobook form.
// Usage: node cdp_sample.js <baseURL> <cookieJar>
const http = require('http');
const fs = require('fs');
const { spawn } = require('child_process');
const path = require('path');
const ROOT = process.env.ECHO_ROOT || process.cwd();
const TMP = path.join(ROOT, '.tmp');

const base = process.argv[2] || 'http://127.0.0.1:8090';
const jar = process.argv[3] || path.join(TMP, 'echo.cookies');
const port = 9225;
const profile = path.join(TMP, 'chrome-sample');
fs.mkdirSync(profile, { recursive: true });
fs.mkdirSync(path.join(TMP, 'ui'), { recursive: true });

function sessionCookie() {
  for (const line of fs.readFileSync(jar, 'utf8').split('\n')) {
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
  };
  await new Promise((resolve) => { ws.onopen = resolve; });
  await call('Page.enable');
  await call('Runtime.enable');
  await call('Network.enable');
  await call('Network.setCookie', { name: 'echo_session', value: sessionCookie(), url: base, path: '/', httpOnly: true });

  const evaluate = async (expression) => {
    const res = await call('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true });
    if (res.exceptionDetails) throw new Error(res.exceptionDetails.text);
    return res.result.value;
  };

  await call('Page.navigate', { url: base + '/?_=' + Date.now() + '#/library' });
  await sleep(3000);
  await evaluate(`document.querySelector('[data-action="convert-book"]')?.click()`);
  await sleep(2500);
  const formState = await evaluate(`JSON.stringify({
    form: !!document.getElementById('audiobook-form'),
    sampleBtn: !!document.getElementById('ab-sample-btn'),
    model: document.getElementById('ab-model')?.value,
    modelLabel: document.querySelector('#ab-model')?.selectedOptions?.[0]?.textContent,
    fallbackOptions: document.querySelectorAll('#ab-fallback option').length,
    summary: document.getElementById('ab-chapter-summary')?.textContent,
    submitDisabled: document.getElementById('ab-submit')?.disabled,
    voice: document.getElementById('ab-voice')?.value
  })`);
  console.log('FORM', formState);

  await evaluate(`document.getElementById('ab-sample-btn')?.click()`);
  // wait for the audio element to get a src
  let audioState = '';
  for (let i = 0; i < 40; i++) {
    await sleep(500);
    audioState = await evaluate(`JSON.stringify((() => {
      const a = document.getElementById('ab-sample-audio');
      return {
        hidden: a ? a.hidden : null,
        hasSrc: !!(a && a.src),
        duration: a && isFinite(a.duration) ? Number(a.duration.toFixed(2)) : null,
        hint: document.getElementById('ab-sample-hint')?.textContent || ''
      };
    })())`);
    if (JSON.parse(audioState).hasSrc) break;
  }
  console.log('AUDIO', audioState);

  const shot = await call('Page.captureScreenshot', { format: 'png' });
  fs.writeFileSync(path.join(TMP, 'ui', 'ui-sample.png'), Buffer.from(shot.data, 'base64'));
  console.log('saved ui-sample');
  console.log('EVENTS:', events.length ? events.join('\n') : '(none)');
  chrome.kill();
  process.exit(0);
})().catch((e) => { console.error(e); chrome.kill(); process.exit(1); });
