import { expect, test } from '@playwright/test';

import { cleanupTestSession, createTestSession } from './support/db';

// ログイン → 注文 → 決済（#46 WEB-2 の主要導線 E2E）。
//
// ログインは Discord OAuth を通さず、テストが DB にユーザーとセッションを作り
// Cookie を設定する方式（本番コードにテスト用ログインの抜け道は作らない）。
const discordId = `e2e-${Date.now()}`;
const sessionId = `e2e-session-${Date.now()}`;

test.beforeAll(async () => {
  await createTestSession(discordId, sessionId);
});

test.afterAll(async () => {
  await cleanupTestSession(discordId, sessionId);
});

test('ログイン → 注文 → 決済', async ({ context, page }) => {
  // セッション Cookie を設定してログイン済みにする。
  await context.addCookies([
    { name: 'session', value: sessionId, url: 'http://localhost:8080' },
  ]);

  await page.goto('/trade');

  // ログイン済みなので注文フォームと建玉一覧が表示される。
  await expect(page.getByRole('heading', { name: '新規注文' })).toBeVisible();
  await expect(page.getByRole('heading', { name: '建玉一覧' })).toBeVisible();
  await expect(page.getByText('保有ポジションはありません。')).toBeVisible();

  // 通貨 JOG を選び、数量・レバレッジを入力して買い注文を出す。
  await page.getByLabel('通貨').selectOption('JOG');
  await page.getByLabel('数量').fill('10');
  await page.getByLabel('レバレッジ').fill('2');
  await page.getByRole('button', { name: '買い注文' }).click();

  // 注文成立の表示と、建玉一覧への反映を確認する。
  await expect(page.getByText('注文が成立しました')).toBeVisible();
  const row = page.locator('li', { hasText: 'JOG' }).first();
  await expect(row).toContainText('含み損益');

  // 決済（確認 → はい）。
  await row.getByRole('button', { name: '決済' }).click();
  await page.getByRole('button', { name: 'はい' }).click();

  // 決済後は建玉が無くなる。
  await expect(page.getByText('保有ポジションはありません。')).toBeVisible();
});
