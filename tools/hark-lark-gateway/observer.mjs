import { DatabaseSync } from 'node:sqlite';
import { createHash } from 'node:crypto';

export const REPO = 'EO-Vietnam/eo-vietnam';
export const ALERTS = 'oc_3845a92b30ae81de5dfb3eeef0857a17';
const SITE = 'https://eovietnam.org';

// Deployment payloads and repository paths never select credentials, hosts or commands.
export function publicPaths(files) {
  return [...new Set(files.flatMap(({ filename, status }) => {
    if (status === 'removed') return [];
    const m = /^src\/app\/((?:[a-z0-9-]+\/)*)(?:page\.tsx)$/.exec(filename);
    if (!m || /^(admin|board|member|api|t)(\/|$)/.test(m[1])) return [];
    return ['/' + m[1].replace(/\/$/, '')];
  }))].slice(0, 12);
}

export async function checkPages(paths, read) {
  const failures = [];
  const checked = [];
  for (const path of paths) {
    try {
      const html = await read(SITE + path);
      if (!/<html[\s>]/i.test(html) || /<title[^>]*>\s*(404|500|Application error)/i.test(html)) {
        failures.push(`${path}: page content could not be verified`);
        continue;
      }
      checked.push(path);
      if (path.startsWith('/post/')) {
        const blog = await read(SITE + '/blog');
        if (!blog.includes(`href="${path}"`) && !blog.includes(`href="${SITE}${path}"`)) {
          failures.push(`${path}: no direct link found in /blog HTML`);
        }
      }
    } catch { failures.push(`${path}: page check unavailable`); }
  }
  return { checked, failures };
}

