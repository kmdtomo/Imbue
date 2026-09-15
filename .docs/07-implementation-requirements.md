# 実装要件

## 初期のローカル構成

初期実装は一人のPCで動かす。本文のPlatformは、初期には同じPC上のGo API・Worker・Model Gatewayを指す。PostgreSQL（pgx＋sqlc）とローカルファイルを正本とし、ジョブはPostgreSQLのテーブルで管理する。クラウドホスティング、Object Storage、外部Queue、サービス認証・課金は後段とする。CodexおよびFireworksによる外部AI実行・学習は維持するため、完全オフライン構成ではない。具体的な保存・認証・起動境界は[技術アーキテクチャ](12-technical-architecture.md)を正本とする。


## 1. 対象範囲

本システムは、AIコーディングAgentの会話、質問、Tool選択、実行、成果物、自然言語による修正・承認・委任、検証、後日の結果をWork EpisodeとDecision Pointへ接続する。Evidence Data、Abstract Experience Data、Training Projection Dataの三層を維持し、人間とAgentの状況依存のTask、Tool、Collaboration Policyを実際のファインチューニングと個人適応Evalへ接続する。コードは根拠として保持し、最終コードだけを正解にせず、自然言語の抽象化だけにも置き換えない。Context、Memory、Skillは任意projectionであり、次回タスクへの即時注入を必須フローにしない。データ生成・利用に一件ごとの承認状態や承認待ちキューを設けない。

内部CoTは対象外とする。Managed Modeで取得するのは、結果を見る前の短いActionIntent/Decision Preambleと、実際の操作・結果である。

### 初期検証と完成形の境界

本書のコンポーネント・CLI・受入条件は完成形を含む。初期検証は[仮説検証と段階的な実装](15-validation-plan.md)に従い、一人・一観測元・一判断領域・hybrid Direct SFTに絞る。Observe ModeのEvidence追跡、標本監査、データ分割、Context比較、学習・必須評価、Scope選択、訂正とrollbackを先に実装する。Managed Mode完全対応、全Projection、課金、複数Provider、チーム共有は後段とする。秘密情報・所有者分離・削除の要件は初期にも適用する。

## 2. 主要コンポーネント

| Component | 責務 |
|---|---|
| Local CLI / Collector | Agent Learningへのlogin/init/status/train/runと、収集・送信・再送を担う |
| Agent Adapter | LocalでCodex、Claude等の固有イベントを共通contractへ変換する |
| Managed Tool Gate | LocalでActionGroup開始前のActionIntentを永続化してからツールを実行する |
| Observer | LocalのObserve Modeでログ、hooks、PTY、filesystem、Git、CIをbest-effort収集する |
| Codex Runtime | tool loop、context、sandbox、approval、MCP、Skill、subagentとCurrent Modelの実行を担う必須Runtime |
| Local Codex Broker | Codex capabilityを確認し、Curator Runと`agent-learning run`をユーザーPC上で起動する |
| Event Ingestor | Platformでschema検証、重複排除、sequence付与、秘密情報マスク、Ledger追記を行う |
| Artifact Store | diff、snapshot、画像、テスト出力等をcontent-addressed blobとして保存する |
| Trajectory / Episode Assembler | 複数イベントをSession、Task、Work Episode、ActionGroupへ正規化する。Session終了を仕事の成功とみなさない |
| Decision Point Builder | その時点で利用可能だったcontextと、質問・提案・Tool利用・編集・検証・制御返却をEvent参照で構成する |
| Candidate Builder | before/feedback/after/outcome evidence候補をEvent参照だけで機械的に構成する |
| Semantic Curator Scheduler | PlatformでEvidence PacketとJob状態を管理し、Local Codex Brokerへ配信する |
| Semantic Curator Run | ユーザーのChatGPT管理認証下にあるCodexでGPT-5.6 LunaをJobごとにステートレス実行し、Learning Case Patch候補を生成する |
| Evidence Validator | schema、Event参照、用途別成立条件を検証し、Learning CaseとProjectionを有効化する |
| Conflict Resolver | 新旧事例、要件変更、例外、scopeの矛盾を検出・統合する |
| Dataset Store | Learning Caseと変更履歴、tombstone、dataset versionを管理する |
| Projection Engine | Context、Skill、Rule、Eval、SFT、DPO、RFT、Judge形式を生成する |
| Context Injector（任意） | 有効化された場合だけ、関連する短いMemoryを根拠参照と共にAgentへ渡す |
| Training Orchestrator | Platformで学習設定を自動決定し、immutable dataset versionによる学習、artifact検証、Current Model切替、rollbackを行う |
| Model Registry | Dataset、Training Run、Provider上のartifact、Deploymentを関連付ける |
| Model Gateway | Codex向けResponses API互換endpointを提供し、Current Model Deploymentへ変換する |
| Provider Adapter | 初期実装はFireworksとし、Qwen3.8-27Bの学習・artifact保持・推論を内部contractへ変換する |
| Account / Project / Subscription | Platform認証、テナント分離、利用枠、subscriptionを管理する |
| Usage History | Current Model、Dataset Version、後続の修正・承認・委任・質問・Revert、任意Memoryの利用を記録する |
| Platform API / Local RPC | CLIからの操作をLocal DaemonとPlatformへ接続し、Provider固有情報を露出させない |

