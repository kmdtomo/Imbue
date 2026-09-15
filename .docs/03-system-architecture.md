# システムアーキテクチャ

## 初期のローカル構成

初期実装は一人のPCで動かす。本文のPlatformは、初期には同じPC上のGo API・Worker・Model Gatewayを指す。PostgreSQL（pgx＋sqlc）とローカルファイルを正本とし、ジョブはPostgreSQLのテーブルで管理する。クラウドホスティング、Object Storage、外部Queue、サービス認証・課金は後段とする。CodexおよびFireworksによる外部AI実行・学習は維持するため、完全オフライン構成ではない。具体的な保存・認証・起動境界は[技術アーキテクチャ](12-technical-architecture.md)を正本とする。


## 1. 設計原則

- 日常のCodex・Claude等の利用を妨げず、イベントから学習データを自動生成・更新し、版管理されたdatasetとして学習へ供給する。
- Session終了、無反応、短いTrajectory、最終artifactを成功の代理変数にしない。仕事の目的とartifactを軸に、複数Turn・SessionへまたがるWork Episodeを構成する。
- Task Policy、Tool Policy、Collaboration Policyを分離し、質問、確認、探索、委任も学習対象の行動として扱う。
- 承認を必須工程にしない。ユーザーによる確認・訂正・削除は随時可能な管理操作とする。
- 内部Chain of Thought（CoT）は取得・推定・保存しない。取得するのは観測可能な入出力、操作、成果物と、結果を見る前にエージェントが表明した短い意図だけである。
- 観測事実、エージェントの主張、システムの推論を分離する。自然言語の説明は事実ではなく`agent_claim`である。
- エージェント固有ログを正規データにしない。各Agent Adapterが共通イベントへ変換する。
- 正規データは特定のSFT・DPO形式に固定せず、用途別形式はprojectionとして生成する。
- 生の観測とartifact、判断の抽象化、学習用projectionを三層に分ける。コードは第一層の証拠として保持し、抽象化だけで置換しない。
- 全ツール呼び出しを同じ粒度で並べず、同一目的の探索・編集・検証を`ActionGroup`へまとめる。
- Agent実行はCodexへ集約する。Agent Learningは独自Agent Runtimeを実装せず、収集、Dataset、学習、Model Gatewayに責務を限定する。

## 2. 全体構成

```text
Codex / Claude / その他Agent
        │
        ▼
Local Go CLI / Collector
  └─ Agent Adapter ── Managed Mode: tool gate + ActionIntent強制
                     Observe Mode: ログ・hook・Git等をbest-effort観測
        ▼
Agent Learning Platform
  ├─ Local Owner / Project（Account・Subscriptionは後段）
  ├─ Evidence Data（Raw Event Ledger / artifact / diff / test）
        ▼
  ├─ Abstract Experience Data（Trajectory / Episode / Decision Point / Outcome）
        ▼
  ├─ Learning Case Extractor（根拠参照付きの判断傾向・Scope・例外）
        ▼
  ├─ Training Projection Data（編集可能、版管理、根拠追跡）
  │    ├─ SFT / DPO / RFT / Judge projection
  │    ├─ Eval / Grader projection
  │    ├─ Skill / Rule projection（任意）
  │    └─ Context / Memory projection（任意）
  └─ Training Orchestrator / Model Registry
           │ 固定Dataset Versionと設定だけを送信
           ▼
     Training / Inference Provider Adapter
       ├─ Fireworks（初期実装）
       └─ その他Provider（将来追加可能）
```

Agent Learning Platformがプロダクト境界であり、外部Providerはユーザーから見えない交換可能な実行基盤とする。初期はサービスアカウントを設けず、ローカル所有者とProjectで管理する。利用者がFireworks接続用credentialを設定する。

Agent実行系はデータ生成系と分け、次の構成を標準とする。

```text
Semantic Curator Job ──► Local Codex / GPT-5.6 Luna ──► Learning Case Patch

agent-learning run ──► Codex ──Responses API──► Agent Learning Model Gateway
                                                   └──► Current Model Deployment
```

Codexがtool loop、context・compaction、sandbox、approval、MCP、Skill、subagentを担う。Claude Code等は収集元として対応できるが、学習済みCurrent Modelを実行する標準RuntimeはCodexとする。Semantic CuratorもユーザーのChatGPT管理認証下にあるCodexで、Jobごとに独立した非対話・非永続Luna Runとして動かす。

