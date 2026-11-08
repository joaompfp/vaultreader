#!/usr/bin/env node
// End-to-end check of the note save lifecycle in a real browser against the real
// server: leaving a note with unsaved edits must never lose them or write them to
// another note, and the edit states (editState in index.html) must follow the user.
//
//   make e2e-save            (builds ./vaultreader first)
//
// Needs Node 18+ and playwright-core (or playwright) with a Chromium:
//
//   npm i --no-save playwright-core && npx playwright install chromium
//
// Everything runs on throwaway vaults in a temp dir; real notes are never touched.
// Env: VR_BIN (default ./vaultreader), VR_PORT (default 18940),
//      PW_FROM (a package.json whose node_modules hold playwright, if not installed here).

import { createRequire } from 'node:module'
import { spawn } from 'node:child_process'
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, utimesSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const repo = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const bin = resolve(repo, process.env.VR_BIN || './vaultreader')
const port = Number(process.env.VR_PORT || 18940)
const req = createRequire(process.env.PW_FROM || import.meta.url)
const { chromium } = (() => {
  for (const m of ['playwright-core', 'playwright']) { try { return req(m) } catch { /* try the next */ } }
  console.error('playwright-core not found (npm i --no-save playwright-core)'); process.exit(1)
})()
if (!existsSync(bin)) { console.error(`binary not found: ${bin} (run "make build" first)`); process.exit(1) }

const sleep = ms => new Promise(r => setTimeout(r, ms))
const A0 = '# A\n\noriginal A\n', B0 = '# B\n\noriginal B\n'
const results = []
const check = (name, ok, detail = '') => results.push({ name, ok, detail })

// One fresh server + vault + browser page per scenario; `fn` gets helpers.
async function scenario(name, fn) {
  const root = mkdtempSync(join(tmpdir(), 'vr-e2e-'))
  const vault = join(root, 'vaults/v'), appdata = join(root, 'appdata')
  mkdirSync(vault, { recursive: true }); mkdirSync(appdata)
  writeFileSync(join(vault, 'A.md'), A0); writeFileSync(join(vault, 'B.md'), B0)
  // Distinct, realistic mtimes (same-second files would hide the ifMTime guard).
  const now = Date.now() / 1000
  utimesSync(join(vault, 'A.md'), now - 7200, now - 7200); utimesSync(join(vault, 'B.md'), now - 3600, now - 3600)
  writeFileSync(join(appdata, 'config.json'), JSON.stringify({ admin_token: '', rw_paths: ['v'] }))
  const srv = spawn(bin, ['-vaults', join(root, 'vaults'), '-appdata', appdata, '-port', String(port)], { stdio: 'ignore' })
  await sleep(1200)
  const browser = await chromium.launch(); const page = await browser.newPage()
  const dialogs = [], jsErrors = []
  page.on('dialog', d => { dialogs.push(d.message()); d.dismiss() })
  page.on('pageerror', e => jsErrors.push(e.message))
  await page.goto(`http://127.0.0.1:${port}/`); await sleep(800)
  const app = `Alpine.$data(document.querySelector('[x-data]'))`
  const h = {
    page, vault, dialogs,
    vm: expr => page.evaluate(`${app}.${expr}`),
    read: f => readFileSync(join(vault, f), 'utf8'),
    open: async (p) => { await page.evaluate(`${app}.openNote('v','${p}')`); await sleep(500) },
    edit: async (text) => {
      await page.evaluate(`${app}.setMode('edit')`); await page.waitForSelector('.cm-content'); await sleep(300)
      await page.click('.cm-content'); await page.keyboard.press('Control+End'); await page.keyboard.type(text)
    },
  }
  try { await fn(h) } catch (e) { check(`${name}: scenario ran`, false, String(e).split('\n')[0]) }
  check(`${name}: no uncaught JS errors`, jsErrors.length === 0, jsErrors.join(' | ').slice(0, 200))
  await browser.close(); srv.kill(); rmSync(root, { recursive: true, force: true })
}

await scenario('fast switch', async h => {
  await h.open('A.md'); await h.edit('\nTYPED-IN-A'); await sleep(200)
  await h.vm(`openNote('v','B.md')`); await sleep(3000)
  check('fast switch: edits reach A.md', h.read('A.md').includes('TYPED-IN-A'))
  check('fast switch: B.md untouched', h.read('B.md') === B0)
  check('fast switch: lands on B, no stray dialog', (await h.vm('activePath')) === 'B.md' && !(await h.vm('modal.open')))
})

