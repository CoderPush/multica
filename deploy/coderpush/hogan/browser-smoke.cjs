#!/usr/bin/env node
// Operator preflight: local fixture only; no external services or credentials.
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const { chromium } = require('/Users/webworker/tools/browser/node_modules/playwright');

async function main() {
  const role = process.argv[process.argv.indexOf('--role') + 1];
  assert.ok(['engineer', 'reviewer'].includes(role));
  assert.equal(process.getuid(), 600, 'worker UID changed; inspect account first');
  assert.equal(process.env.MULTICA_REPO_CHECKOUT_MODE, 'isolated');
  await fs.access(`/Users/webworker/runtime/${role}/auth.json`, fs.constants.R_OK);
  for (const path of [
    '/Users/hogan/.codex/auth.json',
    '/Users/webworker/.multica/profiles/hogan-web/config.json',
    '/Users/webworker/.ssh',
    `/Users/webworker/runtime/${role === 'engineer' ? 'reviewer' : 'engineer'}/auth.json`,
  ]) {
    const readable = await fs.access(path, fs.constants.R_OK).then(() => true, () => false);
    assert.equal(readable, false, `credential boundary failed: ${path}`);
  }
  const browser = await chromium.launch({ headless: true, timeout: 15000 });
  try {
    const page = await browser.newPage();
    await page.setContent('<!doctype html><title>Worker fixture</title><button id="test">Run</button><script>document.querySelector("button").onclick = () => document.querySelector("button").textContent = "Passed";</script>');
    await page.getByRole('button', { name: 'Run', exact: true }).click();
    assert.equal(await page.getByRole('button').textContent(), 'Passed');
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: 900 });
      const path = `browser-${role}-${width}.png`;
      await page.screenshot({ path });
      assert.ok((await fs.stat(path)).size > 1000);
    }
    console.log(JSON.stringify({ result: 'pass', role, uid: process.getuid(), browser: browser.version(), widths: [1440, 390], click: 'pass', credentialBoundaries: 'pass' }));
  } finally {
    await browser.close();
  }
}

main().catch(error => { console.error(error); process.exitCode = 1; });
