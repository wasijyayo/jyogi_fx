import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';

// Vitest の設定。環境は jsdom（React コンポーネントの描画に必要）。
// テストは `npm test`（vitest run）で実行する（#99 WEB-4）。
export default defineConfig({
  plugins: [react()],
  test: {
    environment: 'jsdom',
    include: ['src/**/*.test.{ts,tsx}'],
  },
});
