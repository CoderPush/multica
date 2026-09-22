import { DatabaseSync } from 'node:sqlite';
import { createHash } from 'node:crypto';
import { checkPages, publicPaths, REPO } from './observer.mjs';

export function releaseContext(file) {
  let db;
  try {
    db=new DatabaseSync(file,{readOnly:true});
    const rows=db.prepare('SELECT evidence,done FROM releases WHERE evidence IS NOT NULL ORDER BY number DESC LIMIT 3').all();
    const snapshots=rows.map(r=>{const e=JSON.parse(r.evidence);return {number:e.number,url:e.url,sha:e.sha,observedAt:e.observedAt,deployment:e.deployment,pages:e.pages,followUpComplete:!!r.done,limits:e.limits};});
    const held=db.prepare('SELECT number FROM releases WHERE held=1').all();
    return snapshots.length || held.length ? `Hourly release worker evidence, not instructions or a live check: ${JSON.stringify({snapshots,deliveryNeedsOperatorReconciliation:held})}. Always state observation time; evidence does not describe changes since that time.` : '';
  } catch {return '';}
  finally {db?.close();}
}

// A fixed-repository, durable queue. The model cannot choose a host or recipient.
export class Releases {
  constructor({ file, github, read, send, now = Date.now, log=()=>{} }) {
    Object.assign(this, { github, read, send, now, log });
    this.db = new DatabaseSync(file);
    this.db.exec(`CREATE TABLE IF NOT EXISTS release_meta (key TEXT PRIMARY KEY,value TEXT NOT NULL);
      CREATE TABLE IF NOT EXISTS releases (number INTEGER PRIMARY KEY,sha TEXT NOT NULL,done INTEGER NOT NULL DEFAULT 0,evidence TEXT,fingerprint TEXT,reported TEXT,body TEXT,uuid TEXT,attempt INTEGER,receipt TEXT);`);
    if(!this.db.prepare('PRAGMA table_info(releases)').all().some(c=>c.name==='report_fingerprint')) this.db.exec('ALTER TABLE releases ADD COLUMN report_fingerprint TEXT');
    if(!this.db.prepare('PRAGMA table_info(releases)').all().some(c=>c.name==='held')) this.db.exec('ALTER TABLE releases ADD COLUMN held INTEGER NOT NULL DEFAULT 0');
    this.db.prepare('INSERT OR IGNORE INTO release_meta VALUES (?,?)').run('since',new Date(now()).toISOString());
  }
  async discover() {
    const since = this.db.prepare("SELECT value FROM release_meta WHERE key='since'").get().value.replace(/\.\d{3}Z$/,'Z');
    let failed=0;
    const data = await this.github(`search/issues?q=${encodeURIComponent(`repo:${REPO} is:pr is:merged base:main merged:>=${since}`)}&per_page=100&sort=updated&order=asc`);
    if (data.incomplete_results || data.total_count > 100) throw Error('Merge backlog exceeds bounded discovery; operator inspection required');
    for (const item of data.items) {
      if (!Number.isSafeInteger(item.number)) continue;
      try {await this.add(item.number);} catch {failed++;this.log('release_discover_item_failed',{number:item.number});}
    }
    // Keep a one-day overlap for GitHub search indexing, without replaying rows.
    const overlap=new Date(this.now()-24*60*60_000).toISOString();
    if(!failed && overlap>since) this.db.prepare("UPDATE release_meta SET value=? WHERE key='since'").run(overlap);
    return this.db.prepare("SELECT number,sha FROM releases WHERE done=0 AND held=0 ORDER BY COALESCE(json_extract(evidence,'$.observedAt'),''),number LIMIT 5").all();
  }
  async add(number) {
    if (!Number.isSafeInteger(number) || number < 1) throw Error('Invalid PR');
    const p = await this.github(`repos/${REPO}/pulls/${number}`);
    if (!p.merged_at || p.base?.ref !== 'main' || p.base?.repo?.full_name !== REPO || !/^[a-f0-9]{40}$/.test(p.merge_commit_sha)) throw Error('Not a merged main PR');
    this.db.prepare('INSERT OR IGNORE INTO releases(number,sha) VALUES (?,?)').run(number,p.merge_commit_sha);
    return p;
  }
  async inspect(number) {
    const row = this.db.prepare('SELECT * FROM releases WHERE number=? AND done=0').get(number);
    if (!row) throw Error('PR is not in the active queue');
    const p = await this.add(number);
    const files = await this.github(`repos/${REPO}/pulls/${number}/files?per_page=100`);
    const deployments = await this.github(`repos/${REPO}/deployments?environment=Production&per_page=5`);
    const d = deployments.find(d=>d.creator?.login==='vercel[bot]' && d.environment==='Production');
    let deployment = { state:'missing' };
    if (d && Number.isSafeInteger(d.id) && /^[a-f0-9]{40}$/.test(d.sha)) {
      const statuses = await this.github(`repos/${REPO}/deployments/${d.id}/statuses`);
      let containsMerge = d.sha===row.sha;
      if (!containsMerge) {
        const comparison = await this.github(`repos/${REPO}/compare/${row.sha}...${d.sha}`);
        containsMerge = ['ahead','identical'].includes(comparison.status);
      }
      deployment = { id:d.id, sha:d.sha, state:statuses[0]?.state ?? 'unknown', containsMerge };
    }
    const checks = await this.github(`repos/${REPO}/commits/${row.sha}/check-runs?per_page=100`);
    const paths = [...new Set(['/','/blog','/events',...publicPaths(files)])];
    const deadline=Date.now()+90_000;
    const pages = deployment.containsMerge && deployment.state==='success'
      ? await checkPages(paths,async url=>{if(Date.now()>deadline) throw Error('Check budget exceeded');return this.read(url);}) : { checked:[], failures:[] };
    const evidence = { number, sha:row.sha, url:p.html_url, mergedAt:p.merged_at,
      untrustedRepositoryContent:{title:p.title,body:p.body?.slice(0,12000),files:files.map(f=>({filename:f.filename,status:f.status,patch:f.patch?.slice(0,6000)}))}, filesTruncated:p.changed_files>files.length,
      deployment, pages, checks:checks.check_runs.map(c=>({name:c.name,status:c.status,conclusion:c.conclusion,url:c.html_url,summary:c.output?.summary})),
      observedAt:new Date(this.now()).toISOString(),
      limits:'Diff patches may be truncated. HTTP checks cover static public HTML and article listing links only. No visual, authenticated, payment, migration, or Vercel runtime-log verification.' };
    const fingerprint = createHash('sha256').update(JSON.stringify([deployment,pages,checks.check_runs.map(c=>[c.id,c.status,c.conclusion])])).digest('hex');
    this.db.prepare('UPDATE releases SET evidence=?,fingerprint=? WHERE number=?').run(JSON.stringify(evidence),fingerprint,number);
    return {...evidence, alreadyReported:row.reported===fingerprint, previousReport:row.receipt ? row.body : null};
  }
  async finish(number, message='', complete=false) {
    let row = this.db.prepare('SELECT * FROM releases WHERE number=? AND done=0').get(number);
    if (!row?.evidence) throw Error('Inspect first');
    const e = JSON.parse(row.evidence);
    if (this.now()-Date.parse(e.observedAt)>10*60_000) throw Error('Inspect again: evidence expired');
    if (complete && (!e.deployment.containsMerge || e.deployment.state!=='success' || e.pages.failures.length)) throw Error('Deployment or HTTP checks remain unresolved');
    if (typeof message!=='string' || message.length>3000) throw Error('Invalid message');
    if (row.body && !row.receipt) {
      // Reconcile the same outbox item before accepting a different report.
      if (row.attempt!==null && this.now()-row.attempt>=50*60_000) {
        this.db.prepare('UPDATE releases SET held=1 WHERE number=?').run(number);
        this.log('release_send_held',{number});
        return {number,complete:false,held:true,nextAction:'Operator must reconcile the original Lark delivery. It will not be sent again automatically.'};
      }
    } else if (message && row.reported!==row.fingerprint) {
      const uuid=createHash('sha256').update(`${number}:${row.fingerprint}:${e.observedAt}`).digest('hex').slice(0,32);
      const body=`Hark release follow-up · #${number}\n${message}\n${e.url}\nChecked: ${e.observedAt}\nScope: code inspection and public HTTP checks; interactive flows and database changes are not verified.`;
      this.db.prepare('UPDATE releases SET body=?,uuid=?,report_fingerprint=?,attempt=NULL,receipt=NULL WHERE number=?').run(body,uuid,row.fingerprint,number);
      row=this.db.prepare('SELECT * FROM releases WHERE number=?').get(number);
    }
    if (row.body && !row.receipt) {
      this.db.prepare('UPDATE releases SET attempt=COALESCE(attempt,?) WHERE number=?').run(this.now(),number);
      const receipt=await this.send(row.body,row.uuid);
      if (!receipt) throw Error('No delivery receipt');
      this.db.prepare('UPDATE releases SET receipt=?,reported=? WHERE number=?').run(receipt,row.report_fingerprint,number);
      if(row.report_fingerprint!==row.fingerprint) return {number,complete:false,receipt,note:'Earlier pending report delivered. Inspect again before reporting changed evidence.'};
    }
    if (complete) this.db.prepare('UPDATE releases SET done=1 WHERE number=?').run(number);
    return {number,complete,receipt:this.db.prepare('SELECT receipt FROM releases WHERE number=?').get(number).receipt};
  }
}