## 3. 共通Event Contract

全イベントは次のEnvelopeを満たす。

```json
{
  "schema_version": "1.0",
  "event_id": "uuidv7",
  "event_type": "action_group.completed",
  "occurred_at": "RFC3339Nano",
  "ingested_at": "RFC3339Nano",
  "workspace_id": "...",
  "session_id": "...",
  "task_id": "...",
  "action_group_id": "...",
  "source": {
    "provider": "codex",
    "mode": "managed",
    "source_event_id": "...",
    "source_cursor": "..."
  },
  "evidence_class": "observed_fact",
  "payload": {},
  "payload_hash": "sha256:...",
  "redaction": {"applied": true, "ruleset_version": "..."}
}
```

必須条件:

- `event_id`、`workspace_id`、`session_id`、`event_type`、時刻、`evidence_class`を必須とする。
- providerのIDがある場合は`source_event_id`を保持する。
- 大きな本文・binary・snapshotは`payload`へ埋め込まずblob hashを参照する。
- `observed_fact`、`agent_claim`、`user_statement`、`system_inference`を混同しない。
- schema不一致イベントは破棄せず`quarantine/`へ保存する。

## 4. Event種別

### 観測イベント

- `session.started` / `session.quiescent` / `session.ended` / `session.aborted`
- `context.attached`: prompt、AGENTS.md、CLAUDE.md、Skill、Memory等の識別子とhash
- `user.message`: 依頼、追加要件、修正指示の原文
- `agent.message`: commentary、final、公開reasoning summary等。内部CoTは含めない
- `agent.action_intent`: Managed Modeで結果取得前に保存した構造化claim
- `agent.decision_preamble`: 取得できた短い自然言語claim
- `action_group.started` / `action_group.completed` / `action_group.failed`
- `artifact.snapshot` / `artifact.diff`: file、UI screenshot、生成物
- `verification.result`: test、lint、build、grader
- `vcs.commit` / `vcs.review` / `vcs.merge` / `vcs.revert`
- `ci.result`
- `adapter.warning` / `adapter.gap` / `managed.bypass`

個々のtool callは、境界・失敗・副作用・再現に必要な場合だけ`action_group.evidence`として保持する。連続したread/search等をトップレベルイベントへ冗長展開しない。providerの完全traceを保持する場合も、正規Trajectoryからはblob参照とする。

### 派生イベント

