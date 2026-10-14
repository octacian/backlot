import assert from 'node:assert/strict';
import { randomBytes } from 'node:crypto';
import { mkdir, writeFile } from 'node:fs/promises';
import WebSocket from 'ws';
import { get } from './app.mjs';

const checks = [];
try {
  const home = await get('/');
  assert.equal(home.status, 200);
  assert.match(home.body, /Native SSR through the canonical origin: hello from API/);
  checks.push('frontend and native SSR');
  const api = await get('/api/value');
  assert.equal(api.status, 200);
  assert.equal(JSON.parse(api.body).origin, process.env.APP_ORIGIN);
  assert.equal((await get('/apix')).status, 404);
  checks.push('same-origin API and literal path routing');
  const redirect = await get('/api/redirect');
  assert.equal(redirect.status, 302);
  assert.equal(redirect.headers.location, new URL('/api/value', process.env.APP_ORIGIN).href);
  const cookie = redirect.headers['set-cookie'][0];
  for (const attribute of ['Secure', 'HttpOnly', 'SameSite=Lax', 'Path=/api']) assert.ok(cookie.includes(attribute));
  const followed = await get('/api/value', { Cookie: cookie.split(';')[0] });
  assert.equal(JSON.parse(followed.body).cookie, 'quickstart=hello');
  checks.push('canonical redirect and secure cookie round trip');
  await new Promise((resolve, reject) => {
    const socket = new WebSocket(new URL('/api/ws', process.env.APP_ORIGIN.replace('https:', 'wss:')));
    const payloads = [Buffer.from('small-' + randomBytes(12).toString('hex')), randomBytes(32768)];
    let index = 0;
    const timer = setTimeout(() => { socket.terminate(); reject(new Error('WebSocket timeout')); }, 5000);
    socket.on('error', error => { clearTimeout(timer); reject(error); });
    socket.on('open', () => socket.send(payloads[index], { binary: false }));
    socket.on('message', (data, binary) => {
      try {
        assert.deepEqual(data, payloads[index]);
        assert.equal(binary, index !== 0);
        index++;
        if (index === payloads.length) { clearTimeout(timer); socket.close(); resolve(); }
        else socket.send(payloads[index], { binary: true });
      } catch (error) { clearTimeout(timer); socket.terminate(); reject(error); }
    });
  });
  checks.push('variable text and binary WebSocket echoes');
  if (process.argv.includes('--fail')) throw new Error('requested failure for retained evidence');
  console.log(checks.join('\n'));
} catch (error) {
  console.error(error.message);
  process.exitCode = 1;
} finally {
  await mkdir('reports', { recursive: true });
  await writeFile('reports/result.json', JSON.stringify({ origin: process.env.APP_ORIGIN,
    checks, passed: !process.exitCode }, null, 2) + '\n');
}
