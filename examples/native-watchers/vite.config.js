const origin = new URL(process.env.APP_ORIGIN);
export default {
  server: {
    host: '127.0.0.1', port: Number(process.env.PORT), strictPort: true,
    allowedHosts: [origin.hostname],
    hmr: { protocol: 'wss', host: origin.hostname, clientPort: Number(origin.port) }
  }
};
