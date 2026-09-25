# ローカル実装と動作確認

この文書は実装済みCLIの操作と、実機で確認した収集境界を記録する。完成形の設計は既存の設計文書を参照する。

## 構成

- Mac上でGo製 `imbue` / `imbued` を実行する。Collector、localhost API、Workerは同じDaemon内で動く。
- Docker ComposeでPostgreSQL 17のみを起動する。DBポートは `127.0.0.1:55432`。通常の停止でvolumeを削除しない。
- 保存先は `~/.imbue`（`--home` または `IMBUE_HOME` で変更可能）。対象リポジトリへ作業ログを置かない。
- PostgreSQLへ会話、Evidence index、完了Turn、処理位置、Job、Case revisionを保存する。Evidence本体と整形入出力は内容hash付きローカルblobに置く。
- ローカル収集cursorはatomic JSON、再送bufferはredaction済みのbatch JSONとする。初期実装ではSQLiteを追加せず、この小さな永続状態で再開を実現する。
- APIは `127.0.0.1:8788` のみ。ランダム生成token、Host検査、Origin拒否を適用する。
- ローカルDB passwordとAPI tokenは `auth/` の0600ファイルに保存する。Codexの認証情報は読み出さず、Codex自身にLuna専用homeのChatGPTログインを使わせる。

## 導入・起動

```sh
brew install go sqlc
open -a Docker
make build
./bin/imbue doctor
./bin/imbue init
./bin/imbue start
./bin/imbue status
```

Docker Desktopは事前に導入し、必要な初回同意と権限設定を済ませる。Go 1.26以降を使う。`init`の途中でDocker起動に失敗しても、設定を作り直さず `start` で再開できる。

設定は `~/.imbue/config/config.toml`。既定の `observe_all=true` では、切り替え時点より後にCodexで開始・再開した全作業ディレクトリをsession metadataから自動検出する。過去の全履歴は突然取り込まず、既存ログの末尾にcursorを置く。会話ごとに実パスを保持し、Lunaの判断事例もその作業ディレクトリのscopeで検証する。

`imbue scope repository PATH`で一つのディレクトリに限定し、`imbue scope all`で全Codexワークスペースへ戻せる。`exclude_sessions`、`exclude_paths`、`redact_patterns` で対象外セッション・秘密ファイル名・追加マスク規則を設定する。範囲変更後はDaemonを再起動する。

```sh
./bin/imbue stop
./bin/imbue start
```

`stop`はDBも停止するがvolumeとEvidenceは残す。Macログイン時の自動起動は登録しない。バックグラウンドDaemonのログは `~/.imbue/state/daemon.log`。

## Observe Modeの互換性

収集形式の実機確認対象は **Codex CLI 0.154.0-alpha.6.2 / 0.155.0-alpha.2.6**。このMacでは `/Applications/ChatGPT.app/Contents/Resources/codex` を使う。PATH上の古いCLIとは別なので、`codex_binary`で明示する。

収集元はCodexの `sessions/` と `archived_sessions/` のrollout JSONL。実際に確認した `session_meta` と `event_msg.item_completed`、`task_started`、`task_complete`、`turn_aborted` を読む。これは公開安定APIではないため、未検証バージョンは警告を出し、推測で取り込まない。

| 取得対象 | 取得方法・意味 |
|---|---|
| ユーザー発言 | `UserMessage`。システム・開発者指示を会話として保存しない |
| Agentの回答・進捗 | `AgentMessage`。進捗だけでTurn完了にしない |
| コマンド実行・検証結果 | `CommandExecution`のコマンド、出力、終了コード。テスト成功や本人の承認を勝手に付与しない |
| コード差分 | `FileChange`に含まれる変更。リポジトリ全体を走査・複製しない |
| MCP等の操作 | 対応する完了itemに実際に記録された引数・結果 |
| 完了Turn | `task_complete.turn_id`の一意性で集計。tool・進捗・サブエージェントの数は加算しない |
| 適用範囲 | sessionのcwdを実パスに解決し、会話ごとの作業ディレクトリとして保存。単一scope時だけ設定repositoryと照合 |

取得できないもの：ログに残らない外部編集、Codexを経由しないテスト実行、欠落・切り詰め済みのtool出力、非公開の内部思考、暗号化されたreasoning。欠けた完了IDは `boundary.missing` として保存し、Turn数へ加えない。観測後のgit diffを過去Turnの差分と見なすこともしない。

読みかけの最終行は持ち越す。sourceの書き換え・切り詰めや異常な巨大行は警告として止め、cursorを進めない。DB停止中も先にredaction済みbufferへ保存できる。buffer保存後にcursorを進め、blob保存とDB transaction成功後にbufferを消す。再送はsource位置に基づくEvidence IDと会話内Turn IDで重複排除する。

