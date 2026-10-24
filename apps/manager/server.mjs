import { createServer } from 'node:http';

// adapter-node 6 derives its origin from trusted headers instead of runtime ORIGIN.
// Set private headers ourselves so a client cannot choose the protocol or host.
const configured = process.env.ORIGIN?.replace(/\/$/, '');
const origin = configured ? new URL(configured) : undefined;
if (origin && (!['http:', 'https:'].includes(origin.protocol) || origin.origin !== configured)) {
  throw new Error('ORIGIN must be an HTTP(S) origin without a path or credentials');
}
process.env.NODE_ENV ??= 'production';
process.env.BODY_SIZE_LIMIT ??= '32M';
process.env.PROTOCOL_HEADER = 'x-memstation-origin-protocol';
process.env.HOST_HEADER = 'x-memstation-origin-host';
process.env.PORT_HEADER = '';
const { handler } = await import('./build/handler.js');
const server = createServer((request, response) => {
  request.headers['x-memstation-origin-protocol'] = origin?.protocol.slice(0, -1) ?? 'https';
  request.headers['x-memstation-origin-host'] = origin?.host ?? request.headers.host;
  Promise.resolve(handler(request, response, () => {
    response.statusCode = 404;
    response.end();
  })).catch(() => {
    if (!response.headersSent) response.writeHead(500);
    response.end();
  });
});
server.requestTimeout = 120_000;
server.headersTimeout = 30_000;
const host = process.env.HOST ?? '0.0.0.0';
const port = Number(process.env.PORT ?? 3000);
server.listen(port, host, () => console.log(`Manager listening on ${host}:${port}`));
let stopping = false;
function shutdown() {
  if (stopping) return;
  stopping = true;
  const timeout = setTimeout(() => server.closeAllConnections(), 30_000);
  timeout.unref();
  server.close(() => {
    clearTimeout(timeout);
    process.emit('sveltekit:shutdown');
  });
  server.closeIdleConnections();
}
process.on('SIGTERM', shutdown);
process.on('SIGINT', shutdown);
