import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// CLAUDE.md §3: Go の embed でバイナリに同梱する単一バイナリ配信のため、
// ビルド出力先は backend/web/ にする（.gitignore 済み。Go 側が embed する対象）。
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: '../backend/web',
    emptyOutDir: true,
  },
  server: {
    // npm run dev（Viteの開発サーバー）で起動したとき、/api と /auth は
    // 実際に go run で動いているバックエンドへ転送する。
    proxy: {
      '/api': 'http://localhost:8080',
      '/auth': 'http://localhost:8080',
    },
  },
});
