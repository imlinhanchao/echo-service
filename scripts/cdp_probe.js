// Minimal CDP client to capture console errors from the headless UI.
// Usage: node cdp_probe.js <url> [ms]
const http = require('http');
const { spawn } = require('child_process');
const path = require('path');
const ROOT = process.env.ECHO_ROOT || process.cwd();
const TMP = path.join(ROOT, '.tmp');
const fs = require('fs');

const url = process.argv[2] || 'http://127.0.0.1:8090/';
const waitMs = parseInt(process.argv[3] || '6000', 10);
const port = 9223;
const profile = path.join(TMP, 'chrome-cdp');
fs.mkdirSync(profile, { recursive: true });

const chrome = spawn('google-chrome', [
  '--headless=new', '--disable-gpu', '--no-sandbox',
  `--user-data-dir=${profile}`, `--remote-debugging-port=${port}`,
  'about:blank',
], { stdio: 'ignore' });

function getJSON(path) {
  return new Promise((resolve, reject) => {
    http.get({ host: '127.0.0.1', port, path }, (res) => {
      let data = '';
      res.on('data', (d) => (data += d));
      res.on('end', () => {
        try { resolve(JSON.parse(data)); } catch (e) { reject(e); }
      });
    }).on('error', reject);
  });
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

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
  const send = (id, method, params) => ws.send(JSON.stringify({ id, method, params }));
  const events = [];
  ws.onmessage = (ev) => {
    const msg = JSON.parse(ev.data);
    if (msg.method === 'Runtime.exceptionThrown') {
      const d = msg.params.exceptionDetails;
      events.push('EXCEPTION: ' + (d.exception && d.exception.description || d.text));
    } else if (msg.method === 'Runtime.consoleAPICalled') {
      const args = (msg.params.args || []).map((a) => a.value ?? a.description ?? '').join(' ');
      events.push('CONSOLE[' + msg.params.type + ']: ' + args);
    } else if (msg.method === 'Log.entryAdded') {
      events.push('LOG[' + msg.params.entry.level + ']: ' + msg.params.entry.text);
    }
  };
  ws.onopen = async () => {
    send(1, 'Runtime.enable');
    send(2, 'Log.enable');
    send(3, 'Page.enable');
    send(4, 'Page.navigate', { url });
    await sleep(waitMs);
    send(5, 'Runtime.evaluate', {
      expression: `JSON.stringify({
        title: document.title,
        authHidden: document.getElementById('auth-view')?.className,
        appHidden: document.getElementById('app-view')?.className,
        bootHidden: document.getElementById('boot')?.className,
        authError: document.getElementById('auth-error')?.textContent,
        bodyText: document.body.innerText.slice(0, 200)
      })`,
      returnByValue: true,
    });
    await sleep(1000);
    send(6, 'Runtime.evaluate', { expression: '1' });
  };
  let lastId = 0;
  ws.onmessage = ((orig) => (ev) => {
    const msg = JSON.parse(ev.data);
    if (msg.id === 5 && msg.result && msg.result.result) {
      console.log('STATE:', msg.result.result.value);
    }
    orig(ev);
  })(ws.onmessage);

  setTimeout(() => {
    console.log('EVENTS:\n' + (events.length ? events.join('\n') : '(none)'));
    chrome.kill();
    process.exit(0);
  }, waitMs + 3000);
})();
