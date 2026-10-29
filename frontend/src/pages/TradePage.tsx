import { useGetMe } from '../api/generated/api';
import OrderForm from '../components/OrderForm';
import PositionsList from '../components/PositionsList';
import styles from './TradePage.module.css';

// TradePage は `/trade`。新規注文フォームと建玉一覧を出す（#46 WEB-2）。
// 注文フォーム・建玉一覧はログイン必須（#46 決定事項）。未ログイン時はフォームや
// 一覧を出さず、ログインを促す表示にする。
export default function TradePage() {
  const { data: meData, isLoading } = useGetMe();

  if (isLoading) {
    return <p className={styles.loading}>読み込み中...</p>;
  }

  if (meData?.status !== 200) {
    return (
      <main className={styles.main}>
        <h1 className={styles.title}>取引</h1>
        <p className={styles.loginPrompt}>注文・建玉の確認にはログインが必要です。</p>
        <a className={styles.loginLink} href="/auth/discord/login">
          Discordでログイン
        </a>
      </main>
    );
  }

  return (
    <main className={styles.main}>
      <h1 className={styles.title}>取引</h1>
      <OrderForm />
      <PositionsList />
    </main>
  );
}
