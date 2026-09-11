const port = Number(process.argv[2] || 9227);
const candidates = [
  'canOpenCommunity',
  'getApplicationIcon',
  'getAutoUpdatePreferences',
  'getCuaGrayEnabled',
  'getDesktopSessionActivity',
  'getDesktopWindowChromeState',
  'getDesktopZoomLevel',
  'getDeviceId',
  'getInstalledEditors',
  'getRendererActionTraceConfig',
  'getSystemLocale',
  'getUpdateState',
  'getWebRemoteControlStatus',
  'getWindowControlsOverlayMetrics',
  'getZCodeStdioTapDevState',
  'isDockerAvailable',
  'listDockerContainers',
  'listWSLDistros'
];

function summarize(value, depth = 0) {
  if (value === null) return { kind: 'null' };
  if (Array.isArray(value)) {
    return {
      kind: 'array',
      length: value.length,
      items: depth < 2 ? value.slice(0, 3).map(item => summarize(item, depth + 1)) : undefined
    };
  }
  const kind = typeof value;
  if (kind === 'string') return { kind, length: value.length };
  if (kind === 'number' || kind === 'boolean') return { kind, value };
  if (kind === 'undefined') return { kind };
  if (depth >= 3) return { kind: 'object', truncated: true };
  const keys = Object.keys(value).sort();
  const fields = {};
  for (const key of keys) fields[key] = summarize(value[key], depth + 1);
  return { kind: 'object', keys, fields };
}

async function rpc(ws, id, method, params = {}, sessionId) {
  ws.send(JSON.stringify({ id, method, params, sessionId }));
  return await new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(`timeout: ${method}`)), 15000);
    const onMessage = event => {
      const message = JSON.parse(event.data);
      if (message.id !== id) return;
      clearTimeout(timer);
      ws.removeEventListener('message', onMessage);
      if (message.error) reject(new Error(`${message.error.code}: ${message.error.message}`));
      else resolve(message.result);
    };
    ws.addEventListener('message', onMessage);
  });
}

async function evaluate(ws, id, expression) {
  const result = await rpc(ws, id, 'Runtime.evaluate', {
    expression,
    awaitPromise: true,
    returnByValue: true,
    userGesture: false
  });
  if (result.exceptionDetails) {
    const error = result.exceptionDetails.exception || {};
    const e = new Error(error.description || result.exceptionDetails.text || 'evaluation failed');
    e.name = error.className || result.exceptionDetails.text || 'Error';
    throw e;
  }
  return result.result.value;
}

const targets = await (await fetch(`http://127.0.0.1:${port}/json`)).json();
const page = targets.find(t => t.type === 'page' && t.url.includes('/index.html'))
  || targets.find(t => t.type === 'page' && t.url.startsWith('file://'))
  || targets.find(t => t.type === 'page');
if (!page) throw new Error('No ZCode page target found');

const ws = new WebSocket(page.webSocketDebuggerUrl);
await new Promise((resolve, reject) => {
  ws.addEventListener('open', resolve, { once: true });
  ws.addEventListener('error', reject, { once: true });
});

try {
  const bridge = await evaluate(ws, 1, `(() => ({ has: Boolean(window.zcode), url: location.href, methods: window.zcode ? Object.keys(window.zcode).length : 0 }))()`);
  if (!bridge.has) throw new Error('window.zcode is unavailable');
  let id = 10;
  const output = [];
  for (const name of candidates) {
    const expression = `(async () => {
  const fn = window.zcode[${JSON.stringify(name)}];
  if (typeof fn !== 'function') return { missing: true };
  return await fn.call(window.zcode);
})()`;
    const calls = [];
    for (let i = 0; i < 2; i++) {
      try {
        calls.push({ ok: true, shape: summarize(await evaluate(ws, ++id, expression)) });
      } catch (error) {
        calls.push({ ok: false, name: error.name, message: error.message.split('\n')[0] });
      }
    }
    output.push({ name, calls });
  }
  console.log(JSON.stringify({ bridge: { url: bridge.url, methodCount: bridge.methods }, results: output }, null, 2));
} finally {
  ws.close();
}
