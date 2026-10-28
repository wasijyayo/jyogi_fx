#!/usr/bin/env bash
# start-issue.sh で用意した作業環境を片付ける（#93 AGENT-3）。
#
#   scripts/finish-issue.sh <issue番号>
#
# worktree を削除してから専用 DB を削除する。ブランチは削除しない（マージ後も残す運用）。
# worktree に未コミットの変更があると git worktree remove が失敗し、DB も残したまま止まる。

. "$(dirname "$0")/issue-common.sh"

echo "==> worktree: $worktree"
if [ -d "$worktree" ]; then
	git worktree remove "$worktree"
else
	echo "    見つからないのでスキップ"
fi

echo "==> DB: $db_name"
psql_admin "DROP DATABASE IF EXISTS $db_name WITH (FORCE)" >/dev/null

echo "片付けました（ブランチ $branch は残しています）。"
