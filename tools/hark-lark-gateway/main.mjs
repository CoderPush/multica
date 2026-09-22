import fs from 'node:fs';
import path from 'node:path';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import * as lark from '@larksuiteoapi/node-sdk';
import { accept, acquireProcessLock, Gateway, Ledger } from './gateway.mjs';
import { Observer, ALERTS, REPO } from './observer.mjs';
import { releaseContext } from './releases.mjs';

const fatal = () => { console.error('Hark gateway stopped; operator inspection required.'); process.exit(1); };
process.on('uncaughtException', fatal);
process.on('unhandledRejection', fatal);
process.umask(0o077);
const configPath = process.env.HARK_GATEWAY_CONFIG;
if (!configPath) throw Error('HARK_GATEWAY_CONFIG is required');
if (fs.statSync(configPath).mode & 0o077) throw Error('config must be private');
const config = JSON.parse(fs.readFileSync(configPath, 'utf8'));
if (config.serverURL !== 'https://multica.coderbase.dev') throw Error('unexpected destination');
if (!config.groups?.length || !config.agentID || !config.workspaceID) throw Error('incomplete configuration');
fs.mkdirSync(config.stateDir, { recursive: true, mode: 0o700 });
const lock = acquireProcessLock(path.join(config.stateDir, 'process-lock.sqlite'));
const log = (event, fields = {}) => console.log(JSON.stringify({ time: new Date().toISOString(), event, ...fields }));
const safeLogger = Object.fromEntries(['debug','trace','info','warn','error'].map(level => [level, () => {
  if (level === 'error' || level === 'warn') log(`lark_${level}`);
}]));
// Bound actual HTTP requests, including tenant token acquisition and replies.
lark.defaultHttpInstance.defaults.timeout = 25_000;
const base = { appId: config.appID, appSecret: config.appSecret, domain: lark.Domain.Lark, logger: safeLogger };
const client = new lark.Client(base);
const ledger = new Ledger(path.join(config.stateDir, 'messages.sqlite'));
const api = async (method, endpoint, body) => {
  const auth = JSON.parse(fs.readFileSync(config.multicaConfig, 'utf8'));
  if (auth.server_url !== config.serverURL || !auth.token) throw Error('Multica authentication unavailable');
  const result = await fetch(config.serverURL + endpoint, {
    method, redirect: 'error', signal: AbortSignal.timeout(25_000),
    headers: { Authorization: `Bearer ${auth.token}`, 'X-Workspace-ID': config.workspaceID, 'Content-Type': 'application/json' },
    ...(body ? { body: JSON.stringify(body) } : {}),
  });
  if (!result.ok) throw Error(`Multica HTTP ${result.status}`);
  return result.json();
};
const reply = async (messageID, content, uuid) => {
  const result = await client.im.v1.message.reply({
    path: { message_id: messageID },
    data: { msg_type: 'text', content: JSON.stringify({ text: content }), reply_in_thread: true, uuid },
  });
  if (result.code !== 0) throw Error('Lark reply failed');
  return result.data?.message_id;
};
const gateway = new Gateway({ ledger, api, reply, agentID: config.agentID, log,
  observerContext: () => observer?.context() ?? releaseContext(path.join(config.stateDir,'releases.sqlite')) });
const run = promisify(execFile);
const observer = config.releaseObserver === true ? new Observer({
  file: path.join(config.stateDir, 'observer.sqlite'), log,
  github: async endpoint => {
    const { stdout } = await run('/Users/hogan/.local/bin/gh', ['api', '--method', 'GET', `repos/${REPO}/${endpoint}`],
      { timeout: 25_000, maxBuffer: 4 * 1024 * 1024, env: { ...process.env, GH_HOST: 'github.com', GH_PROMPT_DISABLED: '1' } });
    return JSON.parse(stdout);
  },
  read: async url => {
    for (let redirects = 0; redirects < 4; redirects++) {
      const u = new URL(url);
      if (u.protocol !== 'https:' || !['eovietnam.org','www.eovietnam.org'].includes(u.host)) throw Error('Unexpected page host');
      const response = await fetch(u, { redirect: 'manual', signal: AbortSignal.timeout(20_000) });
      if (response.status >= 300 && response.status < 400 && response.headers.get('location')) {
        url = new URL(response.headers.get('location'), u).href;
        await response.body?.cancel(); continue;
      }
      if (response.status !== 200 || !response.headers.get('content-type')?.includes('text/html')) {
        await response.body?.cancel(); throw Error('Unexpected page response');
      }
      let body = ''; let bytes = 0;
      for await (const chunk of response.body) {
        bytes += chunk.length;
        if (bytes > 3 * 1024 * 1024) throw Error('Page too large');
        body += Buffer.from(chunk).toString('utf8');
      }
      return body;
    }
    throw Error('Too many redirects');
  },
  send: async (text, uuid) => {
    const result = await client.im.v1.message.create({ params: { receive_id_type: 'chat_id' },
      data: { receive_id: ALERTS, msg_type: 'text', content: JSON.stringify({ text }), uuid } });
    if (result.code !== 0) throw Error('Lark alert failed');
    return result.data?.message_id;
  },
}) : null;
if (process.argv.includes('--check-observer')) {
  if (!observer) throw Error('Observer is disabled');
  await observer.observe();
  console.log(JSON.stringify(observer.state()));
  process.exit(0);
}
const ws = new lark.WSClient(base);
await ws.start({ eventDispatcher: new lark.EventDispatcher({ logger: safeLogger }).register({
  'im.message.receive_v1': async event => {
    const accepted = accept(event, config);
    if (accepted && ledger.add(accepted)) log('accepted', { id: accepted.id });
  },
}) });
log('started', { agent: config.agentID, workspace: config.workspaceID });
const timer = setInterval(() => gateway.tick(), 2000);
const observerTimer = observer ? setInterval(() => observer.tick(), 5 * 60_000) : null;
if (observer) void observer.tick();
for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, () => {
  clearInterval(timer);
  clearInterval(observerTimer);
  // In-flight submissions/replies remain fenced in the durable ledger.
  lock.close();
  process.exit(0);
});
