/**
 * Local server + SSE. Binds to 127.0.0.1 only - this exposes repository
 * contents (commit messages, file paths, branch names), so it must never be
 * reachable off the machine.
 */

import { createServer } from 'node:http';
import { renderHtml } from './render-html.mjs';

export function startServer({ port = 0, onListen = () => {} } = {}) {
  let current = null;
  const clients = new Set();

  const server = createServer((req, res) => {
    const url = (req.url || '/').split('?')[0];

    if (url === '/events') {
      res.writeHead(200, {
        'content-type': 'text/event-stream',
        'cache-control': 'no-cache, no-transform',
        connection: 'keep-alive',
        'x-accel-buffering': 'no',
      });
      res.write('retry: 1000\n\n');
      clients.add(res);
      if (current) res.write(sse('model', current));
      const ping = setInterval(() => { try { res.write(': ping\n\n'); } catch { /* closed */ } }, 25000);
      req.on('close', () => { clearInterval(ping); clients.delete(res); });
      return;
    }

    if (url === '/' || url === '/index.html') {
      if (!current) {
        // A cold build on a large repo takes ~12s, and --app opens the window
        // immediately. Serve a page that retries itself rather than a dead 503.
        res.writeHead(503, { 'content-type': 'text/html; charset=utf-8', 'cache-control': 'no-store' });
        return res.end('<!doctype html><meta charset="utf-8"><title>landfirst</title>' +
          '<style>body{background:#181c24;color:#7b869a;font:13px ui-monospace,monospace;' +
          'display:grid;place-items:center;height:100vh;margin:0}</style>' +
          '<div>reading the repository&hellip;</div>' +
          '<script>setTimeout(function(){location.reload();},700)<\/script>');
      }
      const body = renderHtml(current, { live: true });
      res.writeHead(200, { 'content-type': 'text/html; charset=utf-8', 'cache-control': 'no-store' });
      return res.end(body);
    }

    res.writeHead(404, { 'content-type': 'text/plain' });
    res.end('not found');
  });

  server.on('error', (err) => {
    console.error(`landfirst: server error: ${err.message}`);
    process.exit(1);
  });

  server.listen(port, '127.0.0.1', () => onListen(server.address().port));

  return {
    push(model) {
      current = model;
      const frame = sse('model', model);
      for (const res of clients) {
        try { res.write(frame); } catch { clients.delete(res); }
      }
    },
    get clientCount() { return clients.size; },
    close() {
      for (const res of clients) { try { res.end(); } catch { /* closed */ } }
      server.close();
    },
  };
}

function sse(event, payload) {
  // A newline inside the JSON would split the SSE frame; JSON.stringify escapes
  // them, but guard anyway since one bad frame kills the stream.
  const data = JSON.stringify(payload).replace(/[\r\n]/g, ' ');
  return `event: ${event}\ndata: ${data}\n\n`;
}
