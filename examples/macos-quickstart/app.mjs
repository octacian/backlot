import http from 'node:http';
import { readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import https from 'node:https';
import { WebSocketServer } from 'ws';

// SSR deliberately uses the canonical HTTPS origin, not an internal API port.
// Node reads the manifest's explicit NODE_EXTRA_CA_CERTS at process startup.
export function get(path, headers = {}) {
  return new Promise((resolve, reject) => {
    const request = https.get(new URL(path, process.env.APP_ORIGIN), { headers }, response => {
      const chunks = [];
      response.on('data', chunk => chunks.push(chunk));
      response.on('end', () => resolve({ status: response.statusCode, headers: response.headers,
        body: Buffer.concat(chunks).toString() }));
      response.on('error', reject);
    });
    request.setTimeout(5000, () => request.destroy(new Error('HTTPS request timed out')));
    request.on('error', reject);
  });
}

function page(value) {
  return `<!doctype html><html lang="en"><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Backlot quickstart</title><h1>Backlot HTTPS quickstart</h1>
<p id="ssr">Native SSR through the canonical origin: ${value}</p>
<p><a href="/api/value">API</a> · <a href="/api/redirect">Redirect and secure cookie</a></p>
<button id="echo">Send WebSocket message</button><p id="result" role="status"></p>
<script>
const result = document.querySelector('#result');
document.querySelector('#echo').onclick = () => {
  const socket = new WebSocket(location.origin.replace('https:', 'wss:') + '/api/ws');
  const payload = 'browser-' + crypto.randomUUID();
  socket.onopen = () => socket.send(payload);
  socket.onmessage = event => {
    result.textContent = event.data === payload ? 'WebSocket echo: ' + event.data : 'Incorrect echo';
    socket.close();
  };
  socket.onerror = () => { result.textContent = 'WebSocket connection failed'; };
};
</script></html>`;
}

if (process.argv[2] === 'seed') {
  writeFileSync(join(process.env.APP_DATA, 'account.json'), JSON.stringify({
    email: process.env.LOGIN_EMAIL, password: process.env.LOGIN_PASSWORD
  }), { mode: 0o600 });
  console.log('application account seeded');
}

if (process.argv[2] === 'api' || process.argv[2] === 'web') {
  const role = process.argv[2];
  const server = http.createServer(async (request, response) => {
    try {
      if (request.url === '/health') return response.end('ready');
      if (role === 'api') {
        if (request.url === '/login' && request.method === 'POST') {
          let body = '';
          for await (const chunk of request) {
            body += chunk;
            if (Buffer.byteLength(body) > 1024) { response.writeHead(413).end(); return; }
          }
          let supplied;
          try { supplied = JSON.parse(body); }
          catch { response.writeHead(400).end(); return; }
          const account = JSON.parse(readFileSync(join(process.env.APP_DATA, 'account.json'), 'utf8'));
          const authenticated = supplied?.email === account.email && supplied?.password === account.password;
          response.writeHead(authenticated ? 200 : 401, { 'Content-Type': 'application/json' });
          return response.end(JSON.stringify({ authenticated }));
        }
        if (request.url === '/value') {
          response.setHeader('Content-Type', 'application/json');
          return response.end(JSON.stringify({ value: 'hello from API', origin: process.env.APP_ORIGIN,
            cookie: request.headers.cookie ?? '' }));
        }
        if (request.url === '/redirect') {
          response.writeHead(302, { Location: new URL('/api/value', process.env.APP_ORIGIN).href,
            'Set-Cookie': 'quickstart=hello; Path=/api; Secure; HttpOnly; SameSite=Lax' });
          return response.end();
        }
      } else if (request.url === '/') {
        const api = await get('/api/value');
        if (api.status !== 200) throw new Error(`API returned ${api.status}`);
        response.setHeader('Content-Type', 'text/html; charset=utf-8');
        return response.end(page(JSON.parse(api.body).value));
      }
      response.writeHead(404).end('not found');
    } catch (error) {
      console.error(error.message);
      response.writeHead(502).end(`SSR failed: ${error.message}`);
    }
  });
  if (role === 'api') {
    const sockets = new WebSocketServer({ server, path: '/ws', maxPayload: 1024 * 1024 });
    sockets.on('connection', socket => socket.on('message', (payload, binary) => {
      socket.send(payload, { binary });
    }));
  }
  server.listen(Number(process.env.PORT), '127.0.0.1', () => console.log(`${role} ready`));
}
