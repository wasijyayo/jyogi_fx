#!/usr/bin/env bash
# worktree 専用の DB（start-issue.sh が書いた .issue-db）に向けてコマンドを実行する（#93 AGENT-3）。
#
#   scripts/with-issue-db.sh go test ./...
#
# .issue-db が無い（start-issue.sh で作った worktree ではない）場合は、
# 共有のローカル DB で走ってしまわないよう失敗させる。

set -euo pipefail

file="$(git rev-parse --show-toplevel)/.issue-db"
if [ ! -f "$file" ]; then
	echo "$file がありません。scripts/start-issue.sh で作った worktree の中で実行してください" >&2
	exit 1
fi

set -a
. "$file"
set +a
exec "$@"
