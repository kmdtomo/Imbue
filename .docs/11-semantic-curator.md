# Semantic Curator（Luna）

## 初期のローカル構成

初期実装は一人のPCで動かす。本文のPlatformは、初期には同じPC上のGo API・Worker・Model Gatewayを指す。PostgreSQL（pgx＋sqlc）とローカルファイルを正本とし、ジョブはPostgreSQLのテーブルで管理する。クラウドホスティング、Object Storage、外部Queue、サービス認証・課金は後段とする。CodexおよびFireworksによる外部AI実行・学習は維持するため、完全オフライン構成ではない。具体的な保存・認証・起動境界は[技術アーキテクチャ](12-technical-architecture.md)を正本とする。


## 役割

機械的に集約したEvidence Packetを、GPT-5.6 LunaでLearning Caseへ整形する。Curator JobはImbue Platformが管理するが、モデル実行はユーザーPC上のCodexを通して行う。Imbue独自のLLM RuntimeやAPI実行へ置き換えず、現在のユーザーTaskにも表示しない。

Lunaが行うのは次の意味処理に限定する。

- フィードバックと対象Trajectory・artifactを関連付ける。
- correction、preference refinement、requirement addition、exploration、approval、delegation、change of mind、bug report等を区別する。
- Agentの質問をinformation、preference、approval gate、exploration、redundant confirmation等の候補へ分類する。
- 一発言に複数の修正があればCaseを分割する。
- 適用条件・例外を根拠付きannotationとして生成する。
- SFT、refinement SFT、DPO、RFT、Judge、Evalの適格候補を出す。最終判定は用途別validatorが行う。

Lunaは、Session終了、無反応、短いTrajectory、最終artifactを成功と判定しない。「これでいい」はUser PreferenceのEvidence候補として関連付けるが、技術的正しさへ一般化しない。質問へ回答があったという事実だけで、質問の有益性を確定しない。

## 実行構成

```text
Imbue Platform
  └── Candidate Builder / Curator Job Queue
        │ Evidence Packet
        ▼
Local imbued / Curator Broker
  └── Curator Root（論理的な親IDと設定）
        └── Codex（ChatGPT管理認証）
              ├── Luna Run 001 ──終了・破棄
              ├── Luna Run 002 ──終了・破棄
              └── Luna Run 003 ──終了・破棄
```

- PlatformのJob QueueとLocal Curator Brokerは常駐する。
- Curator Rootは会話ではなく、policy・schema・子Runを束ねる論理IDとする。
- LunaはJobごとに新しい非対話Codex Runとして起動し、結果取得後に破棄する。
- 継続状態はLunaの履歴ではなく、Case、Event Ledger、Datasetに保存する。
- Lunaの推論はOpenAI側で行い、モデルweightをユーザーPCへ保存しない。
- CodexのChatGPT access/refresh tokenをImbue Platformへ送信しない。

サブエージェントは活動がメインスレッドへ返り、ユーザー画面にも現れるため利用しない。通常の別セッションも履歴を蓄積するため利用しない。

## Trigger

```text
UserMessageReceived
  -> 直前のAgent作業と機械的に関連付け
  -> Work Episode / Decision Pointを開く・更新する

AgentTurnCompleted
  -> diffとverificationを追加
  -> 同じ会話の未処理・未予約の完了Turnを数える
  -> 10 Turn以上なら対象範囲を固定してCurator Jobをenqueue
  -> Local Curator BrokerがJobを取得
  -> Codex上のLunaでLearning Caseを新規作成・更新
```

既定の起動条件は同じ会話で未処理の完了Turnが10件蓄積することとし、閾値は設定で変更できる。8,000文字やコード差分量は起動条件にしない。

- 1 Turnはユーザー入力に対しAgentが回答・作業を終え、制御を返すまでとする。Tool呼び出し、進捗説明、サブエージェントの応答は独立したTurnに数えない。境界を取得できない場合は推測で補わず欠損として記録する。
- 完了イベントの再送を重複計上せず、会話ごとに完了Turn、処理済み位置、Jobが予約した範囲を永続化する。異なる会話のTurn数を合算しない。
- 10件未満は保存して持ち越す。セッション終了・無操作だけでは自動起動せず、手動の整形要求では閾値未満の完了Turnも対象にできる。Session終了を成功や承認とはみなさない。
- 同じ会話のJobは直列化する。処理中の新規Turnは次のバッチへ回す。成功結果の検証・保存後にのみ処理済み位置を進め、失敗・利用枠不足時は同じ範囲を保持して再試行する。
- 10 Turnは起動の目安でありWork Episodeの区切りではない。直前の関連文脈、既存Case、対応するコード・diff・検証結果をEvidence Packetへ含める。入力上限を超える場合は関係を保持して分割し、会話やEvidenceを黙って切り捨てない。
- 遅延EventはTrajectory revisionへ記録し、影響するCaseを再整形対象として次回バッチまたは手動整形に含める。新しい完了Turnとして数えず、遅延Eventだけで即時Luna起動はしない。