- `trajectory.revised`
- `work_episode.revised`
- `decision_point.linked`
- `feedback.linked`
- `outcome_observation.recorded`
- `learning_case.created` / `updated` / `conflicted` / `retracted`
- `projection.built` / `activated` / `failed` / `staled`
- `memory.used`（Context/Memory projection有効時のみ）
- `training.started` / `completed` / `failed` / `activated` / `rolled_back`

派生イベントは必ず入力event ID、extractor/exporter version、生成設定を持つ。

## 5. Managed Tool Gate Contract

### ActionIntent

```json
{
  "schema_version": "1.0",
  "intent_id": "uuidv7",
  "session_id": "...",
  "task_id": "...",
  "action_group_id": "...",
  "summary": "既存構造を保ち、認証失敗時の表示だけを直す",
  "action_kind": "inspect|edit|verify|deliver|external_action",
  "targets": ["src/auth.ts"],
  "expected_observations": ["関連テストが成功する"],
  "created_before_execution": true
}
```

### 実行規則

1. AgentはActionGroupの最初のtool requestと共に`intent_id`を送る。
2. GateはIntentのschema、session、group、対象scopeを検証する。
3. Gateは`agent.action_intent`をLedgerへfsync相当で永続化する。
4. 永続化成功後にだけtoolを実行し、結果を観測イベントとして保存する。
5. 同じ目的の後続toolは同一`action_group_id`を再利用する。
6. 目的、操作種別、主要対象が変わった場合は新しいActionGroupを要求する。

Intent欠損時は人間の承認を求めず、`ACTION_INTENT_REQUIRED`と期待schemaをAgentへ返す。Decision PreambleだけではManaged Modeのgate条件を満たさない。

## 6. ActionGroup Contract

```json
{
  "action_group_id": "...",
  "task_id": "...",
  "intent_id": "...",
  "kind": "inspect|edit|verify|deliver",
  "targets": ["src/auth.ts", "tests/auth.test.ts"],
  "representative_actions": ["read auth handler", "edit error branch"],
  "input_artifacts": ["sha256:..."],
  "output_artifacts": ["sha256:..."],
  "verification_ids": ["..."],
  "status": "succeeded|failed|cancelled|unknown"
}
```

グループ境界はIntent変更、ユーザー割り込み、成果物確定、検証開始、長いアイドルで切る。Observe Modeで推定した境界には`boundary_source=system_inference`と根拠eventを付け、解決できない境界は`unknown`とする。

## 7. Learning Case Contract

```json
{
  "case_id": "...",
  "case_version": 3,
  "case_type": "feedback_driven_revision",
  "scope": {"user": "...", "workspace": "...", "paths": ["ui/**"]},
  "input_context": {
    "request_event_ids": ["..."],
    "available_at_decision_event_ids": ["..."]
  },
  "work_episode_ids": ["..."],
  "decision_point_ids": ["..."],
  "before": {"artifact_refs": ["..."], "agent_claim_refs": ["..."]},
  "feedback": {
    "user_event_ids": ["..."],
    "interpretation": "カード分割を減らし情報密度を上げる",
    "relation_kind": "correction|preference_refinement|requirement_addition|exploration|approval|delegation|change_of_mind|bug_report|question|unknown"
  },
  "after": {"artifact_refs": ["..."]},
  "outcome_evidence": [
    {
      "dimension": "user_preference|functional|collaboration|delivery|durability",
      "kind": "explicit_approval|explicit_rejection|revision|verification|merge|revert|other",
      "event_ids": ["..."]
    }
  ],
  "applicability": {"conditions": [], "exceptions": [], "generality": "task|workspace|user"},
  "evidence_state": "grounded|conflicted",
  "provenance": {"trajectory_revision": 4, "event_ids": ["..."]},
  "projection_eligibility": {
    "context": true,
    "skill": false,
    "eval": true,
    "sft": true,
    "dpo": false,
    "rft": false,
    "judge": true
  },
  "status": "active|conflicted|superseded|retracted"
}
```

