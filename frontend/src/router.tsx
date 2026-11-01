import { createRootRoute, createRoute, createRouter, Outlet } from '@tanstack/react-router';

import Header from './components/Header';
import CurrencyListPage from './pages/CurrencyListPage';
import CurrencyPage from './pages/CurrencyPage';
import TradePage from './pages/TradePage';
import DemoPage from './demo/DemoPage';
import ReplayPage from './replay/ReplayPage';

// RootLayout は全画面共通のレイアウト（ヘッダー + 中身）。CLAUDE.md §2 の TanStack Router。
function RootLayout() {
  return (
    <>
      <Header />
      <Outlet />
    </>
  );
}

const rootRoute = createRootRoute({ component: RootLayout });

const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/',
  component: CurrencyListPage,
});

export const currencyRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/currencies/$symbol',
  component: CurrencyPage,
});

export const tradeRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/trade',
  component: TradePage,
});

// 発表用のデモ画面（#111）。サンプル相場とブラウザ内の状態だけで動き、API は呼ばない。
const demoRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/demo',
  component: DemoPage,
});

// 過去チャートモード（#105）。ログイン不要（ゲスト可）で 1 回 3 分。
const replayRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/replay',
  component: ReplayPage,
});

const routeTree = rootRoute.addChildren([indexRoute, currencyRoute, tradeRoute, demoRoute, replayRoute]);

export const router = createRouter({ routeTree });

// TanStack Router の型登録。useParams 等の `from` に文字列リテラルを使えるようにする。
declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router;
  }
}
