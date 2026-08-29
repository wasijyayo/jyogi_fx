package discord

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// MessagesConfig は Bot トークンでチャンネルにメッセージを投稿・編集するための設定
// （#43 NOTIFY-1: 市場ティッカー。design.md §6.4）。
// RegisterCommandsConfig と役割が近いが、対象がスラッシュコマンド定義ではなく
// 通常のチャンネルメッセージのため型を分けている。
type MessagesConfig struct {
	// BotToken は Authorization ヘッダに使う Bot トークンの値（"Bot " 接頭辞は付けない）。
	BotToken string

	// APIBaseURL はテストでのエンドポイント差し替え用。空文字なら本物の Discord API を使う。
	APIBaseURL string
}

func (c MessagesConfig) baseURL() string {
	if c.APIBaseURL != "" {
		return c.APIBaseURL
	}
	return defaultAPIBaseURL
}

// CreateMessage はチャンネルに新規メッセージを投稿し、投稿したメッセージ ID を返す
// （design.md §6.4「専用チャンネルに1つのメッセージを投稿し」）。
// components は省略可（nilなら"components"キー自体を送らない）。市場ティッカーの
// 買う/売るボタン常設（issue #78）で使う。
func CreateMessage(ctx context.Context, cfg MessagesConfig, channelID, content string, components []ActionRow) (messageID string, err error) {
	url := fmt.Sprintf("%s/channels/%s/messages", cfg.baseURL(), channelID)
	return sendChannelMessage(ctx, cfg, http.MethodPost, url, content, components)
}

// EditMessage は既存メッセージの本文を書き換える
// （design.md §6.4「毎分のtickでそれを編集し続ける」。新規投稿ではなく編集なので
// チャンネルが荒れない）。
//
// components は**呼び出しのたびに明示的に指定すること**。Discordのメッセージ編集は
// 「渡さなかったフィールドは変更しない」ではなく、componentsキーを省略すると既存の
// ボタンが消える可能性があるため、ボタンを維持したいなら毎回同じcomponentsを渡す
// （issue #78）。付けない場合はnilを渡す。
func EditMessage(ctx context.Context, cfg MessagesConfig, channelID, messageID, content string, components []ActionRow) error {
	url := fmt.Sprintf("%s/channels/%s/messages/%s", cfg.baseURL(), channelID, messageID)
	_, err := sendChannelMessage(ctx, cfg, http.MethodPatch, url, content, components)
	return err
}

// DeleteMessage はメッセージを削除する。
//
// 主な用途は game.TickerService.Update の補償処理: CreateMessage（投稿）自体は
// 成功したのに、その直後の ticker_msg_id の保存が失敗した場合、投稿した
// メッセージをここで削除して「まだ投稿していない」状態に巻き戻す。これをしないと
// 次tickでも ticker_msg_id が空のままなので再度新規投稿してしまい、孤児メッセージが
// 積み重なって「新規投稿が増えない」という完了条件が崩れる。
func DeleteMessage(ctx context.Context, cfg MessagesConfig, channelID, messageID string) error {
	url := fmt.Sprintf("%s/channels/%s/messages/%s", cfg.baseURL(), channelID, messageID)

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bot "+cfg.BotToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Discord は削除成功時に 204 No Content を返す。
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("discord delete message failed: status=%d body=%s", resp.StatusCode, body)
	}
	return nil
}

// sendChannelMessage は POST（新規投稿）/ PATCH（編集）の共通処理。
//
// レート制限（issue #43「Discord API のレート制限に注意」）に対する明示的な
// リトライは行わない。呼び出し元（game.TickerService.Update）のエラーは
// TickService.Tick がログするだけで tick 全体を失敗させない設計にしてあり、
// 失敗しても次の tick（1分後）で自然に再試行される（冪等）ため、ここで
// リトライループを持つ必要がない（design.md §11.3・CLAUDE.md §5.5と同じ考え方）。
func sendChannelMessage(ctx context.Context, cfg MessagesConfig, method, url, content string, components []ActionRow) (string, error) {
	body, err := json.Marshal(struct {
		Content    string      `json:"content"`
		Components []ActionRow `json:"components,omitempty"`
	}{Content: content, Components: components})
	if err != nil {
		return "", fmt.Errorf("marshal message body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bot "+cfg.BotToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("discord %s message failed: status=%d body=%s", method, resp.StatusCode, respBody)
	}

	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		return "", fmt.Errorf("decode message response: %w", err)
	}
	return out.ID, nil
}
