#!/usr/bin/env bash
# Issue ごとの作業環境を 1 コマンドで用意する（#93 AGENT-3）。
#
#   scripts/start-issue.sh <issue番号>
#
# 1. origin/main から <BRANCH_PREFIX>/issue<番号> ブランチの worktree を作る
# 2. ローカル Postgres に専用 DB（fxgame_issue<番号>）を作り、マイグレーションを適用する
# 3. worktree 直下に .issue-db（専用 DB の接続先）を書く。.env はコピーしない
# 4. コード生成（oapi-codegen・sqlc・orval）と npm ci を実行する
#
# 何度実行しても壊れない（既にあるものは作り直さない）。
# 専用 DB でテストを走らせるには、worktree の中で scripts/with-issue-db.sh を使う。

. "$(dirname "$0")/issue-common.sh"

echo "==> worktree: $worktree ($branch)"
git fetch -q origin
if [ -d "$worktree" ]; then
	echo "    既にあるのでそのまま使う"
elif git show-ref --verify --quiet "refs/heads/$branch"; then
	git worktree add "$worktree" "$branch"
elif git show-ref --verify --quiet "refs/remotes/origin/$branch"; then
	git worktree add --track -b "$branch" "$worktree" "origin/$branch"
else
	git worktree add --no-track -b "$branch" "$worktree" origin/main
fi

echo "==> DB: $db_name"
if [ "$(psql_admin "SELECT 1 FROM pg_database WHERE datname = '$db_name'")" = 1 ]; then
	echo "    既にあるのでそのまま使う"
else
	psql_admin "CREATE DATABASE $db_name" >/dev/null
fi
(cd "$worktree/backend" && migrate -path db/migrations -database "$db_url" up)

# テストは DATABASE_URL（未設定ならローカルの fxgame DB）を、serve は DATABASE_URL →
# LOCAL_DATABASE_URL の順に読むため、両方を専用 DB に向ける。
# この worktree には .env が無いので、DATABASE_URL が本番を指すことはない。
cat >"$worktree/.issue-db" <<EOF
DATABASE_URL=$db_url
LOCAL_DATABASE_URL=$db_url
EOF

echo "==> コード生成"
(cd "$worktree" && oapi-codegen -config api/oapi-codegen.yaml api/openapi.yaml)
(cd "$worktree/backend" && sqlc generate)
(cd "$worktree/frontend" && { [ -d node_modules ] || npm ci; } && npm run gen)

cat <<EOF

準備できました。
  worktree: $worktree
  branch:   $branch
  DB:       $db_name

テストは専用 DB に向けて実行する:
  cd "$worktree/backend" && ../scripts/with-issue-db.sh go test ./...
後片付け:
  scripts/finish-issue.sh $issue
EOF
