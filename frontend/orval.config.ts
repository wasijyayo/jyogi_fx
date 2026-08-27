import { defineConfig } from 'orval';

// api/openapi.yaml から TanStack Query hooks を生成する。
// 生成物は frontend/src/api/generated/（.gitignore 済み、make gen で再生成）。
// baseUrl は付けない: openapi.yaml のpathが "/api/me" のようにフルパスで、
// Go の embed で SPA と同一オリジン配信するため相対パスのままで良い（CLAUDE.md §2 配信形態）。
export default defineConfig({
  fxgame: {
    input: '../api/openapi.yaml',
    output: {
      mode: 'single',
      target: 'src/api/generated/api.ts',
      schemas: 'src/api/generated/model',
      client: 'react-query',
      httpClient: 'fetch',
    },
  },
});