## 確認コマンド

```sh
./bin/imbue collect
./bin/imbue conversations
./bin/imbue turns CONVERSATION_ID
./bin/imbue evidence list CONVERSATION_ID
./bin/imbue evidence show EVIDENCE_ID
```

`collect`はDaemon停止中にも利用できる。DBが使えない間はbuffer保存まで行う。`status`には会話ごとの完了件数、未処理件数、処理位置、buffer件数、直近の収集警告が出る。

秘密情報はbuffer保存前とLuna入力前にマスクする。既知のAPI key形式、token、password、秘密鍵、認証ヘッダー、メール等と追加regexを検査する。除外パスに言及したitemは本文全体を除外マーカーに置き換える。一般的なパターン検出なので、独自形式の秘密値は追加規則・セッション除外を指定する。

## Luna整形

### ターミナルUI

`./bin/imbue` または `./bin/imbue ui` で全画面のTUIを起動する。既存の認証付きローカルAPIに接続し、3秒ごとに状態を取得する。通常の閲覧ではLunaを実行しない。

| キー | 動作 |
|---|---|
| `1`〜`4` / `Tab` | ホーム・会話・判断事例・処理履歴の切り替え |
| `↑↓` / `j k` | 一覧の選択、詳細のスクロール |
| `Enter` / `Esc` | 詳細を開く / 一覧へ戻る |
| `g` | 判断事例の根拠を表示 |
| `v` | 専用homeのLunaアカウントを推論なしで検証 |
| `r` | 状態の再取得 |
| `s` | 未接続時に既存の `start` コマンドでサービス起動 |
| `Space` | 脳の発光アニメーション切り替え |
| `q` / `Ctrl+C` | UIを終了し、元のターミナルへ戻る |

シアン・紫・マゼンタのBrailleドットで脳を描く。40列×12行以上で表示でき、横幅に応じて配置を切り替える。100列×32行以上を推奨する。端末の色対応を検出し、`NO_COLOR`も尊重する。アカウントの固定設定と、その場で検証した認証結果は区別して表示する。接続障害時は最後に取得した表示を保ち、未接続と取得時刻を示す。UI終了は常駐プロセスを停止しない。

外部由来の文字列はANSI/OSC・制御文字を除去してから描画する。TUIは新しいHTTP経路や外部公開ポートを追加しない。編集・再試行は引き続き既存CLIから行う。

### 専用アカウントと自動整形

初期状態は `curator_enabled=false`。収集だけなら10Turnに達してもLunaは起動しない。段階3の検証後に明示的に有効化する。

```sh
./bin/imbue curator login --email YOUR_ACCOUNT_EMAIL
./bin/imbue curator account
./bin/imbue curator check
./bin/imbue curator enable
./bin/imbue stop
./bin/imbue start
```

`curator check`は合成の短い判断事例で実際のLuna実行・構造化出力を確認する。ChatGPTの利用枠を使う。

`curator login`は公式ブラウザログインを `curator_codex_home`（既定 `~/.imbue/auth/curator-codex`）で起動する。作業用Codexのhome・ログインは変更しない。`curator_account_email`で実行を許可するアカウントを固定し、`curator account`は推論せず認証先を検証する。作業用Codexを別アカウントに切り替えても、Lunaはこの専用ログインを使う。専用認証が期限切れなら、同じコマンドで専用アカウントに再ログインする。

自動起動は同じ会話の未処理完了Turnが `curator_threshold`（既定10）以上になった場合のみ。無操作やセッション終了では起動しない。手動なら10件未満も処理できる。

```sh
./bin/imbue curate CONVERSATION_ID
./bin/imbue jobs
./bin/imbue retry JOB_ID
./bin/imbue case list
./bin/imbue case show CASE_ID
./bin/imbue case edit CASE_ID --judgment '訂正後の判断' --reason '訂正理由'
./bin/imbue case edit CASE_ID --status held --reason '根拠の追加確認が必要'
```

同一会話のJobを直列化し、対象範囲をenqueue時に固定する。処理中に増えたTurnは次回へ持ち越す。根拠ID・適用範囲・Case versionを検証してCase保存と処理位置更新を一つのtransactionで確定する。失敗時は位置を進めず、lease失効後の再試行に対応する。再試行上限を超えたJobはquarantineし、`retry`で再開できる。

Lunaは空の専用directoryから `codex app-server --stdio` を起動し、`gpt-5.6-luna` のephemeral threadを実行する。認証確認と推論を同一プロセスで行い、thread作成前・Turn開始直前・結果受理前の `account/read` で固定メールとの一致を確認する。Turn中の認証変更通知も処理を中断する。認証不一致・未ログイン・API key認証ではEvidenceを送信せず、Jobを `blocked_by_account` で保留する。保留Jobは再ログイン・確認後に `retry` で再開する。作業用認証への自動fallbackはない。

