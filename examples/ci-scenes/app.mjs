import http from 'node:http';
import https from 'node:https';
import { randomUUID } from 'node:crypto';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';

const data = process.env.APP_DATA;
await mkdir(data, { recursive: true });
const stack = randomUUID();
await writeFile(join(data, 'stack.json'), JSON.stringify({ stack }));
const page = `<!doctype html><html lang="en"><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Backlot CI scene</title><style>
body{font:18px system-ui;max-width:40rem;margin:2rem auto;padding:0 1rem}
input,button{font:inherit;max-width:100%;box-sizing:border-box} form{display:grid;gap:1rem}
</style><h1>Job-local HTTPS notes</h1><p id="ssr">STACK</p>
<form><label>Note <input name="note" required maxlength="80"></label>
<button>Save note</button></form><p role="status">Ready</p>
<script>
const status = document.querySelector('[role=status]');
document.querySelector('form').onsubmit = async event => {
  event.preventDefault(); status.textContent = 'Saving…';
  try {
    const response = await fetch('/api/notes/' + new URLSearchParams(location.search).get('worker'), {
      method:'PUT', body:document.querySelector('input').value
    });
    if (!response.ok) throw new Error('Save failed');
    status.textContent = 'Saved: ' + await response.text();
  } catch { status.textContent = 'Save failed. Try again.'; }
};
</script></html>`;

// Ordinary Node HTTPS, with explicit NODE_EXTRA_CA_CERTS from the manifest.
function canonicalStack() {
  return new Promise((resolve, reject) => {
    const req = https.get(new URL('/api/stack', process.env.APP_ORIGIN), res => {
      let body = '';
      res.on('data', chunk => { body += chunk; });
      res.on('end', () => res.statusCode === 200 ? resolve(JSON.parse(body).stack) : reject(new Error('SSR failed')));
      res.on('error', reject);
    });
    req.setTimeout(5000, () => req.destroy(new Error('SSR timeout')));
    req.on('error', reject);
  });
}

const server = http.createServer(async (req, res) => {
  try {
    const url = new URL(req.url, 'http://localhost');
    if (url.pathname === '/health') return res.end('ready');
    if (url.pathname === '/api/stack') {
      res.setHeader('Content-Type', 'application/json');
      return res.end(await readFile(join(data, 'stack.json')));
    }
    const note = /^\/api\/notes\/([a-zA-Z0-9-]{1,100})$/.exec(url.pathname);
    if (note && req.method === 'PUT') {
      let value = '';
      for await (const chunk of req) {
        value += chunk;
        if (value.length > 80) return res.writeHead(413).end();
      }
      if (value === 'reject-save') return res.writeHead(503).end('requested application error');
      await writeFile(join(data, note[1] + '.txt'), value);
      console.log('note saved', note[1]);
      return res.end(value);
    }
    if (note && req.method === 'GET') {
      try { return res.end(await readFile(join(data, note[1] + '.txt'))); }
      catch (error) { if (error.code === 'ENOENT') return res.writeHead(404).end('absent'); throw error; }
    }
    if (url.pathname === '/') {
      const value = await canonicalStack();
      res.setHeader('Content-Type', 'text/html; charset=utf-8');
      return res.end(page.replace('STACK', 'Native SSR stack: ' + value));
    }
    res.writeHead(404).end('not found');
  } catch (error) {
    console.error(error.message);
    res.writeHead(502).end('Application request failed');
  }
});
server.listen(Number(process.env.PORT), '127.0.0.1', () => console.log('application ready', stack));
