import { createRootRoute, createRoute, createRouter, Outlet } from '@tanstack/react-router';

import Header from './components/Header';
import CurrencyListPage from './pages/CurrencyListPage';
import CurrencyPage from './pages/CurrencyPage';

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

const routeTree = rootRoute.addChildren([indexRoute, currencyRoute]);

export const router = createRouter({ routeTree });

// TanStack Router の型登録。useParams 等の `from` に文字列リテラルを使えるようにする。
declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router;
  }
}
