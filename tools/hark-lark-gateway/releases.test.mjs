import test from 'node:test';
import assert from 'node:assert/strict';
import { Releases } from './releases.mjs';
const sha='a'.repeat(40);
function fixture({send=async()=> 'receipt',state='success'}={}) {
  let now=Date.parse('2026-09-22T10:00:00Z');
  const r=new Releases({file:':memory:',now:()=>now,send,read:async()=>'<html>ok</html>',github:async p=>{
    if(p.startsWith('search/')) return {total_count:1,items:[{number:425}]};
    if(p.endsWith('/files?per_page=100')) return [];
    if(p.includes('check-runs')) return {check_runs:[]};
    if(p.includes('/statuses')) return [{state}];
    if(p.includes('/deployments?')) return [{id:1,sha,creator:{login:'vercel[bot]'},environment:'Production'}];
    return {merged_at:'2026-09-22T09:00:00Z',merge_commit_sha:sha,base:{ref:'main',repo:{full_name:'EO-Vietnam/eo-vietnam'}},html_url:'https://github.com/EO-Vietnam/eo-vietnam/pull/425',changed_files:0};
  }});
  return {r,advance:n=>now+=n};
}
test('merged releases close once and repeated discovery does not reopen',async()=>{
  const {r}=fixture();assert.equal((await r.discover()).length,1);
  await r.inspect(425);await r.finish(425,'',true);
  assert.equal((await r.discover()).length,0);
});
test('unknown PR cannot be inspected or reported',async()=>{
  const {r}=fixture();await assert.rejects(r.inspect(999));await assert.rejects(r.finish(999,'x',true));
});
test('pending deployment cannot be completed',async()=>{
  const {r}=fixture({state:'pending'});await r.discover();await r.inspect(425);
  await assert.rejects(r.finish(425,'',true));assert.equal((await r.discover()).length,1);
});
test('same evidence does not duplicate alerts',async()=>{
  let sends=0;const {r}=fixture({send:async()=>{sends++;return 'receipt';}});
  await r.discover();await r.inspect(425);await r.finish(425,'Finding',false);
  await r.inspect(425);await r.finish(425,'Finding again',false);assert.equal(sends,1);
});
test('uncertain send cannot be retried after Lark dedup expiry',async()=>{
  let sends=0;const {r,advance}=fixture({send:async()=>{sends++;throw Error('timeout');}});
  await r.discover();await r.inspect(425);await assert.rejects(r.finish(425,'Finding',false));
  advance(60*60_000);await r.inspect(425);assert.equal((await r.finish(425,'changed',false)).held,true);assert.equal(sends,1);
  assert.equal((await r.discover()).length,0);
  assert.equal(r.db.prepare('SELECT held FROM releases WHERE number=425').get().held,1);
});
test('a production deployment behind the merge cannot complete the release',async()=>{
  const {r}=fixture();const github=r.github;let compared=false;
  r.github=async p=>{
    if(p.includes('/deployments?')) return [{id:2,sha:'b'.repeat(40),creator:{login:'vercel[bot]'},environment:'Production'}];
    if(p.includes('/compare/')) {compared=true;return {status:'behind'};}
    return github(p);
  };
  await r.discover();const e=await r.inspect(425);assert.equal(compared,true);assert.equal(e.deployment.containsMerge,false);
  await assert.rejects(r.finish(425,'',true),/Deployment or HTTP checks remain unresolved/);
});
test('a failed new lookup does not hide queued work or advance discovery',async()=>{
  const {r,advance}=fixture();await r.discover();const before=r.db.prepare("SELECT value FROM release_meta WHERE key='since'").get().value;
  advance(48*60*60_000);const github=r.github;
  r.github=async p=>p.startsWith('search/')?{total_count:1,items:[{number:999}]}:p.endsWith('/999')?Promise.reject(Error('temporary')):github(p);
  assert.equal((await r.discover())[0].number,425);
  assert.equal(r.db.prepare("SELECT value FROM release_meta WHERE key='since'").get().value,before);
});
test('failed public page keeps release pending',async()=>{
  const {r}=fixture();r.read=async()=>{throw Error('unavailable');};await r.discover();await r.inspect(425);
  await assert.rejects(r.finish(425,'',true),/Deployment or HTTP checks remain unresolved/);
});
test('stale evidence must be refreshed before completion',async()=>{
  const {r,advance}=fixture();await r.discover();await r.inspect(425);advance(11*60_000);
  await assert.rejects(r.finish(425,'',true));
});
test('unmerged PR is rejected',async()=>{
  const {r}=fixture();r.github=async()=>({base:{ref:'main'}});await assert.rejects(r.add(425));
});
test('a refreshed snapshot cannot mark an older uncertain report as current',async()=>{
  let attempts=0;const {r}=fixture({send:async()=>{if(++attempts===1) throw Error('timeout');return 'receipt';}});
  await r.discover();await r.inspect(425);await assert.rejects(r.finish(425,'Old report',false));
  r.db.prepare('UPDATE releases SET fingerprint=? WHERE number=425').run('changed');
  const result=await r.finish(425,'New report',true);
  assert.equal(result.complete,false);
  assert.notEqual(r.db.prepare('SELECT reported FROM releases WHERE number=425').get().reported,'changed');
  assert.equal((await r.discover()).length,1);
});