専用homeのfile credential storeとChatGPTログインを強制し、API keyや別homeを指定する環境変数を継承しない。作業用homeとの共有・配下配置、認証/configファイルのsymlinkを拒否し、ログインと推論は専用homeのロックで直列化する。作業用設定・AGENTS.mdを読み込まず、MCP・Plugin・Skill・Memory・外部操作機能を無効にする。timeoutと出力上限を設け、予期しないtoolイベントやモデルの振り替えは実行を打ち切る。

整形結果は根拠付きのannotationとして扱う。SFT等の用途候補は検証済みの教師ラベルを意味しない。Fireworks、追加学習、学習済みモデル実行は未実装。

## 開発・検証

```sh
make generate
make test
IMBUE_TEST_HOME="$HOME/.imbue" go test -race ./...
go vet ./...
```

DB統合テストは指定DB内にランダム名の隔離schemaを作成し、終了時にそのschemaのみ削除する。実ユーザーの会話・Caseには書き込まない。Lunaの実実行は通常のテストから分離する。

10Turnからの実Luna実行も検証する場合は、明示的に次を実行する。合成の作業ログを使用し、ChatGPTの利用枠を消費する。

```sh
IMBUE_TEST_HOME="$HOME/.imbue" IMBUE_LIVE_LUNA=1 \
  go test -v ./internal/curator -run TestIntegrationLiveLunaAtTenTurns -count=1
```

## 2026-09-17の実機確認

| 項目 | 確認結果 |
|---|---|
| Go / sqlc | HomebrewでGo 1.27.1、sqlc 1.31.1を導入 |
| Docker | Engine 28.0.1、Compose 2.33.1。PostgreSQLのみを起動 |
| Codex | 収集形式0.154.0-alpha.6.2 / 0.155.0-alpha.2.6、Luna実行0.155.0-alpha.2.6。ChatGPTログインと実行成功を確認 |
| 収集対象 | このImbueリポジトリ。一つの実会話の完了3Turnを取得 |
| Evidence | ユーザー発言、Agent回答・進捗、コマンド結果、コード差分、tool、除外マーカーを保存 |
| 実会話の整形 | 3Turnを手動投入し、根拠付きCaseを1件保存。Job成功後に処理位置を更新 |
| 自動起動 | 隔離DBの合成10Turnで自動enqueue→実Luna→Case保存が成功。9Turn時点では起動しない |
| 非永続実行 | 実Lunaが返したthread IDのrolloutがsessions/archived_sessionsに存在しないことを検査 |
| 再起動 | DaemonとDBを停止・再起動して完了件数、処理位置、Caseが維持されることを確認 |
| 最終設定 | 自動整形有効、閾値10、APIとDBは127.0.0.1限定、専用保存先は0700、credential fileは0600 |

自動テストでは、未完了行の再開、秘密情報のbuffer前マスク、除外セッション・サブエージェント、未知バージョン拒否、境界欠損、DB障害時のbuffer保持、commit後の再送、会話間の件数分離、10Turn時の無効状態、重複enqueue、処理中の新規Turn持ち越し、架空根拠拒否、lease回復、訂正・保留・版競合、長いEvidenceの無欠損分割と関連発言の保持、待機中Jobへの最新除外設定適用、HTTP認証境界を確認した。`go test -race ./...`（DB統合テスト有効）、`go vet ./...`、ビルドを通過している。

2026-09-18にLunaの実行方式を専用認証のApp Serverに変更し、同梱CLI `0.155.0-alpha.2.6` で認証先確認と実Lunaの構造化出力を検証した。成功判定は `turn/completed` の成功状態、実行前後の認証一致、出力schema・根拠検証を組み合わせる。監査blobには検証済みメール、実行thread ID、CLIバージョン、使用量、イベント種別を保存する。認証不一致・途中変更・API key・未ログイン時の送信阻止と、作業用認証環境変数の非継承を自動テストする。収集側はrollout形式 `0.154.0-alpha.6.2` と `0.155.0-alpha.2.6` を検証済みとして扱い、それ以外は取り込まない。

対応Codex版は意図的に固定している。アップデート後はログ形式と隔離実行の互換性を再確認する。長いEvidenceは分割するが、既存Caseや関連発言の集合自体が入力予算を超える場合は、切り捨てずJobを保留・再試行対象にする。設定の入力上限か対象範囲を調整してから再実行する。

公式参照：[Codex App Server](https://learn.chatgpt.com/docs/app-server)、[Codex設定仕様](https://learn.chatgpt.com/docs/config-file/config-reference)。設定とイベント形式の最終確認は同梱CLIの実行結果を正とする。
