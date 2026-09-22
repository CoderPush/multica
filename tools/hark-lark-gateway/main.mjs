import fs from 'node:fs';
import path from 'node:path';
import * as lark from '@larksuiteoapi/node-sdk';
import { accept, acquireProcessLock, Gateway, Ledger } from './gateway.mjs';

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
const gateway = new Gateway({ ledger, api, reply, agentID: config.agentID, log });
const ws = new lark.WSClient(base);
await ws.start({ eventDispatcher: new lark.EventDispatcher({ logger: safeLogger }).register({
  'im.message.receive_v1': async event => {
    const accepted = accept(event, config);
    if (accepted && ledger.add(accepted)) log('accepted', { id: accepted.id });
  },
}) });
log('started', { agent: config.agentID, workspace: config.workspaceID });
const timer = setInterval(() => gateway.tick(), 2000);
for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, () => {
  clearInterval(timer);
  // In-flight submissions/replies remain fenced in the durable ledger.
  lock.close();
  process.exit(0);
});
