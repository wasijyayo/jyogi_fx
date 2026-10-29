import { defineConfig } from '@playwright/test';

// E2E（#46 WEB-2）設定。主要導線「ログイン → 注文 → 決済」の1本を e2e/ に置く。
//
// webServer は Go サーバー（embed された SPA ごと）を起動する。Discord の秘密情報や
// 本番 DB は使わず、worktree 専用 DB（.issue-db）に向ける（AGENTS.md）。
// テスト用ログインは Discord OAuth を通さず、テストが DB にセッションを作って
// Cookie を設定する方式（本番コードにテスト用ログインの抜け道は作らない）。
export default defineConfig({
  testDir: './e2e',
  timeout: 60_000,
  fullyParallel: false,
  workers: 1,
  use: {
    baseURL: 'http://localhost:8080',
    trace: 'on-first-retry',
  },
  webServer: {
    command: 'node e2e/support/startServer.mjs',
    url: 'http://localhost:8080/health',
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
  },
});
