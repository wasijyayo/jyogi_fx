// E2E 用に Go サーバー（embed された SPA ごと）をビルドして起動する。
//
// - 本番の秘密情報（.env）は読まない。Discord の公開鍵・トークン等はダミーを入れる。
// - DB は worktree 専用 DB（.issue-db）に向ける（AGENTS.md: 共有の fxgame DB を使わない）。
// - GAME_ALWAYS_OPEN=true にして、時間に関係なく注文をテストできるようにする。

import { spawn, spawnSync } from 'node:child_process';
import { existsSync, readFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url)); // frontend/e2e/support
const repoRoot = resolve(here, '..', '..', '..');

// 環境変数 DATABASE_URL は見ない（.env の DATABASE_URL は本番 Neon を指すため、
// シェルに残っていると本番 DB に向いたサーバーを起動してしまう）。localhost 以外も拒否する。
function readDatabaseUrl() {
  const file = join(repoRoot, '.issue-db');
  if (!existsSync(file)) {
    throw new Error(`.issue-db が見つかりません（${file}）。scripts/start-issue.sh で作った worktree で実行してください`);
  }
  const m = readFileSync(file, 'utf8').match(/^DATABASE_URL=(.+)$/m);
  if (!m) throw new Error(`${file} に DATABASE_URL が見つかりません`);
  const url = m[1].trim();
  const host = new URL(url).hostname;
  if (host !== 'localhost' && host !== '127.0.0.1') {
    throw new Error(`E2E はローカル DB でしか動かしません（host=${host}）`);
  }
  return url;
}

const dbUrl = readDatabaseUrl();

const env = {
  ...process.env,
  DATABASE_URL: dbUrl,
  LOCAL_DATABASE_URL: dbUrl,
  // DISCORD_PUBLIC_KEY は32バイト（hex 64桁）のダミー公開鍵。署名検証は E2E では使わない。
  DISCORD_PUBLIC_KEY: '00'.repeat(32),
  TICK_SHARED_SECRET: 'e2e-tick-secret',
  DISCORD_BOT_TOKEN: 'e2e-dummy-bot-token',
  DISCORD_TICKER_CHANNEL_ID: '0',
  DISCORD_NOTIFY_CHANNEL_ID: '0',
  GAME_ALWAYS_OPEN: 'true',
  ADDR: ':8080',
};

const backendDir = join(repoRoot, 'backend');
// Windows では go build が .exe を付ける（spawn は .exe 付きの実ファイルを要求する）。
const bin = join(repoRoot, 'bin', `app-e2e${process.platform === 'win32' ? '.exe' : ''}`);

const build = spawnSync('go', ['build', '-o', bin, './cmd/app'], {
  cwd: backendDir,
  env,
  stdio: 'inherit',
});
if (build.status !== 0) {
  process.exit(build.status ?? 1);
}

const child = spawn(bin, ['serve'], {
  cwd: backendDir,
  env,
  stdio: 'inherit',
});

// Playwright が webServer を終了するときに Go サーバーも確実に止める。
const shutdown = () => {
  if (!child.killed) child.kill();
};
process.on('SIGINT', shutdown);
process.on('SIGTERM', shutdown);
child.on('exit', (code) => process.exit(code ?? 0));
