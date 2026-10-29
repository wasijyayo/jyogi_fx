import { useMutation, useQueryClient } from '@tanstack/react-query';

import {
  getGetMeQueryKey,
  useGetMe,
  useGetMarketStatus,
} from '../api/generated/api';
import { formatMarketStatus, formatPrice } from '../lib/format';
import styles from './Header.module.css';

// logout は POST /auth/logout（手書きエンドポイント。openapi.yaml の /auth 系は
// Discord OAuth の login/callback と同様に openapi に書かない）。
// サーバーが sessions の行を削除し Cookie を失効させる。
async function logout(): Promise<void> {
  const res = await fetch('/auth/logout', { method: 'POST' });
  if (!res.ok) {
    throw new Error(`logout failed: ${res.status}`);
  }
}

// 取引時間の状態はサーバー側で計算して返す（#99 指示）。ヘッダーが表示を最新に保つ
// ため、開催中は15秒・時間外は60秒でポーリングする（時間外に止めず60秒に残すのは、
// セッション開始への切り替わりを自動で拾うため）。
function marketStatusRefetchInterval(query: {
  state: { data?: { data: { open: boolean } } };
}): number {
  return query.state.data?.data.open ? 15_000 : 60_000;
}

export default function Header() {
  const queryClient = useQueryClient();
  const { data: statusData } = useGetMarketStatus({
    query: { refetchInterval: marketStatusRefetchInterval },
  });
  const { data: meData, isLoading: meLoading } = useGetMe();

  const logoutMutation = useMutation({
    mutationFn: logout,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: getGetMeQueryKey() });
    },
  });

  return (
    <header className={styles.header}>
      <div className={styles.left}>
        <a className={styles.brand} href="/">
          fxgame
        </a>
        <nav className={styles.nav}>
          <a href="/" className={styles.navLink}>
            通貨
          </a>
          <a href="/trade" className={styles.navLink}>
            取引
          </a>
          <a href="/demo" className={styles.navLink}>
            デモ
          </a>
        </nav>
      </div>
      <div className={styles.right}>
        {statusData && (
          <span className={styles.marketStatus}>{formatMarketStatus(statusData.data)}</span>
        )}
        {meLoading ? null : meData?.status === 200 ? (
          <>
            <span className={styles.user}>{meData.data.displayName}</span>
            <span className={styles.balance}>{formatPrice(meData.data.balance)}</span>
            <button
              type="button"
              className={styles.logout}
              onClick={() => logoutMutation.mutate()}
              disabled={logoutMutation.isPending}
            >
              ログアウト
            </button>
          </>
        ) : (
          <a className={styles.login} href="/auth/discord/login">
            Discordでログイン
          </a>
        )}
        {logoutMutation.isError && (
          <span className={styles.error} role="alert">
            ログアウトに失敗しました
          </span>
        )}
      </div>
    </header>
  );
}
