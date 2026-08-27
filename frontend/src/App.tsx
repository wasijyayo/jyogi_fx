import { useGetMe } from './api/generated/api';

// WS-8: /api/me を呼び出し、ログイン中のDiscord表示名を表示する。
// 生成された useGetMe は HTTPステータスに関わらず例外を投げないため
// （fetchクライアントの実装上、401も正常応答として resolve する）、
// data.status を見てログイン済みかどうかを判定する。
function App() {
  const { data, isLoading } = useGetMe();

  if (isLoading) {
    return <p>読み込み中...</p>;
  }

  if (data?.status !== 200) {
    return (
      <p>
        <a href="/auth/discord/login">Discordでログイン</a>
      </p>
    );
  }

  return <p>ようこそ、{data.data.displayName} さん</p>;
}

export default App;