初回実装はTurn検出・永続化・件数確認までとし、Curator Jobの実行とLunaによる整形は次段階で実装する。

## Lunaへの入力

会話全文やリポジトリ全体を渡さず、機械処理で絞ったEvidence Packetを渡す。

```json
{
  "job_id": "job-001",
  "curator_root_id": "root-001",
  "policy_version": "1.0",
  "schema_version": "2.0",
  "trajectory_revision": 4,
  "episode_id": "episode-001",
  "decision_point_ids": [],
  "case_id": "case-001",
  "case_version": 2,
  "current_case": {},
  "request_event_ids": [],
  "feedback_event_ids": [],
  "before_refs": [],
  "after_refs": [],
  "verification_event_ids": [],
  "outcome_observation_ids": [],
  "allowed_evidence_ids": []
}
```

出力は自由文ではなく、`LearningCasePatch[]` の固定schemaとする。

```json
{
  "case_id": "case-001",
  "base_version": 2,
  "operation": "update",
  "changes": {},
  "grounded_in_event_ids": []
}
```

## 答え合わせ

過去との整合確認に長期会話は不要である。次のRunへ、前回Caseと後続のユーザーフィードバック、diff、testを明示的に渡して再評価する。

```text
前回Case + 新しいEvidence
  -> confirm / update / split / supersede
```

これにより、判断根拠、再現性、prompt・schema変更の影響を監査できる。

## 並行実行と整合性

- 異なるCaseは並列実行する。
- 同じCaseは `case_version` 順に直列化する。
- `base_version` が最新でなければ結果を保存せず、最新Caseで再実行する。
- extractor keyはTrajectory hash、model、prompt、schema versionから作り、再実行を冪等にする。

## 検証と失敗時動作

- 出力schemaに違反した結果は保存しない。
- `allowed_evidence_ids` にない根拠参照は拒否する。
- 必須Evidenceが欠ける場合はLearning Caseへ昇格させずRaw Eventに留める。
- Lunaの推論はannotationであり、観測事実を上書きしない。
- Lunaが生成した「望ましい行動」「ユーザーの好み」「質問の有益性」を、それ自体の出力だけで教師ラベルにしない。
- Job失敗はforegroundのCodex作業を止めず、再試行またはquarantineする。
- secret・不要な個人情報はLunaへ送信する前にredactする。

## Codex実行契約

Semantic Curatorの標準かつ必須RuntimeはCodexとする。Imbueは独自Agent Runtimeを実装せず、Codex App Serverまたは対応するCodex CLIの非対話・非永続RunをLocal Curator Brokerから起動する。

```bash
codex exec --ephemeral \
  --model gpt-5.6-luna \
  --sandbox read-only \
  --output-schema learning-case-patch.schema.json
```

実行要件は次のとおり。

- ChatGPT管理認証済みのCodexと、`gpt-5.6-luna`へのアクセスを必須とする。
- 初期設定時にCodex version、非永続実行、構造化出力、Luna availability、rate limitを確認する。
- `--ephemeral`等により会話履歴とrollout fileを残さず、ユーザーの作業Taskから分離する。
- 専用の空作業directoryで起動し、Evidence Packet以外を渡さない。
- MCP、Plugin、Skill、Memory、AGENTS.md、repository、network、外部Toolを無効化する。
- 固定schema、token上限、timeout、再試行上限を強制する。
- Imbueが費用負担するOpenAI APIやManaged Curatorへ自動fallbackしない。
- CodexまたはLunaが利用できない間はJobを`blocked_by_codex`として保留し、Raw Eventと直前のActive Caseを維持する。

実行状況はCodexの会話履歴ではなくPlatformのJob状態として管理する。ユーザーのCodex/ChatGPT利用枠とImbueのTraining Provider利用量は別に記録する。