`requirement_addition`をAI判断の誤りやpreferenceとして自動一般化しない。同一入力条件でないbefore/afterをDPOへ出力しない。後続Eventで初めて追加された情報を、それ以前のDecision Pointへ書き戻さない。

Session終了、無反応、Agentの完了報告、最終artifact、短いTrajectoryから総合的な成功判定を生成しない。明示的承認、diff、test、CI、review、merge、後続修正、revert等を用途別の`outcome_evidence`として分離する。質問への回答が存在するだけで、その質問を有益または不要と一般化しない。

## 8. 状態機械

### Session / Trajectory

```text
discovered → observing → quiescent → normalizing → ready
                    └──────────────→ aborted
ready → revised → ready
```

遅延イベント到着時は既存Trajectoryを書き換えずrevisionを追加し、影響するCaseを再抽出する。

`session.ended`と`session.quiescent`は収集状態であり、Work Episodeの完了、承認、成功へ遷移させない。

### Work Episode

```text
observing → quiescent → resumed
    ├───────────────→ abandoned
    └───────────────→ unknown
```

Work Episodeは意味的な`success`または`completed`終端を持たない。明示的承認や技術検証はOutcome Observationとして追加し、後日の修正・Revertで反証可能にする。

### ActionGroup

```text
declared → executing → succeeded
                    ├→ failed
                    └→ cancelled
```

Managed Modeでは`declared`より先にtool結果が存在してはならない。Observe Modeでは`declared`欠損を許容し`unknown`として扱う。

### Learning Case

```text
active → superseded
   ├──→ conflicted → active | superseded
   └──→ retracted
```

承認済み・未承認の状態は持たない。訂正は新version、削除はtombstoneを生成し、履歴を保持したまま将来projectionから除外する。

### Projection

```text
pending → built → verified → active → stale
             └→ failed        └→ replaced
```

Context projectionは設定で有効化された場合だけ生成・利用する。Eval/学習projectionは用途別validatorを通過しない限りactiveにしない。

### Training Run

```text
planned → materialized → running → candidate → evaluated → activated
                      └→ failed        └────────→ rejected
                                                     └→ rolled_back
```

学習またはartifactの読込検証に失敗した場合はCurrent Modelを変更しない。成功した新versionもCandidateとして保持し、個人適応と一般能力回帰の用途別Evalを通過するまでCurrent Modelへ切り替えない。旧versionはrollback用に保持する。評価結果はpassed・failed・inconclusiveとし、inconclusiveはCandidateのまま追加収集・再評価を待つ。passedのみactivatedへ遷移できる。評価の計画・分割・最小件数とScopeの実行契約、訂正時の再学習元は[検証計画](15-validation-plan.md)に従う。

## 9. Projection Contract

各projectionは次を持つ。

```json
{
  "projection_id": "...",
  "projection_type": "context|skill|eval|sft|dpo|rft|judge",
  "source_case_versions": ["case-id@3"],
  "exporter_version": "...",
  "target_format": "...",
  "abstraction_refs": ["case-id@3"],
  "input_artifact_refs": ["sha256:..."],
  "target_artifact_refs": [],
  "loss_policy": "decision_only|artifact_change|full_trajectory",
  "artifact_hash": "sha256:...",
  "validation": {"status": "passed", "checks": []},
  "status": "active"
}
```

任意のContext Injectorを有効化した場合は、タスク、workspace、path、技術、過去の利用実績、鮮度、矛盾を使って関連projectionを選ぶ。注入内容には短い指針と`case_id`を含め、全履歴をプロンプトへ詰め込まない。使用後は`memory.used`を記録する。無効時もdataset生成、Eval、trainingは独立して完結しなければならない。

Training Projectionは、元Evidenceへの参照、含めたコードcontext、学習target、loss対象を明示する。`decision_only`ではコードをstateのcontextに使用できるがコードtokenを教師targetにしない。`artifact_change`はコード変更との直接Evidenceとverificationが成立する場合だけ許可する。Semantic Curatorの抽象化だけを根拠に`target_artifact_refs`を設定しない。

