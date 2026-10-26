import { createServer } from 'node:http';

createServer((_request, response) => {
  response.setHeader('content-type', 'text/html; charset=utf-8');
  response.end(`<!doctype html><html lang="en"><meta charset="utf-8">
    <title>Native browser recipe</title><h1>Counter</h1>
    <button type="button" onclick="this.textContent = Number(this.textContent) + 1">0</button>
    </html>`);
}).listen(Number(process.env.PORT), '127.0.0.1');