await scenario('slow switch', async h => {
  await h.page.route('**/api/note?vault=v&path=B.md', async r => { await sleep(2500); await r.continue() })
  await h.open('A.md'); await h.edit('\nTYPED-IN-A'); await sleep(200)
  await h.vm(`openNote('v','B.md')`); await sleep(5000)
  check('slow switch: edits reach A.md', h.read('A.md').includes('TYPED-IN-A'))
  check('slow switch: B.md untouched', h.read('B.md') === B0)
  check('slow switch: no false conflict modal over B', !(await h.vm('modal.open')))
})

await scenario('edit states', async h => {
  const st = () => h.vm('editState')
  await h.open('A.md'); check('states: viewing', (await st()) === 'viewing')
  await h.vm(`setMode('edit')`); await h.page.waitForSelector('.cm-content'); await sleep(300)
  check('states: editing', (await st()) === 'editing')
  await h.page.click('.cm-content'); await h.page.keyboard.type('x')
  check('states: pending after a keystroke', (await st()) === 'pending')
  await sleep(2500)
  check('states: editing again once autosaved', (await st()) === 'editing' && h.read('A.md').includes('x'))
  await h.vm(`setMode('preview')`); await sleep(500)
  check('states: viewing after leaving edit mode', (await st()) === 'viewing')
})

await scenario('conflict', async h => {
  await h.open('A.md'); await h.edit('\nMINE')
  const t = Date.now() / 1000 + 5
  writeFileSync(join(h.vault, 'A.md'), A0 + 'THEIRS\n'); utimesSync(join(h.vault, 'A.md'), t, t)
  await sleep(2500)                                   // the autosave hits the changed file
  check('conflict: state is conflict', (await h.vm('editState')) === 'conflict')
  await h.vm(`openNote('v','B.md')`); await sleep(800)
  check('conflict: cannot leave the note', (await h.vm('activePath')) === 'A.md')
  await h.page.getByRole('button', { name: 'Take theirs' }).click(); await sleep(800)
  check('conflict: take theirs loads the disk copy', (await h.vm('noteRaw')).includes('THEIRS') && (await h.vm('isDirty')) === false)
})

await scenario('failed save', async h => {
  await h.page.route('**/api/note?vault=v&path=A.md*', r => r.request().method() === 'PUT' ? r.abort() : r.continue())
  await h.open('A.md'); await h.edit('\nTYPED-IN-A'); await sleep(200)
  await h.vm(`openNote('v','B.md')`); await sleep(1500)
  check('failed: stays on A', (await h.vm('activePath')) === 'A.md')
  check('failed: edits still in the editor', (await h.vm('noteRaw')).includes('TYPED-IN-A'))
  check('failed: user is told', (await h.vm('modal.open')) && (await h.vm('modal.title')) === 'Not saved')
  await h.page.unroute('**/api/note?vault=v&path=A.md*')
  await h.page.getByRole('button', { name: 'OK' }).click()
  await h.vm(`openNote('v','B.md')`); await sleep(1500)
  check('failed: leaves once the save works, edits written', (await h.vm('activePath')) === 'B.md' && h.read('A.md').includes('TYPED-IN-A'))
})

await scenario('browser back', async h => {
  await h.open('B.md'); await h.open('A.md'); await h.edit('\nTYPED-IN-A'); await sleep(200)
  await h.page.goBack(); await sleep(3000)
  check('back: edits written to A.md', h.read('A.md').includes('TYPED-IN-A'))
  check('back: arrives on B', (await h.vm('activePath')) === 'B.md')
})

await scenario('clean switch', async h => {
  await h.open('A.md'); await h.open('B.md')
  check('clean: switching a clean note is immediate, no dialog', (await h.vm('activePath')) === 'B.md' && !(await h.vm('modal.open')) && h.dialogs.length === 0)
})

let failed = 0
for (const r of results) { if (!r.ok) failed++; console.log(`${r.ok ? 'PASS' : 'FAIL'}  ${r.name}${r.detail ? '  — ' + r.detail : ''}`) }
console.log(failed ? `\n${failed} of ${results.length} checks failed` : `\nall ${results.length} checks passed`)
process.exit(failed ? 1 : 0)