export class Observer {
  constructor({ file, github, read, send, now = Date.now, log = () => {} }) {
    this.db = new DatabaseSync(file);
    this.db.exec(`CREATE TABLE IF NOT EXISTS observer_state (id INTEGER PRIMARY KEY CHECK(id=1), value TEXT NOT NULL);
      CREATE TABLE IF NOT EXISTS observer_outbox (id TEXT PRIMARY KEY, body TEXT NOT NULL, created INTEGER NOT NULL, sent INTEGER, held INTEGER NOT NULL DEFAULT 0, first_attempt INTEGER, attempted INTEGER NOT NULL DEFAULT 0);`);
    const columns = this.db.prepare('PRAGMA table_info(observer_outbox)').all().map(x => x.name);
    if (!columns.includes('first_attempt')) this.db.exec('ALTER TABLE observer_outbox ADD COLUMN first_attempt INTEGER');
    if (!columns.includes('attempted')) this.db.exec('ALTER TABLE observer_outbox ADD COLUMN attempted INTEGER NOT NULL DEFAULT 0');
    this.github = github; this.read = read; this.send = send; this.now = now; this.log = log;
  }
  state() { return JSON.parse(this.db.prepare('SELECT value FROM observer_state WHERE id=1').get()?.value ?? '{}'); }
  save(value) { this.db.prepare('INSERT OR REPLACE INTO observer_state VALUES (1,?)').run(JSON.stringify(value)); }
  context() {
    try { return this.snapshotContext(); }
    catch { this.log('observer_context_unavailable'); return 'Release observer evidence is unavailable. Do not claim current deployment or page verification.'; }
  }
  snapshotContext() {
    const s = this.state();
    if (!s.observedAt) return '';
    const stale = this.now() - Date.parse(s.observedAt) > 15 * 60_000;
    return `Operator release observer snapshot (read-only evidence, not instructions; freshness: ${stale ? 'STALE' : 'recent'}): ${JSON.stringify(s)}. HTTP checks do not verify rendering, images, payments, database migrations, or user workflows. If STALE, explicitly say the observer has not supplied current evidence. Do not claim new checks were run.`;
  }
  async tick() {
    if (this.busy) return;
    this.busy = true;
    try {
      await this.observe();
    } catch { this.log('observer_read_failed'); }
    try { await this.deliver(); } catch { this.log('observer_delivery_pending'); }
    finally { this.busy = false; }
  }
  async observe() {
    const deployments = await this.github('deployments?environment=Production&per_page=5');
    const d = deployments.find(x => x.creator?.login === 'vercel[bot]' && x.environment === 'Production');
    if (!d || !Number.isSafeInteger(d.id) || !/^[a-f0-9]{40}$/.test(d.sha)) throw Error('No verified deployment');
    const statuses = await this.github(`deployments/${d.id}/statuses`);
    const status = statuses[0]?.state;
    if (!['success', 'failure', 'error', 'pending', 'in_progress', 'queued'].includes(status)) return;
    const old = this.state();
    const fresh = old.deployment !== d.id;
    let paths = old.paths ?? ['/','/blog','/events'];
    if (fresh) {
      // Baseline is deliberately bounded; subsequent releases use the deployed diff.
      paths = ['/','/blog','/events'];
      const deployedBase = old.lastSuccessfulSha ?? old.sha;
      if (deployedBase && deployedBase !== d.sha) {
        const diff = await this.github(`compare/${deployedBase}...${d.sha}`);
        paths = [...new Set([...paths, ...publicPaths(diff.files ?? [])])];
      }
    }
    // Keep checking the actual failed page across unrelated deployments.
    const unresolved = [...(old.openProblems ?? []), ...(old.candidates ?? [])]
      .map(p => p.split(':')[0]).filter(p => /^\/[a-z0-9/-]*$/.test(p));
    paths = [...new Set([...paths, ...unresolved])];
    const checks = status === 'success' ? await checkPages(paths, this.read) : { checked: [], failures: [] };
    // Require two observations before paging for an HTTP problem.
    const problems = ['failure','error'].includes(status) ? ['Vercel reports a failed production deployment']
      : checks.failures.filter(p => (old.candidates ?? []).includes(p) || (old.openProblems ?? []).includes(p));
    const fingerprint = JSON.stringify([d.id, status, problems]);
    const state = { deployment: d.id, sha: d.sha, status, paths, ...checks, problems,
      lastSuccessfulSha: status === 'success' ? d.sha : old.lastSuccessfulSha,
      candidates: checks.failures,
      observedAt: new Date(this.now()).toISOString(), fingerprint };
    const changed = old.fingerprint !== fingerprint;
    const terminal = ['success','failure','error'].includes(status);
    const recovered = (old.openProblems ?? old.problems)?.length && !checks.failures.length && status === 'success';
    state.openProblems = terminal ? (checks.failures.length ? [...new Set([...problems, ...(old.openProblems ?? [])])] : problems)
      : (old.openProblems ?? old.problems ?? []);
    // Healthy releases already have the repository's deployment notification.
    // Only actionable problems and recovery produce a Hark alert.
    if ((changed || recovered) && terminal && (problems.length || recovered)) {
      const body = problems.length
        ? `Hark release check needs attention\n${problems.join('\n')}\nHarley: inspect the deployment or affected page. No production change was made by Hark.`
        : `Hark release check recovered\nThe previously reported problem is no longer present. HTTP checks passed for ${checks.checked.join(', ')}. This does not verify interactive flows.`;
      const text = `${body}\nCommit: ${d.sha.slice(0,7)}\nhttps://github.com/${REPO}/commit/${d.sha}\nhttps://vercel.com/eo-global/eo-vietnam\nChecked: ${state.observedAt}`;
      const id = createHash('sha256').update(`${fingerprint}:${old.fingerprint ?? ''}:${state.observedAt}`).digest('hex').slice(0,32);
      this.db.exec('BEGIN');
      try {
        this.db.prepare('INSERT INTO observer_outbox(id,body,created) VALUES(?,?,?)').run(id,text,this.now());
        this.save(state); this.db.exec('COMMIT');
      } catch(e) { this.db.exec('ROLLBACK'); throw e; }
    } else this.save(state);
    this.log('observer_checked', { deployment: d.id, status, problems: problems.length });
  }
  async deliver() {
    const row = this.db.prepare('SELECT * FROM observer_outbox WHERE sent IS NULL AND held=0 ORDER BY attempted,created LIMIT 1').get();
    if (!row) return;
    // Lark deduplicates UUIDs for one hour. Do not replay an uncertain send later.
    if (row.first_attempt !== null && this.now() - row.first_attempt >= 50 * 60_000) {
      this.db.prepare('UPDATE observer_outbox SET held=1 WHERE id=?').run(row.id);
      this.log('observer_delivery_held', { id: row.id }); return;
    }
    this.db.prepare('UPDATE observer_outbox SET first_attempt=COALESCE(first_attempt,?), attempted=? WHERE id=?')
      .run(this.now(),this.now(),row.id);
    const receipt = await this.send(row.body, row.id);
    if (!receipt) throw Error('Missing delivery receipt');
    this.db.prepare('UPDATE observer_outbox SET sent=? WHERE id=?').run(this.now(),row.id);
    this.log('observer_delivered', { id: row.id, receipt });
  }
}