Learning Case Extractorは、決定的なCandidate Builder、ステートレスなSemantic Curator、Evidence Validatorで構成する。経験学習の意味モデルは[Experience Learning Model](14-experience-learning-model.md)、具体的な会話からの生成例は[会話からモデル学習まで](10-conversation-to-model.md)、Curator実行は[Semantic Curator（Luna）](11-semantic-curator.md)を参照する。

中核成果物は、Evidence Data、Abstract Experience Data、Training Projection Dataの三層と、それを使った個人適応の比較実験である。チューニングモデルは検証結果であり、個人差の定着と汎化を事前に仮定しない。Context・Memory・Skillは任意projectionかつ比較対象とする。一修正ごとの重み更新は行わず、`agent-learning train`で版固定したActive Dataset全体を学習する。初期構成はQwen3.8-27BをFireworks Managed TrainingでLoRA学習し、Fireworks On-demand Deploymentで実行する。

## 3. 実行モード

### Managed Mode

Agent Adapterがツール実行経路を管理できるモード。各`ActionGroup`の最初のツール実行前に、短い`ActionIntent`をtool gateへ登録させる。

- tool gateは、対応する`ActionIntent`がLedgerへ永続化されるまでツールを実行しない。
- 同じ目的・対象の連続ツールは一つの`ActionGroup`として扱い、各コマンドで意図を再送させない。
- 目的、対象範囲、操作種別が変わった時だけ新しい`ActionIntent`を要求する。
- 要求を満たさない呼び出しは人間の承認待ちにせず、機械的に拒否し、必要な形式をAgentへ返して再試行させる。
- `ActionIntent`は結果を見る前の主張なので判断分析に利用できるが、正しさや因果関係を保証しない。

### Observe Mode

既存Agentをラップできず、セッションログ、hooks、PTY、ファイル変更、Git、CI等だけを読むモード。

- 作業をブロックしない。
- ActionIntentやDecision Preambleを取得できる場合のみ保存する。
- 観測できない意図は欠損のまま扱う。後から推定した意図は`system_inference`として保存し、Agent自身の主張に格上げしない。
- providerのログ形式変更や欠損に備え、成果物diff・テスト・Git等の外部証拠を優先する。

## 4. ActionIntentとDecision Preamble

`ActionIntent`は機械処理用の短い構造化claim、`Decision Preamble`はユーザーやログで読める一文のclaimである。どちらもCoTではない。

```json
{
  "intent_id": "uuidv7",
  "session_id": "...",
  "action_group_id": "...",
  "summary": "既存APIを維持したまま認証エラー処理だけを修正する",
  "action_kind": "edit",
  "targets": ["src/auth.ts"],
  "expected_observations": ["対象テストが成功する"],
  "evidence_class": "agent_claim"
}
```

Managed Modeでは構造化`ActionIntent`を必須とし、Decision Preambleはそこから表示できる。Observe Modeでは自然言語のPreambleしか取得できない場合があるため、構造化抽出しても`system_inference`として区別する。

## 5. データ層

データ層は次の三層を正規構成とする。

1. Evidence Data: 原文、Tool操作、コード、diff、test等の観測事実。
2. Abstract Experience Data: Work Episode、Decision Point、Outcome Evidence、Learning Caseとして表した判断傾向。
3. Training Projection Data: SFT、DPO、RFT、Judge、Eval等の用途に合わせた学習・評価形式。

第二層と第三層は必ず第一層への参照を持つ。自然言語の抽象化を理由にコードを削除せず、コードを含むことを理由に最終artifactを教師へ自動採用しない。

### Raw Event Ledger

原観測の追記専用台帳。セッション、ユーザー発言、Agent出力、ActionIntent、ActionGroup、ファイルdiff、テスト、Git・CI結果、使用Context・Skillの識別子を保存する。原文・blobはハッシュ参照とし、派生データから書き戻さない。

### Normalized Trajectory / Work Episode

provider固有イベントを、一つの仕事の流れとして再構成する。

- ユーザー要求、質問、承認、委任、途中の要件変更
- ActionGroupとその事前claim
- 参照・編集・検証の要約
- 修正前後の成果物
- テスト・CI・レビュー等の結果
- 未観測区間、順序不明、推定境界

Trajectoryは順序付きの観測列、Work Episodeは一つの仕事の目的に関係するTrajectoryと後続結果をSession境界を越えて接続する単位とする。Work Episodeの休止やSession終了を成功とみなさない。

### Decision Point

