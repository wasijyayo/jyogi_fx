package discord

// スラッシュコマンドの定義を置く唯一の場所（#29 の完了条件）。
//
// RegisterCommands（登録側）と internal/server の /interactions ハンドラ（実行側）の
// 両方がここの定数・一覧を参照する。コマンド名の文字列リテラルを両側に重複させると
// 片方だけ変更してズレる事故が起きるため、名前は必ず CommandXxx 定数を介す。
//
// 一覧は design.md §6.6 の MVP コマンド。
const (
	CommandRank      = "rank"
	CommandBalance   = "balance"
	CommandClaim     = "claim"
	CommandToday     = "today"
	CommandPositions = "positions"
	CommandPrice     = "price"
	CommandProfile   = "profile"
	// CommandPips は生涯獲得pipsランキング（#84。design.md §7.7の軸候補に
	// 追加した、ユーザーからの追加要望）。
	CommandPips = "pips"
)

// Discord のスラッシュコマンド関連の enum 値。
// 全種類ではなく、MVP コマンドの定義に必要な分だけ持つ。
const (
	commandTypeChatInput = 1 // ApplicationCommandType.CHAT_INPUT

	optionTypeString = 3 // ApplicationCommandOptionType.STRING
	optionTypeUser   = 6 // ApplicationCommandOptionType.USER
)

// CommandOption はスラッシュコマンドの引数 1 つ分の定義。
type CommandOption struct {
	Type        int    `json:"type"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Required    bool   `json:"required,omitempty"`
}

// Command は Discord に登録するスラッシュコマンド 1 つ分の定義。
type Command struct {
	Type        int             `json:"type,omitempty"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Options     []CommandOption `json:"options,omitempty"`
}

// Commands は登録するスラッシュコマンドの一覧（design.md §6.6 MVP）。
//
// RegisterCommands はこの一覧で Discord 側を**丸ごと上書き**する（bulk overwrite）。
// 差分登録ではないので、ここに無いコマンドは Discord 側から自動的に消える。
// 呼び出し側は常にこの変数をそのまま渡すこと。
//
// 実際のコマンド実行ロジック（残高計算・注文等）は #41 / #42 で internal/server 側に
// 実装する。ここでは登録に必要な名前・説明・引数の形だけを持つ。
// `/price` の通貨引数は currencies テーブルが動的（CLAUDE.md §5.3）なため、
// 選択肢を固定した choices は使わず自由入力の文字列にしている。
var Commands = []Command{
	{
		Type:        commandTypeChatInput,
		Name:        CommandRank,
		Description: "資金ランキングを表示します",
	},
	{
		Type:        commandTypeChatInput,
		Name:        CommandBalance,
		Description: "自分の残高を表示します",
	},
	{
		Type:        commandTypeChatInput,
		Name:        CommandClaim,
		Description: "セッション開始時の資金を受け取ります",
	},
	{
		Type:        commandTypeChatInput,
		Name:        CommandToday,
		Description: "本日の増減ランキングを表示します",
	},
	{
		Type:        commandTypeChatInput,
		Name:        CommandPositions,
		Description: "保有ポジションと含み損益を表示します（決済ボタン付き）",
	},
	{
		Type:        commandTypeChatInput,
		Name:        CommandPrice,
		Description: "通貨の現在価格・変動率を表示します",
		Options: []CommandOption{
			{
				Type:        optionTypeString,
				Name:        "currency",
				Description: "通貨コード（例: USD）",
				Required:    true,
			},
		},
	},
	{
		Type:        commandTypeChatInput,
		Name:        CommandProfile,
		Description: "プロフィールを表示します（省略時は自分）",
		Options: []CommandOption{
			{
				Type:        optionTypeUser,
				Name:        "user",
				Description: "表示するユーザー",
				Required:    false,
			},
		},
	},
	{
		Type:        commandTypeChatInput,
		Name:        CommandPips,
		Description: "生涯獲得pipsランキングを表示します",
	},
}
