// Render the full App against hub-development-fixture.mjs; no real update is run.
// node web/e2e/hub-development.mjs <url> <shots> --executable <chromium>
import { chromium } from 'playwright-core';
import { mkdir, writeFile, mkdtemp, symlink, unlink, rm } from 'node:fs/promises';
import { execFileSync } from 'node:child_process';
import assert from 'node:assert/strict';
const [origin, out] = process.argv.slice(2);
const executablePath = process.argv[process.argv.indexOf('--executable') + 1];
if (!origin || !out || !process.argv.includes('--executable')) throw new Error('Expected URL, screenshot directory and --executable');
const address = new URL(origin);
assert.ok(['127.0.0.1', 'localhost', '[::1]'].includes(address.hostname), 'Use the owned loopback fixture');
assert.ok(!address.username && !address.password && address.pathname === '/' && !address.search && !address.hash, 'Supply an origin');
const base = address.origin;
const probe = await (await fetch(base + '/__fixture', { signal: AbortSignal.timeout(5000) })).json();
assert.equal(probe.fixture, 'persona-web-development-ui', 'Refuse non-fixture services');
await mkdir(out, { recursive: true });
const temporary = await mkdtemp((process.env.TMPDIR || '/tmp') + '/persona-web-browser-');
// Chromium's singleton socket needs a short alias; material stays in the owned temp directory.
const alias = '/tmp/persona-web-browser-' + process.pid;
await symlink(temporary, alias);
const browser = await chromium.launch({ executablePath, headless: true, args: ['--no-sandbox'], env: { ...process.env, TMPDIR: alias } }).catch(async error => {
  await unlink(alias); await rm(temporary, { recursive: true }); throw error;
});
const evidence = { head: execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).trim(),
  web_tree: execFileSync('git', ['rev-parse', 'HEAD:web/src'], { encoding: 'utf8' }).trim(),
  browser: browser.version(), platform: process.platform, node: process.version, screenshots: [], interactions: [] };
