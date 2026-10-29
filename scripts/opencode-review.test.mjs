import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, writeFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { main, parseReview, excluded, dedicatedComment, modelConfig, MARKER } from './opencode-review.mjs';

test('secret paths and renamed secret files are excluded', () => {
  for (const name of ['.env', 'backend/.env.production', '.issue-db', 'keys/private.pem', 'auth.json']) assert.ok(excluded(name));
  assert.equal(excluded('backend/internal/game/trade.go'), false);
});

test('only completed text is accepted, not reasoning, API errors, or partial responses', () => {
  const text = JSON.stringify({ type: 'text', part: { text: '指摘なし' } });
  const finish = JSON.stringify({ type: 'step_finish', part: { reason: 'stop' } });
  assert.equal(parseReview(`${text}\n${finish}`), '指摘なし');
  assert.throws(() => parseReview(text), /completed/);
  assert.throws(() => parseReview(`${text}\n${finish}\n{"type":"error"}`), /API/);
  assert.throws(() => parseReview(finish), /completed/);
  assert.throws(() => parseReview(JSON.stringify({ type: 'error', error: { data: { statusCode: 429, message: 'DO_NOT_LOG_RAW_PROVIDER_MESSAGE' } } })), /quota\/rate limit.*429/);
  assert.throws(() => parseReview(JSON.stringify({ type: 'error', error: { data: { statusCode: 401 } } })), /authentication\/permission.*401/);
});

test('comment selection preserves other bots and unmarked Preview comments', () => {
  const comments = [
    { id: 1, user: { id: 1, type: 'User' }, body: MARKER },
    { id: 2, user: { id: 2, type: 'Bot' }, body: MARKER },
    { id: 3, user: { id: 3, type: 'Bot' }, body: '### プレビュー' },
    { id: 4, user: { id: 3, type: 'Bot' }, body: `${MARKER}\nreview` },
  ];
  assert.equal(dedicatedComment(comments, 3).id, 4);
  assert.equal(dedicatedComment(comments, 99), undefined);
  assert.deepEqual(modelConfig().permission, { '*': 'deny' });
  assert.deepEqual(modelConfig().enabled_providers, ['opencode-go']);
});

// Exercise the complete GitHub orchestration without credentials or model calls.
test('publication, rerun, stale result, and provider failure', async t => {
  for (const scenario of ['create', 'update', 'stale', 'base-changed', 'provider-error', 'secret-output', 'large']) {
    await t.test(scenario, async () => {
      const dir = await mkdtemp(path.join(tmpdir(), 'review-test-'));
      const priorFetch = globalThis.fetch;
      const saved = { ...process.env };
      const pr = { number: 7, state: 'open', draft: false, title: 'Fix trade #103', body: '',
        head: { sha: 'a'.repeat(40), repo: { full_name: 'owner/repo' } }, base: { sha: 'b'.repeat(40) } };
      await writeFile(path.join(dir, 'event.json'), JSON.stringify({ pull_request: pr }));
      process.env.GITHUB_EVENT_PATH = path.join(dir, 'event.json');
      process.env.GITHUB_REPOSITORY = 'owner/repo';
      process.env.GH_TOKEN = 'test-github-secret';
      process.env.OPENCODE_GO_API_KEY = 'test-model-secret';
      delete process.env.GITHUB_STEP_SUMMARY;
      let prReads = 0;
      const writes = [];
      let prompt = '';
      globalThis.fetch = async (url, options) => {
        const pathname = new URL(url).pathname;
        let value;
        if (options.method) { writes.push({ pathname, ...options }); value = {}; }
        else if (pathname.endsWith('/pulls/7')) {
          prReads++;
          value = structuredClone(pr);
          if (prReads > 1 && scenario === 'stale') value.head.sha = 'c'.repeat(40);
          if (prReads > 1 && scenario === 'base-changed') value.base.sha = 'c'.repeat(40);
        } else if (pathname.endsWith('/files')) value = [
          { filename: '.env', patch: 'NEVER_SEND_THIS_SECRET' },
          { filename: 'safe.txt', previous_filename: '.env.old', patch: 'RENAMED_SECRET' },
          { filename: 'trade.go', patch: scenario === 'large' ? 'x'.repeat(360_000) : '+ fix' },
          { filename: 'image.png' },
        ];
        else if (pathname.includes('/contents/')) value = { encoding: 'base64', content: Buffer.from('Trusted base rules').toString('base64') };
        else if (pathname.endsWith('/issues/103')) value = { title: 'Trade', body: '決定事項' };
        else if (pathname.endsWith('/issues/103/comments')) value = [{ user: { login: 'owner' }, body: '追加決定' }];
        else if (pathname.endsWith('/issues/7/comments')) value = [
          { id: 10, user: { id: 100, login: 'github-actions[bot]', type: 'Bot' }, body: 'Preview URL' },
          ...(scenario === 'update' ? [{ id: 11, user: { id: 100, login: 'github-actions[bot]', type: 'Bot' }, body: MARKER }] : []),
        ];
        else throw new Error(`Unexpected route ${pathname}`);
        return { ok: true, json: async () => value };
      };
      try {
        const execute = () => main({ review: async input => {
          prompt = input;
          if (scenario === 'provider-error') throw new Error('Provider unavailable');
          return scenario === 'secret-output' ? process.env.GH_TOKEN : '指摘なし @someone';
        } });
        if (['provider-error', 'secret-output', 'large'].includes(scenario)) await assert.rejects(execute);
        else await execute();
        assert.equal(writes.length, ['create', 'update'].includes(scenario) ? 1 : 0);
        if (writes.length) {
          assert.equal(writes[0].method, scenario === 'update' ? 'PATCH' : 'POST');
          assert.match(writes[0].pathname, scenario === 'update' ? /comments\/11$/ : /issues\/7\/comments$/);
          const body = JSON.parse(writes[0].body).body;
          assert.ok(body.startsWith(MARKER));
          assert.ok(body.includes(pr.head.sha));
          assert.ok(body.includes('image.png'));
          assert.ok(body.includes('＠someone'));
        }
        assert.ok(!prompt.includes('NEVER_SEND_THIS_SECRET'));
        assert.ok(!prompt.includes('RENAMED_SECRET'));
        if (prompt) assert.ok(prompt.includes('追加決定'));
      } finally {
        globalThis.fetch = priorFetch;
        for (const key of Object.keys(process.env)) if (!(key in saved)) delete process.env[key];
        Object.assign(process.env, saved);
        await rm(dir, { recursive: true });
      }
    });
  }
});