## 10. 保存レイアウト

```text
Local: $AGENT_LEARNING_HOME/           # default: ~/.agent-learning
  config/
  auth/
  spool/
  cache/
  exports/

Platform（初期はLocalのdata/配下）: projects/<project-id>/
  ledger/
  blobs/
  trajectories/
  episodes/
  decision-points/
  outcome-observations/
  cases/
  projections/
  datasets/
  evals/
  training-runs/
  model-registry/
  quarantine/
```

LocalのSQLiteは送信cursor、retry、cache用であり、Canonical Datasetの正本にしない。Platformの正本はLedger、content-addressed blob、versioned manifestとする。Base Model、checkpoint、LoRA、推論用weightはProviderに保持し、Platformは参照IDとprovenanceだけをModel Registryへ保存する。

## 11. Idempotency・再実行

- Ingestは`provider + source_event_id`、なければ`session + source_cursor + payload_hash`で重複排除する。
- 同一イベントの再送は既存`event_id`を返し、Ledgerへ二重追記しない。
- Ledger segmentを先にdurable writeし、index更新に失敗した場合は起動時にsegmentからindexを再構築する。
- Trajectory revision keyは`ordered source event hashes + assembler_version`とする。
- Case extraction keyは`trajectory_revision_hash + extractor/model/prompt/schema version`とする。
- Projection keyは`source case versions + exporter version + target config`とする。
- Training Runは`dataset manifest hash + base model + trainer config + code version`を固定し、再現可能にする。
- 各background jobはlease、heartbeat、attempt、retry_atを持つ。同じ入力の同時処理は一件だけcommitできる。

## 12. 失敗時挙動

- Observe Modeの収集失敗はAgent作業を止めず、`adapter.gap`とsource cursorを残して再開する。
- Managed Tool GateがIntentを保存できない場合はtoolを実行しない。設定済みの緊急fallbackがある場合のみObserve Modeへ切り替え、`managed.bypass`を必ず記録する。黙ってfallbackしない。
- blob保存失敗時は対応イベントをcommitしない。
- schema不一致・未知eventはquarantineし、他イベント処理を継続する。
- extraction失敗時もLedgerとTrajectoryを保持し、指数backoffで再試行する。
- Codex、ChatGPT管理認証、Luna、必要な非永続実行機能のいずれかが利用できない場合、Curator Jobを`blocked_by_codex`として保留する。Agent Learning管理のAPI Runtimeへ自動fallbackしない。
- 新しいCase抽出が失敗しても直前のactive Caseを壊さない。
- projection生成・検証失敗時は直前のactive projectionを維持する。
- 任意のContext Injector障害時はメモリなしでAgentを継続し、dataset生成・trainingを停止しない。
- Trainingまたはartifact検証の失敗時はCurrent Modelを切り替えない。個人適応EvalまたはCapability Preservation Evalの必須条件を満たさないCandidateも切り替えない。
- 秘密情報検出時はprojectionとexportを停止し、redacted派生物を再生成する。原データ保持は設定されたretention policyに従う。

## 13. CLI/API最低要件

- `login`: クラウド提供時のPlatform認証。初期ローカル構成では不要とし、Provider credentialはOS credential storeへ設定する
- `init`: Projectと観測対象を初期化する
- `status [--workspace]`: ingest、未処理job、active dataset/model、adapter gap
- `sessions show <id>`: 収集Sessionと関連するWork Episode、正規Trajectory、ActionGroup
- `episodes show <id>`: 目的、Decision Point、Outcome Evidence、未観測・遅延Event
- `cases list|show|explain <id>`: 根拠、解釈、scope、projection
- `cases edit|split|retract <id>`: 新versionまたはtombstoneを作成
- `memory explain <task-or-run>`（任意機能）: 何が注入され、どのCaseが使われたか
- `dataset diff <a> <b>` / `dataset export --format ...`
- `eval run <suite>`
- `train [--budget <limit>]`: Active Datasetを固定し、Qwen3.8-27Bの学習を開始する
- `training status` / `model rollback <version>`
- `run`: Agent Learning ProfileでCodexを起動し、Model Gateway経由でCurrent ModelのCloud Deploymentを利用する

