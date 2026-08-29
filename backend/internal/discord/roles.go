package discord

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

// AddGuildMemberRole はギルドメンバーにロールを付与する（design.md §6.10
// 「ロール自動付与」の最初の実装。#84「人生の勝者」ロールで使う）。
// PUT /guilds/{guild.id}/members/{user.id}/roles/{role.id}。
//
// 冪等: 既に付与済みのユーザーに対して呼んでも204が返るだけで副作用は増えない
// （Discord API自体の仕様）。呼び出し元がDB側の冪等フラグ更新に失敗しても、
// このAPI呼び出し自体は安全に再試行できる（game.LifeWinnerServiceのコメント参照）。
//
// design.md §6.10の注意点（「Bot自身のロールを、付与対象の称号ロールより上位に
// 配置する。忘れるとAPIが403を返す」）はDiscord側のロール設定でカバーする
// （コード側では対処しない）。
func AddGuildMemberRole(ctx context.Context, cfg MessagesConfig, guildID, userID, roleID string) error {
	url := fmt.Sprintf("%s/guilds/%s/members/%s/roles/%s", cfg.baseURL(), guildID, userID, roleID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bot "+cfg.BotToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	// Discord はロール付与成功時に 204 No Content を返す。
	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("discord add guild member role failed: status=%d body=%s", resp.StatusCode, body)
	}
	return nil
}
