import { readFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

import { Client } from 'pg';

// db.ts は E2E 用の DB 操作（#46 WEB-2）。Discord OAuth を通さず、テストが直接
// users / sessions_auth に行を作って Cookie セッションを成立させる
// （本番コードにテスト用ログインの抜け道は作らない）。
//
// 接続先は worktree 専用 DB（.issue-db）。共有の fxgame DB を使わない（AGENTS.md）。

const supportDir = dirname(fileURLToPath(import.meta.url)); // frontend/e2e/support
const repoRoot = resolve(supportDir, '..', '..', '..');

// databaseUrl は .issue-db から DATABASE_URL を得る。環境変数 DATABASE_URL があれば優先。
function databaseUrl(): string {
  if (process.env.DATABASE_URL) return process.env.DATABASE_URL;
  const file = join(repoRoot, '.issue-db');
  const m = readFileSync(file, 'utf8').match(/^DATABASE_URL=(.+)$/m);
  if (!m) throw new Error(`${file} に DATABASE_URL が見つかりません`);
  return m[1].trim();
}

// createTestSession は E2E 用のユーザーとセッションを作る。残高は注文テストが
// 価格変動（epoch_at からの BasePrice のランダムウォーク）に依存しないよう十分大きくする。
export async function createTestSession(discordId: string, sessionId: string): Promise<void> {
  const client = new Client({ connectionString: databaseUrl() });
  await client.connect();
  try {
    await client.query(
      `INSERT INTO users (discord_id, display_name, balance)
       VALUES ($1, $2, 100000)
       ON CONFLICT (discord_id) DO UPDATE SET display_name = EXCLUDED.display_name, balance = EXCLUDED.balance`,
      [discordId, 'E2E テストユーザー'],
    );
    await client.query(
      `INSERT INTO sessions_auth (id, user_id, expires_at)
       VALUES ($1, $2, now() + interval '1 day')
       ON CONFLICT (id) DO UPDATE SET expires_at = EXCLUDED.expires_at`,
      [sessionId, discordId],
    );
  } finally {
    await client.end();
  }
}

// cleanupTestSession は E2E が作ったデータを後始末する。子テーブルから順に消す
// （AGENTS.md: trades → positions → sessions_auth → users）。
export async function cleanupTestSession(discordId: string, sessionId: string): Promise<void> {
  const client = new Client({ connectionString: databaseUrl() });
  await client.connect();
  try {
    await client.query('DELETE FROM trades WHERE user_id = $1', [discordId]);
    await client.query('DELETE FROM positions WHERE user_id = $1', [discordId]);
    await client.query('DELETE FROM sessions_auth WHERE id = $1', [sessionId]);
    await client.query('DELETE FROM users WHERE discord_id = $1', [discordId]);
  } finally {
    await client.end();
  }
}