編集コマンドは承認フローを開始せず、直ちに新versionを作り、影響するprojectionを再計算する。

## 14. 受入条件

1. Managed ModeでIntentなしのtool callが実行されず、再試行用schemaがAgentへ返る。
2. Managed ModeのLedgerで`agent.action_intent`が、対応する最初のtool結果より必ず前にdurable保存される。
3. 同じ目的の20回のread/searchが、UIとNormalized Trajectoryでは一つの`inspect` ActionGroupとして表示される。
4. Observe ModeでPreambleを取得できなくても作業が継続し、欠損が`adapter.gap`またはunknownとして表現される。
5. 保存schemaに内部CoT欄がなく、providerの非公開reasoningを取得しない。
6. ActionIntent、Agent完了報告、AIによる修正理由が観測事実とは別のevidence classで保存される。
7. ユーザー修正、質問・提案、当時利用可能だったcontext、修正前artifact、修正後artifact、用途別Outcome Evidenceを一つのLearning Caseから元eventまで追跡できる。
8. 追加要件の事例が、同一入力のpreference pairとしてDPO出力されない。
9. 同じイベントと同じextractorを複数回処理しても、Event、Case、Projectionが重複しない。
10. 遅延CI、Review、後続修正、Revertの到着によりWork Episode、Trajectory revision、該当CaseのEvidenceだけが更新される。
11. 一件ごとの`pending approval`、`approved`状態を持たず、Caseが自動生成され、適格なdataset・Eval・training projectionへ供給される。
12. Caseの訂正・分割・削除後、dataset exportと有効化されたprojectionへ新versionが反映され、旧版は監査履歴だけに残る。
13. Context projectionを有効化した場合、回答またはtraceから使用Caseを説明でき、`memory.used`が記録される。無効時もdataset、Eval、trainingが動作する。
14. extraction、projection、trainingの途中障害で直前のactive Case、Projection、Modelが破損しない。
15. process crash後、Ledgerとmanifestからindexと未完了jobを復旧できる。
16. secret fixtureを含むセッションから生成したContext、Skill、学習exportへ秘密値が出力されない。
17. Context projectionを有効化した場合、workspace固有Caseが無関係なworkspaceへ注入されない。
18. training runがdataset hash、base model、config、code versionから再現でき、成功したartifactをCurrent Modelへ切り替えても旧versionへrollbackできる。
19. `login`から`train`と`run`まで、学習・推論Providerのアカウント作成、API key入力、管理画面操作なしで完了できる。
20. `init`やデータ蓄積ではTraining Jobが開始されず、明示的な`train`だけが固定Dataset VersionからFireworks Managed Trainingを開始する。
21. ユーザーPCにBase Model、checkpoint、LoRA、推論用weightが保存されない。
22. 初期Base ModelはQwen3.8-27B、学習方式はLoRA、配備先はFireworks On-demand Deploymentとなる。
23. `run`はDeploymentの作成・起動とLoRA読込を保証し、Scale-to-zeroからの起動中応答を再試行する。
24. 認証された所有者と異なるLoRAをModel Gateway経由で利用できない。
25. `agent-learning run`が独自Agent HarnessではなくCodexを起動し、Responses API互換Model Gateway経由でCurrent Modelを利用する。
26. Codexがtool loop、context・compaction、sandbox、approval、MCP、Skill、subagentを担い、Agent Learning側に同等の汎用Runtimeを重複実装しない。
27. Curator Jobごとに新しいCodex/Luna Runが作られ、別Caseまたは過去Jobの会話状態を継承しない。
28. CuratorのChatGPT credentialがPlatformへ送信されず、ユーザーの作業TaskにもCurator会話が混在しない。
29. Codex/Lunaが利用不能な間もRaw Eventと直前のActive Caseを維持し、復旧後に保留Jobを再開できる。
30. `session.ended`、`session.quiescent`、無反応、Agentのfinal messageだけでは、Work EpisodeまたはLearning Caseへ成功・承認Evidenceが追加されない。
31. 明示的な「これでいい」はUser PreferenceのOutcome Evidenceとなるが、Functional Verificationや一般的な正解として出力されない。
32. `requirement_addition`または`exploration`で後から追加された条件が、それ以前のDecision PointのDirect SFT教師やDPO rejected/chosenへ混入しない。
33. 質問へユーザーが回答した事実だけでは、質問の有益性または冗長性を表すLearning Caseがactiveにならない。
34. 確認による方針変更・新制約、明示的な確認要求、委任、質問過多への指摘を、Collaboration Policyの別々のOutcome Evidenceとして追跡できる。
35. 自然言語だけで複数回編集したfixtureから、各Turnのartifact snapshot、後続発言、Decision Pointを同じWork Episodeへ接続できる。
36. 過去の最終artifactへ一度で到達することを一律の正解とせず、回避可能な修正と探索・承認のための往復を区別したProjection適格性を出力できる。
37. Learning CaseとTraining Projectionから、元の会話、コードsnapshot、diff、Tool操作、testまで追跡でき、自然言語の抽象化後もEvidenceが失われない。
38. 最終コード、commit、mergeだけが存在するfixtureから、コードを教師targetとするactiveなSFT Projectionが生成されない。
39. `decision_only` Projectionで、必要なコードcontextを入力に含めながらコードtokenをloss対象外にできる。
40. Semantic Curatorが生成した抽象化に直接Evidenceがない場合、その抽象化だけから学習targetが有効化されない。
41. 同一基盤モデルについて未学習、Skill・Context利用、チューニングの各条件と、raw中心、abstract中心、hybridのProjection方式を別Dataset Versionとして比較できる。
42. 学習成功だけではCandidateがCurrent Modelへ切り替わらず、個人適応EvalとCapability Preservation Evalの必須条件を満たした場合だけ昇格できる。

