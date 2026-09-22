import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { Observer, publicPaths, checkPages } from './observer.mjs';

function fixture() {
  const f = { id: 1, sha: 'a'.repeat(40), status: 'success', sent: [], files: [], bad: false, now: 1000 };
  f.o = new Observer({ file: ':memory:', now: () => f.now,
    github: async p => p.startsWith('compare/') ? { files:f.files } : p.endsWith('/statuses') ? [{state:f.status}]
      : [{ id:f.id, sha:f.sha, creator:{login:'vercel[bot]'}, environment:'Production' }],
    read: async () => { if(f.bad) throw Error('secret error'); return '<html><title>EO Vietnam</title></html>'; },
    send: async (body,id) => { f.sent.push({body,id}); return 'om_receipt'; },
  });
  return f;
}
test('public paths exclude private, dynamic and malicious routes', () => {
  assert.deepEqual(publicPaths(['src/app/post/recap/page.tsx','src/app/admin/page.tsx','src/app/board/x/page.tsx',
    'src/app/member/page.tsx','src/app/t/x/page.tsx','src/app/events/[id]/page.tsx', 'src/app/../../page.tsx']
    .map(filename => ({filename}))), ['/post/recap']);
});
test('article discovery requires an actual href, not a script mention', async () => {
  const result = await checkPages(['/post/recap'], async url => url.endsWith('/blog') ? '<html>/post/recap</html>' : '<html></html>');
  assert.equal(result.failures.length,1);
});
test('healthy baseline and repeated polls are quiet', async () => {
  const f=fixture(); await f.o.tick(); await f.o.tick();
  assert.equal(f.sent.length,0); assert.equal(f.o.state().checked.length,3);
});
test('failure alerts once, no private error text, then recovery once', async () => {
  const f=fixture(); await f.o.tick(); f.bad=true; await f.o.tick(); await f.o.tick();
  assert.equal(f.sent.length,1); assert.ok(!f.sent[0].body.includes('secret error'));
  f.bad=false; await f.o.tick(); await f.o.tick();
  assert.equal(f.sent.length,2); assert.match(f.sent[1].body,/recovered/);
});
test('pending deployment preserves unresolved problem until success', async () => {
  const f=fixture(); f.status='failure'; await f.o.tick();
  f.id=2; f.status='pending'; await f.o.tick(); assert.equal(f.sent.length,1);
  f.status='success'; await f.o.tick(); assert.equal(f.sent.length,2);
  assert.match(f.sent[1].body,/recovered/);
});
test('new production diff checks added public article and its listing', async () => {
  const f=fixture(); await f.o.tick(); f.id=2; f.sha='b'.repeat(40);
  f.files=[{filename:'src/app/post/recap/page.tsx'}]; await f.o.tick(); await f.o.tick();
  assert.ok(f.o.state().paths.includes('/post/recap')); assert.equal(f.sent.length,1);
});
test('uncertain delivery retries identical UUID, holds beyond dedup window', async () => {
  const f=fixture(); const ids=[]; f.o.send=async (_,id)=>{ids.push(id);throw Error('uncertain');};
  f.status='failure'; await f.o.tick(); await f.o.tick();
  assert.equal(ids.length,2); assert.equal(ids[0],ids[1]);
  f.now+=50*60_000; await f.o.tick(); assert.equal(ids.length,2);
  assert.equal(f.o.db.prepare('SELECT held FROM observer_outbox').get().held,1);
});
test('source failure does not overwrite fresh evidence or block pending delivery', async () => {
  const f=fixture(); f.status='failure'; f.o.send=async()=>{throw Error('offline');}; await f.o.tick();
  const before=f.o.state(); f.o.github=async()=>{throw Error('no access');};
  f.o.send=async(body,id)=>{f.sent.push({body,id});return 'om_receipt';}; await f.o.tick();
  assert.deepEqual(f.o.state(),before); assert.equal(f.sent.length,1);
});
test('removed pages are not reported as missing content', () => {
  assert.deepEqual(publicPaths([{filename:'src/app/post/old/page.tsx',status:'removed'}]),[]);
});
test('a failed release does not become the comparison base for the next release', async () => {
  const f=fixture(); const calls=[];const github=f.o.github;
  f.o.github=async p=>{calls.push(p);return github(p);};
  await f.o.tick(); f.id=2;f.sha='b'.repeat(40);f.status='failure';await f.o.tick();
  f.id=3;f.sha='c'.repeat(40);f.status='success';await f.o.tick();
  assert.ok(calls.includes(`compare/${'a'.repeat(40)}...${'c'.repeat(40)}`));
});
test('stale context is classified by the controller, not inferred by the model', async () => {
  const f=fixture();await f.o.tick();assert.match(f.o.context(),/freshness: recent/);
  f.now+=16*60_000;assert.match(f.o.context(),/freshness: STALE/);
});
test('a transient HTTP failure does not alert or report recovery', async () => {
  const f=fixture();await f.o.tick();f.bad=true;await f.o.tick();f.bad=false;await f.o.tick();
  assert.equal(f.sent.length,0);
});
test('unresolved article is rechecked after an unrelated deployment', async () => {
  const f=fixture();await f.o.tick();f.id=2;f.sha='b'.repeat(40);
  f.files=[{filename:'src/app/post/recap/page.tsx'}];await f.o.tick();await f.o.tick();
  f.id=3;f.sha='c'.repeat(40);f.files=[];await f.o.tick();
  assert.ok(f.o.state().paths.includes('/post/recap'));
  assert.ok(f.o.state().problems.length);assert.ok(f.sent.every(x=>!x.body.includes('recovered')));
});
test('old unattempted rows remain deliverable', async () => {
  const f=fixture();f.o.db.prepare('INSERT INTO observer_outbox(id,body,created) VALUES(?,?,?)').run('a','test',0);
  f.now=60*60_000;await f.o.deliver();assert.equal(f.sent.length,1);
});
test('corrupt observer context cannot throw into chat', () => {
  const f=fixture();f.o.db.prepare('INSERT INTO observer_state VALUES(1,?)').run('bad JSON');
  assert.match(f.o.context(),/unavailable/);
});
test('outbox and first attempt survive a real database reopen', async () => {
  const dir=fs.mkdtempSync(path.join(os.tmpdir(),'hark-observer-'));
  try {
    const file=path.join(dir,'state.sqlite');
    const opts={file,github:async()=>{throw Error('offline');},read:async()=>'',now:()=>1000};
    const a=new Observer({...opts,send:async()=>{throw Error('uncertain');}});
    a.db.prepare('INSERT INTO observer_outbox(id,body,created) VALUES(?,?,?)').run('stable','test',100);
    await a.deliver().catch(()=>{});a.db.close();
    let uuid;const b=new Observer({...opts,send:async(_,id)=>{uuid=id;return 'om_receipt';}});
    await b.deliver();assert.equal(uuid,'stable');
    assert.equal(b.db.prepare('SELECT first_attempt FROM observer_outbox').get().first_attempt,1000);b.db.close();
  } finally {fs.rmSync(dir,{recursive:true});}
});
