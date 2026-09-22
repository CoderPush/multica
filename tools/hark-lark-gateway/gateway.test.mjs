import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { accept, acquireProcessLock, Gateway, Ledger, marker, PR_HOLD, replyUUID } from './gateway.mjs';

const config = { groups: ['oc_team'], dmUsers: ['ou_user'], botID: 'ou_bot' };
const event = (overrides = {}) => ({ sender: { sender_type: 'user', sender_id: { open_id: 'ou_user' } }, message: {
  message_id: 'om_one', chat_id: 'oc_team', chat_type: 'group', message_type: 'text', create_time: String(Date.now()),
  mentions: [{ key: '@_user_1', id: { open_id: 'ou_bot' } }], content: JSON.stringify({text:'@_user_1 Hello'}), ...overrides,
} });
test('scope, mention, human and freshness gates', () => {
  assert.equal(accept(event(), config).text, 'Hello');
  assert.equal(accept(event({chat_id:'oc_other'}), config), null);
});
// Always explicitly supply the installation policy.
test('ignores unrelated groups, bots, unmentioned text, unknown DMs and old events', () => {
  for (const patch of [{chat_id:'oc_other'}, {mentions:[]}, {chat_type:'p2p', create_time:'0'}]) assert.equal(accept(event(patch), config), null);
  const bot = event(); bot.sender.sender_type = 'app'; assert.equal(accept(bot, config), null);
  const stranger = event({chat_type:'p2p'}); stranger.sender.sender_id.open_id='ou_other'; assert.equal(accept(stranger, config), null);
  assert.ok(accept(event({chat_type:'p2p',mentions:[]}), config));
});
test('PR command and media have explicit terminal replies', () => {
  assert.equal(accept(event({content:'{"text":"@_user_1 merge 425"}'}), config).immediate, PR_HOLD);
  assert.equal(accept(event({chat_type:'p2p',mentions:[],content:'{"text":"  merge 425"}'}), config).immediate, PR_HOLD);
  assert.equal(accept(event({chat_type:'p2p',mentions:[],content:'{"text":"Could you please review PR #425?"}'}), config).immediate, PR_HOLD);
  assert.match(accept(event({message_type:'image'}), config).immediate, /supports text/);
});
test('conversation keys isolate senders and threads', () => {
  const a=accept(event(),config), b=event(); b.sender.sender_id.open_id='ou_second';
  assert.notEqual(a.route,accept(b,config).route);
  assert.notEqual(a.route,accept(event({root_id:'om_other'}),config).route);
});
function fixture(api) {
  const ledger=new Ledger(':memory:'); const replies=[];
  const gateway=new Gateway({ledger,api,agentID:'agent',reply:async (...args)=>{replies.push(args);return 'om_reply';}});
  const e=accept(event(),config); ledger.add(e);
  return {ledger,gateway,replies,e};
}
test('deduplicates inbound and uses matching task result only', async () => {
  const calls=[];
  const f=fixture(async(method,url,body)=>{calls.push({method,url,body});
    if(url==='/api/chat/sessions')return {id:'session'};
    if(method==='POST')return {task_id:'task'};
    return [{role:'assistant',task_id:'other',content:'Wrong thread'},{role:'assistant',task_id:'task',content:'Hello'}];
  });
  assert.equal(f.ledger.add(f.e),false);
  await f.gateway.tick(); await f.gateway.tick();
  assert.equal(f.replies.length,1); assert.equal(f.replies[0][1],'Hello');
  assert.match(calls[1].body.content,/not authenticated as Harley/);
  assert.equal(f.ledger.pending(),undefined); f.ledger.close();
});
test('uncertain submission reconciles instead of POSTing twice', async()=>{
  let posts=0;
  const f=fixture(async(method,url)=>{
    if(url==='/api/chat/sessions')return{id:'session'};
    if(method==='POST'){posts++;throw Error('timeout after acceptance');}
    return[{role:'user',task_id:'task',content:marker('om_one')+'\nHello'}, {role:'assistant',task_id:'task',content:'Recovered'}];
  });
  await f.gateway.tick(); assert.equal(f.ledger.pending().state,'submitting');
  await f.gateway.tick(); assert.equal(posts,1);assert.equal(f.replies[0][1],'Recovered');f.ledger.close();
});
test('runtime failure never leaks its raw error',async()=>{
  const f=fixture(async(method,url)=>url==='/api/chat/sessions'?{id:'session'}:method==='POST'?{task_id:'task'}:[{role:'assistant',task_id:'task',failure_reason:'agent_error',content:'secret-bearing diagnostic'}]);
  await f.gateway.tick();assert.match(f.replies[0][1],/runtime failed/);assert.doesNotMatch(f.replies[0][1],/secret/);f.ledger.close();
});
test('reply retries use a stable deduplication key',async()=>{
  const f=fixture(async()=>{throw Error('must not invoke Multica');});
  f.ledger.update(f.e.id,{state:'replying',response:'Ready'});
  await f.gateway.tick();assert.equal(f.replies[0][2],replyUUID(f.e.id));f.ledger.close();
});
test('PR hold never invokes Multica',async()=>{
  const f=fixture(async()=>{throw Error('must not invoke Multica');});
  f.ledger.db.prepare('UPDATE messages SET payload=?').run(JSON.stringify({...f.e,immediate:PR_HOLD}));
  await f.gateway.tick();assert.equal(f.replies[0][1],PR_HOLD);f.ledger.close();
});
test('a failing Lark reply does not starve another conversation', async()=>{
  const f=fixture(async()=>{throw Error('not needed');});
  f.ledger.update(f.e.id,{state:'replying',response:'First'});
  const second={...f.e,id:'om_two',route:'different',immediate:PR_HOLD}; f.ledger.add(second);
  const delivered=[];
  f.gateway.reply=async(id)=>{if(id==='om_one')throw Error('not deliverable');delivered.push(id);return 'om_receipt';};
  await f.gateway.tick();await f.gateway.tick();
  assert.deepEqual(delivered,['om_two']);f.ledger.close();
});
test('model tasks serialize but immediate holds do not wait for them', async()=>{
  const f=fixture(async(method,url)=>url==='/api/chat/sessions'?{id:'session'}:method==='POST'?{task_id:'task'}:[]);
  await f.gateway.tick();
  f.ledger.add({...f.e,id:'om_two',immediate:PR_HOLD});
  await f.gateway.tick();assert.equal(f.replies[0][0],'om_two');
  assert.equal(f.ledger.pending().state,'waiting');f.ledger.close();
});
test('queue age does not immediately time out a newly submitted model task',async()=>{
  const f=fixture(async(method,url)=>url==='/api/chat/sessions'?{id:'session'}:method==='POST'?{task_id:'task'}:[]);
  f.ledger.db.prepare('UPDATE messages SET created=?,updated=?').run(Date.now()-660_000,Date.now()-660_000);
  await f.gateway.tick();assert.equal(f.ledger.pending().state,'waiting');assert.equal(f.replies.length,0);f.ledger.close();
});
test('a transient read failure after new submission does not inherit queue age',async()=>{
  const f=fixture(async(method,url)=>{if(url==='/api/chat/sessions')return{id:'session'};if(method==='POST')return{task_id:'task'};throw Error('temporary read failure');});
  f.ledger.db.prepare('UPDATE messages SET created=?,updated=?').run(Date.now()-660_000,Date.now()-660_000);
  await f.gateway.tick();assert.equal(f.ledger.pending().state,'waiting');assert.equal(f.replies.length,0);f.ledger.close();
});
test('unconfirmed submission reports uncertainty without a second POST',async()=>{
  const f=fixture(async(method)=>{assert.equal(method,'GET');return[];});
  f.ledger.update(f.e.id,{state:'submitting',session:'session'});
  f.ledger.db.prepare('UPDATE messages SET updated=?').run(Date.now()-61_000);
  await f.gateway.tick();assert.match(f.replies[0][1],/could not confirm/);f.ledger.close();
});
test('restart after lost reply receipt retains the same outbox and UUID',async()=>{
  const dir=fs.mkdtempSync(path.join(os.tmpdir(),'hark-test-'));const file=path.join(dir,'state.sqlite');
  let ledger=new Ledger(file);const e=accept(event(),config);ledger.add(e);ledger.update(e.id,{state:'reply_ready',response:'Reply'});
  const receipts=[];let gateway=new Gateway({ledger,api:async()=>{throw Error('must not run');},reply:async(...args)=>{receipts.push(args);throw Error('receipt lost');}});
  await gateway.tick();assert.equal(ledger.pending().state,'replying');ledger.close();
  ledger=new Ledger(file);gateway=new Gateway({ledger,api:async()=>{throw Error('must not run');},reply:async(...args)=>{receipts.push(args);return 'om_reply';}});
  await gateway.tick();assert.deepEqual(receipts[0],receipts[1]);assert.equal(ledger.pending(),undefined);ledger.close();fs.rmSync(dir,{recursive:true});
});
test('reply uncertainty beyond the dedup window is held, never resent',async()=>{
  const f=fixture(async()=>{throw Error('must not run');});f.ledger.update(f.e.id,{state:'replying',response:'Reply'});
  f.ledger.db.prepare('UPDATE messages SET updated=?').run(Date.now()-3_100_000);
  await f.gateway.tick();assert.equal(f.replies.length,0);assert.equal(f.ledger.pending(),undefined);
  assert.equal(f.ledger.db.prepare('SELECT state FROM messages').get().state,'held');f.ledger.close();
});
test('process lock excludes a second process and recovers after SIGKILL',async()=>{
  const dir=fs.mkdtempSync(path.join(os.tmpdir(),'hark-lock-'));const file=path.join(dir,'lock.sqlite');
  const script=`import { acquireProcessLock } from ${JSON.stringify(new URL('./gateway.mjs',import.meta.url).href)};const lock=acquireProcessLock(${JSON.stringify(file)});console.log('locked');setInterval(()=>{},1000);`;
  const child=spawn(process.execPath,['--input-type=module','-e',script],{stdio:['ignore','pipe','pipe']});
  try {
    await once(child.stdout,'data');assert.throws(()=>acquireProcessLock(file),/unavailable/);
    child.kill('SIGKILL');await once(child,'exit');
    const lock=acquireProcessLock(file);lock.close();
  } finally {if(child.exitCode===null)child.kill('SIGKILL');fs.rmSync(dir,{recursive:true});}
});