const watchdog = setTimeout(() => { console.error('TIMEOUT: Web development evidence exceeded five minutes'); process.exitCode = 2; void browser.close(); }, 300_000);
async function open(width, theme, textSize, mode = 'available') {
  const context = await browser.newContext({ viewport: { width, height: width === 390 ? 844 : 1000 },
    hasTouch: width === 390, locale: 'en-US', timezoneId: 'America/Los_Angeles', reducedMotion: 'reduce', colorScheme: theme });
  await context.addInitScript(({ theme, textSize }) => {
    localStorage.setItem('persona.theme', theme); localStorage.setItem('persona.text-size', textSize);
  }, { theme, textSize });
  await context.route('**/*', route => new URL(route.request().url()).origin === new URL(base).origin ? route.continue() : route.abort());
  const page = await context.newPage();
  page.setDefaultTimeout(15000);
  await page.clock.setFixedTime(new Date('2026-11-15T17:00:00Z'));
  const errors = []; page.on('pageerror', error => errors.push(error.message));
  await context.request.post(base + '/__fixture', { data: { mode } });
  await page.goto(base + '/settings?tab=hub');
  await page.getByRole('combobox', { name: 'Update source', exact: true }).waitFor();
  if (mode === 'available') await page.getByRole('combobox', { name: 'Update version', exact: true }).waitFor();
  async function shot(name) {
    await page.evaluate(() => document.fonts.ready);
    assert.deepEqual(errors, [], 'Uncaught browser errors');
    assert.equal(await page.locator('vite-error-overlay').count(), 0, 'No Vite error overlay');
    assert.equal(await page.locator('html').evaluate(el => el.classList.contains('dark')), theme === 'dark', 'Actual theme matches capture label');
    assert.equal(await page.locator('html').getAttribute('data-text-size'), textSize === 'standard' ? null : textSize, 'Actual text size matches capture label');
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, 'Page horizontal overflow');
    await page.screenshot({ path: out + '/' + name + '.png', animations: 'disabled' });
    evidence.screenshots.push({ file: name + '.png', width, theme, textSize, mode, timezone: 'America/Los_Angeles',
      scale: await page.evaluate(() => getComputedStyle(document.documentElement).getPropertyValue('--text-scale').trim()) });
  }
  return { context, page, shot };
}
try {
  // Actual supported Web maximum is Large (14/13), not an iOS Dynamic Type category.
  for (const width of [1440, 390]) for (const theme of ['light', 'dark']) for (const size of ['standard', 'large']) {
    const { context, page, shot } = await open(width, theme, size, 'multi-assistant');
    const tag = `${width}-${theme}-${size}`;
    assert.match(await page.locator('[data-settings-group="hub-version"]').innerText(), /2026-11-12 17:02:03 · a{12}/);
    assert.match(await page.locator('[data-settings-group="hub-version"]').innerText(), /2026-11-13 18:23:45 · b{12}/);
    assert.equal(await page.getByRole('switch', { name: 'Update hub automatically', exact: true }).getAttribute('aria-checked'), 'false');
    const versionLabel = page.locator('[data-settings-group="hub-version"] .truncate').filter({ hasText: /^Version$/ }).first();
    assert.equal(await versionLabel.evaluate(el => el.scrollWidth > el.clientWidth), false, 'Version label is readable at the selected text size');
    await shot(tag + '-source');
    await page.locator('[data-settings-group="hub-auto-update"]').evaluate(el => {
      el.scrollIntoView({ block: 'start' });
      for (let p = el.parentElement; p; p = p.parentElement) if (p.scrollHeight > p.clientHeight && /auto|scroll/.test(getComputedStyle(p).overflowY)) { p.scrollTop -= 80; break; }
    });
    await shot(tag + '-options');
    const computers = page.locator('[data-settings-group="hub-nodes"]');
    const desk = computers.getByRole('button', { name: /Desk computer/ });
    const lab = computers.getByRole('button', { name: /Lab computer/ });
    const travel = computers.getByRole('button', { name: /Travel computer/ });
    await computers.getByText('Managed by Studio · Built locally by the assistant', { exact: true }).waitFor();
    assert.match(await desk.innerText(), /Managed by Example assistant · Package from hub/);
    assert.match(await desk.innerText(), /Transferring/);
    assert.match(await lab.innerText(), /Waiting for idle/);
    const installed = lab.getByText('web-dev-cccccccccccc-202611120001', { exact: true });
    assert.equal(await installed.evaluate(el => el.scrollWidth > el.clientWidth), false, 'The installed build fits at this text size');
    assert.match(await travel.innerText(), /Last deploy failed: Go is unavailable/);
    assert.match(await travel.innerText(), /Install Go on the computer running Studio/);
    assert.doesNotMatch(await travel.innerText(), /Package from hub|Built locally by the assistant/);
    await computers.evaluate(el => el.scrollIntoView({ block: 'center' }));
    await shot(tag + '-computers');
    await context.close();
  }
  for (const width of [1440, 390]) for (const theme of ['light', 'dark']) for (const size of ['standard', 'large']) {
    const { context, page, shot } = await open(width, theme, size, 'building');
    const pipeline = page.getByRole('region', { name: 'Update pipeline', exact: true });
    const stages = pipeline.getByRole('list', { name: 'Update stages', exact: true });
    assert.equal(await stages.getByRole('listitem').count(), 4);
    assert.equal(await stages.locator('[aria-current="step"]').innerText(), 'Build');
    assert.equal(await pipeline.getByRole('list', { name: 'Phase steps', exact: true }).getByRole('listitem').count(), 6);
    assert.equal(await pipeline.getByRole('progressbar', { name: 'Compile programs' }).getAttribute('aria-valuenow'), '75');
    assert.match(await pipeline.innerText(), /Target b{12}/);
    assert.match(await pipeline.innerText(), /personad \(linux-amd64\) · Items 3\/4/);
    assert.equal(await pipeline.evaluate(el => el.scrollWidth > el.clientWidth), false, 'Pipeline horizontal overflow');
    if (width === 390) {
      const picker = page.getByRole('combobox', { name: 'Update version' });
      assert.equal(await picker.evaluate(el => getComputedStyle(el).opacity), '0', 'Disabled native text stays hidden under the wrapping label');
      const selectedVersion = picker.locator('..').locator(':scope > [aria-hidden="true"]');
      assert.equal(await selectedVersion.isVisible(), true);
      assert.match(await selectedVersion.innerText(), /2026-11-13 18:23:45 ·\s+b{12}/);
      assert.equal(await selectedVersion.evaluate(el => el.scrollWidth > el.clientWidth), false, 'Selected version horizontal overflow');
    }
    await pipeline.evaluate(el => el.scrollIntoView({ block: 'center' }));
    await shot(`${width}-${theme}-${size}-pipeline`);
    await context.close();
  }
  {
    const { context, page, shot } = await open(1440, 'light', 'standard');
    const source = page.getByRole('combobox', { name: 'Update source', exact: true });
    const channelAnchor = page.locator('[data-settings-row="hub-channel"]');
    assert.equal(await channelAnchor.getByRole('combobox', { name: 'Update source' }).count(), 1);
    await page.getByRole('textbox', { name: 'Search settings' }).fill('Channel');
    await page.getByRole('button', { name: /This hub\s*›\s*Channel/ }).click();
    await page.waitForFunction(() => document.querySelector('[data-settings-row="hub-channel"]')?.style.boxShadow.includes('inset'));
    const automatic = page.getByRole('switch', { name: 'Update hub automatically', exact: true });
    await automatic.click(); await page.waitForFunction(() => document.querySelector('[role="switch"][aria-label="Update hub automatically"]').getAttribute('aria-checked') === 'true');
    await page.getByRole('switch', { name: 'Update computers automatically', exact: true }).click();
    await page.waitForFunction(() => document.querySelector('[role="switch"][aria-label="Update computers automatically"]').getAttribute('aria-checked') === 'true');
    await shot('automatic-enabled');
    await source.selectOption('release');
    await page.waitForFunction(() => !document.querySelector('select[aria-label="Update version"]'));
    assert.match(await channelAnchor.innerText(), /Channel\s+Web development/);
    assert.equal(await automatic.getAttribute('aria-checked'), 'false');
    await shot('release-source');
    await source.selectOption('web-dev');
    const version = page.getByRole('combobox', { name: 'Update version', exact: true });
    assert.match(await version.locator('option[value=""]').textContent(), /2026-11-13 18:23:45 · b{12}/);
    assert.match(await version.locator('option[value^="snapshot-"]').textContent(), /2026-11-12 02:25:11 · c{12}/);
    await version.selectOption('snapshot-20261112000100000000-cccccccccccc');
    const update = page.locator('[data-settings-group="hub-update"]');
    assert.match(await update.innerText(), /Target commit\s+2026-11-12 02:25:11 · c{12}/);
    assert.match(await update.innerText(), /Backed up at\s+2026-11-13 02:26:35/);
    await page.getByRole('button', { name: 'Apply backup', exact: true }).click();
    const dialog = page.getByRole('dialog');
    assert.match(await dialog.innerText(), /current database and history will be kept/);
    await shot('backup-confirm');
    await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
    let receipt = await (await context.request.get(base + '/__fixture')).json();
    assert.equal(receipt.requests.some(r => r.path.endsWith('/apply')), false);
    await page.getByRole('button', { name: 'Apply backup', exact: true }).click();
    await dialog.getByRole('button', { name: 'Apply backup', exact: true }).click();
    await page.getByRole('button', { name: 'Update now…', exact: true }).waitFor();
    assert.equal(await source.isDisabled(), true);
    assert.equal(await automatic.getAttribute('aria-checked'), 'false');
    await shot('backup-waiting');
    await page.getByRole('button', { name: 'Update now…', exact: true }).click();
    assert.match(await dialog.innerText(), /interrupted/);
    await shot('interrupt-confirm');
    await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
    receipt = await (await context.request.get(base + '/__fixture')).json();
    assert.equal(receipt.requests.some(r => r.input.now), false);
    await page.getByRole('button', { name: 'Update now…', exact: true }).click();
    await dialog.getByRole('button', { name: 'Update now', exact: true }).click();
    receipt = await (await context.request.get(base + '/__fixture')).json();
    assert.equal(receipt.requests.filter(r => r.path.endsWith('/apply')).length, 1);
    assert.equal(receipt.requests.filter(r => r.input.now === true).length, 1);
    evidence.interactions.push('Channel search targets the source selector; source change shows the running channel, disables auto and hides backups; program-only backup cancel/confirm; wait controls disabled; Update now cancel/confirm', receipt.requests.filter(r => r.method !== 'GET'));
    await context.close();
  }
  for (const [mode, theme, width] of [['setup','light',1440], ['backing_up','light',390], ['external-quiesce','dark',390],
    ['applying_backup','dark',1440], ['downloading','light',1440], ['confirming','dark',390],
    ['failed','dark',390], ['rolled_back','light',390], ['backup-error','dark',1440]]) {
    const { context, page, shot } = await open(width, theme, 'large', mode);
    if (['backing_up','external-quiesce','applying_backup'].includes(mode)) {
      assert.equal(await page.getByRole('combobox', { name: 'Update source', exact: true }).isDisabled(), true);
      const steps = page.getByRole('list', { name: 'Phase steps', exact: true });
      assert.equal(await steps.getByRole('listitem').count(), mode === 'backing_up' ? 5 : mode === 'external-quiesce' ? 6 : 2);
    }
    if (mode === 'external-quiesce') assert.match(await page.getByRole('region', { name: 'Update pipeline' }).innerText(), /Resume writers/);
    if (mode === 'downloading') assert.match(await page.getByRole('region', { name: 'Update pipeline' }).innerText(), /hub 75%/);
    if (mode === 'setup') assert.equal(await page.locator('option[value="web-dev"]').isDisabled(), true);
    if (mode === 'backup-error') await page.getByRole('alert').waitFor();
    await page.locator('[data-settings-group="hub-update"]').scrollIntoViewIfNeeded();
    await shot(mode + '-large');
    if (mode === 'rolled_back') {
      await page.getByRole('switch', { name: 'Update hub automatically', exact: true }).scrollIntoViewIfNeeded();
      await shot('rollback-history-large');
    }
    await context.close();
  }
  {
    const { context, page, shot } = await open(390, 'dark', 'large');
    const version = page.getByRole('combobox', { name: 'Update version', exact: true });
    await version.locator('option[value^="snapshot-"]').waitFor({ state: 'attached' });
    await version.focus();
    await version.press('ArrowDown');
    assert.equal(await version.inputValue(), 'snapshot-20261112000100000000-cccccccccccc');
    await page.getByRole('button', { name: 'Apply backup', exact: true }).click();
    await shot('backup-confirm-narrow-dark-large');
    await context.close();
  }
} finally {
  clearTimeout(watchdog); await browser.close(); await unlink(alias); await rm(temporary, { recursive: true });
}
await writeFile(out + '/run.json', JSON.stringify(evidence, null, 2) + '\n');
console.log(`PASS: ${evidence.screenshots.length} screenshots, source/auto/backup/idle/forced-confirm interactions; no uncaught browser errors or page overflow`);