43. Experiment Planの必須閾値・件数・比較条件が未設定の場合、探索実験は可能だがモデル昇格はできない。
44. 同じWork Episode、成果物の派生Case、言い換え、別Projectionがtrain・development・final evaluationを跨ぐfixtureを検出できる。後日関連が判明した評価を無効化できる。
45. 評価未完了・件数不足・不確実性過大ではinconclusiveとなり、Current Modelが維持される。初回でも未評価Candidateが既定モデルにならない。
46. 抽出済み・保留・除外候補の標本監査から、誤分類・Scope誤り・教師適格性・保留率・作業量当たりの有効事例数を別々に報告できる。監査は通常運用の承認待ち状態を作らない。
47. Scope一致時だけ対象Adapterが選ばれ、不明・競合・対象外では未適応の基盤モデルが選ばれる。所有者、Scope、選択理由、規則versionが記録される。
48. 対象Scope内の未学習Taskと対象外task・repositoryを評価し、局所変更の好みを無関係な仕事へ誤適用するCandidateを事前条件に基づき昇格不可にできる。
49. 訂正・削除対象を学習した祖先を持つcheckpoint、および祖先の学習履歴不明のcheckpointを除外反映用の再学習元に指定できない。
50. データ除外済みでも旧配備モデルには未反映と表示される。訂正ではstale、削除・秘密情報混入による利用停止では隔離となり、適格な再学習・評価・配備後にだけ反映完了となる。
51. 抽出・再利用・追加学習・実用の四段階の結果を別々に記録し、自動生成データと監査訂正済みデータの成績を区別できる。
