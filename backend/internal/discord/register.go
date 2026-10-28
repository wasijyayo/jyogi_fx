package discord

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const defaultAPIBaseURL = "https://discord.com/api/v10"

// RegisterCommandsConfig はスラッシュコマンド登録に必要な設定。
type RegisterCommandsConfig struct {
	// BotToken は Authorization ヘッダに使う Bot トークンの値（"Bot " 接頭辞は付けない）。
	BotToken string
	// ApplicationID は登録先アプリケーションの ID（Discord Developer Portal の
	// Application ID。DISCORD_CLIENT_ID と同じ値）。
	ApplicationID string
	// GuildID を指定するとギルド限定登録になる（反映が即時・開発向け、issue #29）。
	// 空文字ならグローバル登録（反映に最大1時間かかる・本番向け）。
	GuildID string

	// APIBaseURL はテストでのエンドポイント差し替え用。空文字なら本物の Discord API を使う。
	APIBaseURL string
}

func (c RegisterCommandsConfig) baseURL() string {
	if c.APIBaseURL != "" {
		return c.APIBaseURL
	}
	return defaultAPIBaseURL
}

// commandsURL は登録先の PUT エンドポイントを組み立てる。
// GuildID の有無でグローバル / ギルド限定を切り替える（design.md §6.6, issue #29）。
func (c RegisterCommandsConfig) commandsURL() string {
	base := c.baseURL()
	if c.GuildID != "" {
		return fmt.Sprintf("%s/applications/%s/guilds/%s/commands", base, c.ApplicationID, c.GuildID)
	}
	return fmt.Sprintf("%s/applications/%s/commands", base, c.ApplicationID)
}

// RegisterCommands は Discord にスラッシュコマンド定義を一括登録する。
//
// PUT は bulk overwrite（丸ごと置き換え）なので、渡した一覧に無いコマンドは
// Discord 側から自動的に削除される。差分登録ではないため、呼び出し側は常に
// 完全な一覧（Commands）を渡すこと。
func RegisterCommands(ctx context.Context, cfg RegisterCommandsConfig, commands []Command) error {
	body, err := json.Marshal(commands)
	if err != nil {
		return fmt.Errorf("marshal commands: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, cfg.commandsURL(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bot "+cfg.BotToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("discord register commands failed: status=%d body=%s", resp.StatusCode, respBody)
	}
	return nil
}
