import { createRootRoute, createRoute, createRouter, Outlet } from '@tanstack/react-router';

import Header from './components/Header';
import CurrencyListPage from './pages/CurrencyListPage';
import CurrencyPage from './pages/CurrencyPage';
import ReplayPage from './pages/ReplayPage';
import ReplayRankingPage from './pages/ReplayRankingPage';
import TradePage from './pages/TradePage';
import DemoPage from './demo/DemoPage';

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

// 過去チャートモード（#105）。開始・プレイ・結果をこの 1 画面で扱う。
const replayRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/replay',
  component: ReplayPage,
});

// 過去チャートモードのランキング（#105）。
const replayRankingRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/replay/ranking',
  component: ReplayRankingPage,
});

const routeTree = rootRoute.addChildren([
  indexRoute,
  currencyRoute,
  tradeRoute,
  demoRoute,
  replayRoute,
  replayRankingRoute,
]);

export const router = createRouter({ routeTree });

// TanStack Router の型登録。useParams 等の `from` に文字列リテラルを使えるようにする。
declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router;
  }
}