Agentがその時点で利用可能だったcontextから、質問、提案、Tool利用、編集、検証、制御返却等を選んだ単位。後続の追加要件を過去のcontextへ含めず、行動後のユーザー発言とOutcome Evidenceを参照で接続する。

### Learning Case

Abstract Experience Dataの再利用単位。最低限、`input_context`、`decision_points`、`before`、`feedback`、`after`、`outcome_evidence`、`applicability`、`provenance`、`evidence_state`を持つ。ユーザー発言原文と「これは何を意味するか」というAI解釈は別フィールドにし、コード・diff・testへの参照を失わない。

### Projection

Learning CaseをTraining Projection Dataへ変換する。各projectionは、用途に必要な最小限のコードcontext、抽象化された判断、学習target、loss対象を明示する。

- Refinement SFT: 依頼＋初回出力＋人間のフィードバック→改善出力
- SFT: 同一条件で最終的に望まれた出力を模倣できる事例
- DPO: chosen/rejectedが同じ入力条件で比較可能な場合だけ
- Judge / Outcome Model: Task、Tool、Collaboration Policyの候補と、用途別Outcome Evidenceの関係を予測する場合
- RFT: 実行可能なgraderがあり、報酬を安定計算できる場合だけ
- Eval: 問題を再現し、望ましい挙動を判定可能な事例
- Context/Memory: 関連タスクへ短い判断と根拠ポインタを注入する任意経路
- Skill/Rule: 明文化可能で決定的な手順・制約を出力する任意経路

## 6. Agent Adapter

Adapterは`session lifecycle`、`messages`、`tool boundary`、`artifact changes`、`context inventory`を共通contractへ変換する。provider固有のreasoningや非公開内部状態には依存しない。

- Codex Adapter: rollout、hooks、tool calls、diff、テスト、Git情報
- Claude Adapter: session transcript、hooks、tool calls、diff、テスト、Git情報
- Generic Adapter: OpenTelemetry/JSONL/CLI wrapper/IDE hook

Adapterの能力は`capabilities`として宣言し、取得不能項目を空値で偽装しない。

## 7. ActionGroup化

次のような連続操作は目的単位でまとめる。

- `rg`、ファイルread、設定確認の連続 → `inspect`
- 同じ変更目的の複数ファイル編集 → `edit`
- format、lint、unit test、build → `verify`
- commit、PR、CI確認 → `deliver`

グループには開始時Intent、対象、代表的な操作、入出力artifact、終了状態を持たせる。完全なprovider traceは必要に応じてblob参照で残せるが、Learning CaseやUIへ全ツールを展開しない。

## 8. 保存境界

プロダクトはAgentの設定ディレクトリや対象リポジトリへ実行データを混在させない。ローカルの`AGENT_LEARNING_HOME`（既定`~/.agent-learning/`）には設定、収集buffer、cache、明示的なexportに加えて、初期Platformの正本データを置く。

```text
~/.agent-learning/
  config/
  auth/
  spool/
  cache/
  exports/
  data/projects/  # 初期Platformの正本
```

Canonical Ledger、Dataset、Job、Model RegistryはPlatform上でProjectごとに分離する。可搬なdatasetや設定だけを明示的に`exports/`またはリポジトリ内`.agent-learning/`へ出力できる。ローカル原ログや秘密情報は既定でexportせず、モデルweightはローカルへ保存しない。

## 9. 信頼・安全境界

- `observed_fact`: tool結果、diff、テスト、Git/CI等の直接観測
- `agent_claim`: ActionIntent、Decision Preamble、Agentの完了報告
- `user_statement`: ユーザー発言の原文
- `system_inference`: フィードバック関係、修正理由、一般化範囲、Outcomeの意味等の派生解釈

解釈は常に根拠eventへ戻れるようにする。秘密情報は抽出前後でマスクし、個人情報・嗜好、workspace固有知識、組織ルールを別scopeで管理する。削除・訂正は派生物と将来projectionへ伝播させる。

## 10. 実装アーキテクチャ

PC側はGo製CLI/CollectorとCodex Brokerを持つ。Platformはアカウント、プロジェクト、Dataset、Curator Job、学習オーケストレーション、モデル管理、Codex向けResponses API互換Gatewayを担う。Semantic Curatorのモデル実行とCurrent ModelのAgent実行はCodex、学習・推論計算は交換可能なCloud Providerへ委譲し、ローカル学習やモデルweight保存は標準構成に含めない。詳細は[技術アーキテクチャ](12-technical-architecture.md)を参照する。
