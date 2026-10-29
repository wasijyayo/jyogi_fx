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

// databaseUrl は .issue-db から DATABASE_URL を得る。
// 環境変数 DATABASE_URL は見ない（.env の DATABASE_URL は本番 Neon を指すため、
// シェルに残っていると本番 DB を書き換え・削除してしまう）。念のため localhost 以外も拒否する。
function databaseUrl(): string {
  const file = join(repoRoot, '.issue-db');
  const m = readFileSync(file, 'utf8').match(/^DATABASE_URL=(.+)$/m);
  if (!m) throw new Error(`${file} に DATABASE_URL が見つかりません`);
  const url = m[1].trim();
  const host = new URL(url).hostname;
  if (host !== 'localhost' && host !== '127.0.0.1') {
    throw new Error(`E2E はローカル DB でしか動かしません（host=${host}）`);
  }
  return url;
}

// createTestSession は E2E 用のユーザー・セッションと、使い捨ての通貨を作る。
// 注文・決済は通貨の需給圧力（pressure）を書き換えるので、seed の通貨（JOG など）は使わない
// （AGENTS.md）。通貨の epoch_at を now() にして、価格が base_price 付近から始まるようにする。
// 残高は注文テストが価格変動に依存しないよう十分大きくする。
export async function createTestSession(
  discordId: string,
  sessionId: string,
  symbol: string,
): Promise<void> {
  const client = new Client({ connectionString: databaseUrl() });
  await client.connect();
  try {
    await client.query(
      `INSERT INTO currencies (symbol, name, base_price, drift, volatility, lambda, k, liquidity,
         pressure_at, off_session_scale, seed, max_leverage, fee_rate, epoch_at)
       VALUES ($1, $2, 100, 0, 0.001, 0.1, 0.01, 100000, now(), 0.05, 999, 10, 0.0005, now())`,
      [symbol, 'E2E テスト通貨'],
    );
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
// （AGENTS.md: trades → positions → price_ticks / events → sessions_auth → currencies → users）。
// 削除に失敗したら例外にして、後始末の失敗に気づけるようにする。
export async function cleanupTestSession(
  discordId: string,
  sessionId: string,
  symbol: string,
): Promise<void> {
  const client = new Client({ connectionString: databaseUrl() });
  await client.connect();
  try {
    const currency = `(SELECT id FROM currencies WHERE symbol = $1)`;
    await client.query('DELETE FROM trades WHERE user_id = $1', [discordId]);
    await client.query('DELETE FROM positions WHERE user_id = $1', [discordId]);
    await client.query(`DELETE FROM price_ticks WHERE currency_id = ${currency}`, [symbol]);
    await client.query(`DELETE FROM events WHERE currency_id = ${currency}`, [symbol]);
    await client.query('DELETE FROM sessions_auth WHERE id = $1', [sessionId]);
    await client.query('DELETE FROM currencies WHERE symbol = $1', [symbol]);
    await client.query('DELETE FROM users WHERE discord_id = $1', [discordId]);
  } finally {
    await client.end();
  }
}
