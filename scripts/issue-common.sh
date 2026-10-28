# start-issue.sh / finish-issue.sh から source される共通設定（#93 AGENT-3）。
# 単体では実行しない。
#
# 上書きできる環境変数:
#   BRANCH_PREFIX  ブランチ名の接頭辞（既定: git config user.name）
#   WORKTREE_ROOT  worktree を作る親ディレクトリ（既定: メインの checkout と同じ階層）
#   DB_CONTAINER   ローカル Postgres のコンテナ名（既定: compose の db サービスで 5432 番を公開しているもの）。
#                  接続先は localhost:5432 固定なので、5432 番を公開しているコンテナを指定すること

set -euo pipefail

if [ $# -ne 1 ] || ! [[ $1 =~ ^[0-9]+$ ]]; then
	echo "usage: $(basename "$0") <issue番号>" >&2
	exit 2
fi
issue=$1

# どの worktree から実行しても、メインの checkout を基準にする。
main_root=$(git worktree list --porcelain | sed -n '1s/^worktree //p')

branch="${BRANCH_PREFIX:-$(git config user.name)}/issue$issue"
worktree="${WORKTREE_ROOT:-$(dirname "$main_root")}/$(basename "$main_root")-issue$issue"

# Issue ごとに専用の DB を使い、並列でテストしてもデータや後始末が衝突しないようにする。
# 接続情報は compose.yaml のローカル Postgres と同じ（app/app）。
db_name="fxgame_issue$issue"
db_url="postgres://app:app@localhost:5432/$db_name?sslmode=disable"

# 別プロジェクトの Postgres を誤って掴まないよう、compose の db サービスに絞る。
db_container="${DB_CONTAINER:-$(docker ps --filter label=com.docker.compose.service=db --filter publish=5432 --format '{{.Names}}' | head -n 1)}"
if [ -z "$db_container" ]; then
	echo "ローカル Postgres のコンテナが見つかりません（docker compose up -d で起動してください）" >&2
	exit 1
fi

# 管理用の接続（postgres DB）で SQL を 1 文実行し、結果を値だけで返す。
psql_admin() {
	docker exec -i -e PGOPTIONS="-c client_min_messages=warning" "$db_container" psql -U app -d postgres -v ON_ERROR_STOP=1 -tAc "$1"
}
