#!/usr/bin/env node
// Regenerates the README images in docs/img/ from the bundled demo vault.
//
//   make screenshots            (builds ./vaultreader first)
//
// Needs Node 18+, ffmpeg (for the GIF), and playwright-core with a Chromium:
//
//   npm i --no-save playwright-core
//   npx playwright install chromium
//
// The script copies examples/vaults and examples/appdata to a temp dir and
// serves that copy, so examples/ is never modified. It only ever reads the
// demo vault: never point it at real notes. Local instances started:
//   main  — the demo vault, as in `make run-demo`
//   team  — only the demo's Projects/ folder, as vault "team", with
//           require_user on and the team feature flags off
//   walk  — a second copy of the demo for the walkthrough video (it edits)
//
// Env: VR_BIN (default ./vaultreader), VR_PORT (default 18731; also uses +1, +2).

import { chromium } from 'playwright-core'
import { spawn, execFileSync } from 'node:child_process'
import { cpSync, mkdirSync, mkdtempSync, readdirSync, rmSync, statSync, writeFileSync, existsSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const repo = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const bin = resolve(repo, process.env.VR_BIN || './vaultreader')
const port = Number(process.env.VR_PORT || 18731)
const out = join(repo, 'docs', 'img')
const viewport = { width: 1600, height: 1000 }
const GIF_MAX = 3 * 1024 * 1024

if (!existsSync(bin)) {
  console.error(`binary not found: ${bin} (run "make build" first)`)
  process.exit(1)
}

// ── Throwaway copies of the demo data ────────────────────────────────────────
const work = mkdtempSync(join(tmpdir(), 'vr-shots-'))
const mainVaults = join(work, 'main', 'vaults')
const mainApp = join(work, 'main', 'appdata')
cpSync(join(repo, 'examples', 'vaults'), mainVaults, { recursive: true })
cpSync(join(repo, 'examples', 'appdata'), mainApp, { recursive: true })

const teamVaults = join(work, 'team', 'vaults')
const teamApp = join(work, 'team', 'appdata')
cpSync(join(repo, 'examples', 'vaults', 'demo', 'Projects'), join(teamVaults, 'team'), { recursive: true })
mkdirSync(teamApp, { recursive: true })
writeFileSync(join(teamApp, 'config.json'), JSON.stringify({
  admin_token: '',
  rw_paths: ['team'],
  require_user: true,
  default_note: 'team/Website relaunch.md',
  features: { share_writable: false, webdav: false, sync_status: false, trash_empty: false },
}, null, 2))

const procs = []
function serve(vaults, appdata, p) {
  const child = spawn(bin, ['-vaults', vaults, '-appdata', appdata, '-port', String(p)], { stdio: ['ignore', 'ignore', 'inherit'] })
  procs.push(child)
  return `http://127.0.0.1:${p}`
}
async function waitHealthy(base) {
  for (let i = 0; i < 100; i++) {
    try { if ((await fetch(base + '/health')).ok) return } catch {}
    await new Promise(r => setTimeout(r, 100))
  }
  throw new Error(`${base} did not become healthy`)
}
function cleanup() {
  for (const c of procs) c.kill()
  rmSync(work, { recursive: true, force: true })
}
process.on('exit', cleanup)

// The walkthrough edits a note, so it gets its own copy and instance.
const videoVaults = join(work, 'video-src', 'vaults')
const videoApp = join(work, 'video-src', 'appdata')
cpSync(mainVaults, videoVaults, { recursive: true })
cpSync(mainApp, videoApp, { recursive: true })

const main = serve(mainVaults, mainApp, port)
const team = serve(teamVaults, teamApp, port + 1)
const walk = serve(videoVaults, videoApp, port + 2)
for (const b of [main, team, walk]) await waitHealthy(b)
mkdirSync(out, { recursive: true })

const hero = '/n/demo/Projects/Website%20relaunch.md'

// ── Helpers ──────────────────────────────────────────────────────────────────
async function openNote(page, base, path) {
  await page.goto(base + path)
  await page.waitForSelector('#preview h1', { timeout: 10000 })
  // Mermaid renders asynchronously; wait for its SVG when the note has one.
  await page.waitForFunction(() => !document.querySelector('pre code.language-mermaid'), null, { timeout: 10000 }).catch(() => {})
  await page.waitForTimeout(600)
}
const pause = (page, ms) => page.waitForTimeout(ms)
const alpine = (page, fn, arg) => page.evaluate(([src, a]) => {
  const app = Alpine.$data(document.getElementById('app'))
  return new Function('app', 'arg', src)(app, a)
}, [fn, arg])

const browser = await chromium.launch()
try {
  // ── Stills (main instance) ────────────────────────────────────────────────
  const ctx = await browser.newContext({ viewport, deviceScaleFactor: 1, colorScheme: 'light' })
  const page = await ctx.newPage()

  await openNote(page, main, hero)
  await page.click('.btn-backlinks')
  await pause(page, 500)
  await page.screenshot({ path: join(out, 'hero.png') })
  await alpine(page, 'app.backlinksOpen = false')
  await pause(page, 400)

  await page.click('.btn-mode-toggle')
  await page.waitForSelector('.cm-editor', { state: 'visible' })
  await page.click('.cm-content')
  await page.keyboard.press('Control+End')
  await page.keyboard.type('\n\nNext review: [[Launch')
  await pause(page, 900)
  await page.screenshot({ path: join(out, 'editor.png') })
  await page.keyboard.press('Escape')
  await page.click('.btn-mode-toggle')
  await pause(page, 400)

  await openNote(page, main, hero)
  await page.click('button[title^="Graph around this note"]')
  await page.waitForSelector('#graph-canvas canvas', { timeout: 10000 })
  await pause(page, 1500)
  await page.click('.graph-depth-btn[title="Wider"]')
  await page.waitForSelector('#graph-canvas canvas', { timeout: 10000 })
  await pause(page, 3500)
  await page.screenshot({ path: join(out, 'graph.png') })
  await page.keyboard.press('Escape')

  await openNote(page, main, hero)
  await page.click('.btn-share')
  await page.waitForSelector('.share-pop', { state: 'visible' })
  await pause(page, 300)
  await page.screenshot({ path: join(out, 'share.png') })
  await ctx.close()

  // ── Team instance (sub-folder mount, Remote-User set by the "proxy") ─────
  const tctx = await browser.newContext({ viewport, colorScheme: 'light', extraHTTPHeaders: { 'Remote-User': 'alice' } })
  const tpage = await tctx.newPage()
  await openNote(tpage, team, '/n/team/Website%20relaunch.md')
  await tpage.waitForSelector('.sb-instance', { state: 'visible' })
  await tpage.screenshot({ path: join(out, 'team-instance.png') })
  await tctx.close()

  // ── Walkthrough video: open → edit → wikilink → graph ────────────────────
  const vdir = join(work, 'video')
  const vctx = await browser.newContext({ viewport: { width: 1280, height: 800 }, colorScheme: 'light', recordVideo: { dir: vdir, size: { width: 1280, height: 800 } } })
  const v = await vctx.newPage()
  await openNote(v, walk, '/n/demo/Welcome.md')
  await pause(v, 1500)
  await v.click('#preview a.wikilink:has-text("Website relaunch")')
  await v.waitForSelector('h1:has-text("Website relaunch")')
  await pause(v, 2500)
  await v.click('.btn-mode-toggle')
  await v.waitForSelector('.cm-editor', { state: 'visible' })
  await v.click('.cm-content')
  await v.keyboard.press('Control+End')
  await v.keyboard.type('\n- [ ] Draft the launch post, see [[Launch check', { delay: 70 })
  await pause(v, 1200)
  await v.keyboard.press('Enter')
  await pause(v, 1500)
  await v.click('.btn-mode-toggle')
  await pause(v, 2500)
  await v.click('button[title^="Graph around this note"]')
  await v.waitForSelector('#graph-canvas canvas', { timeout: 10000 })
  await pause(v, 5000)
  const video = v.video()
  await vctx.close()
  const webm = await video.path()

  for (const f of ['demo.gif', 'demo.webm']) rmSync(join(out, f), { force: true })
  try {
    const gif = join(out, 'demo.gif')
    execFileSync('ffmpeg', ['-y', '-loglevel', 'error', '-i', webm, '-vf',
      'fps=6,scale=860:-1:flags=lanczos,split[a][b];[a]palettegen=max_colors=64:stats_mode=diff[p];[b][p]paletteuse=dither=bayer:bayer_scale=5:diff_mode=rectangle',
      gif])
    const size = statSync(gif).size
    console.log(`demo.gif ${(size / 1024 / 1024).toFixed(2)} MB`)
    if (size > GIF_MAX) {
      rmSync(gif)
      cpSync(webm, join(out, 'demo.webm'))
      console.log('GIF over 3 MB; kept docs/img/demo.webm instead')
    }
  } catch (err) {
    cpSync(webm, join(out, 'demo.webm'))
    console.log(`ffmpeg failed (${err.message.split('\n')[0]}); kept docs/img/demo.webm`)
  }
} finally {
  await browser.close()
}

for (const f of readdirSync(out)) console.log('docs/img/' + f)
process.exit(0)
