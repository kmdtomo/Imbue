# OSS調査に基づく設計監査・提案

## 初期のローカル構成

初期実装は一人のPCで動かす。本文のPlatformは、初期には同じPC上のGo API・Worker・Model Gatewayを指す。PostgreSQL（pgx＋sqlc）とローカルファイルを正本とし、ジョブはPostgreSQLのテーブルで管理する。クラウドホスティング、Object Storage、外部Queue、サービス認証・課金は後段とする。CodexおよびFireworksによる外部AI実行・学習は維持するため、完全オフライン構成ではない。具体的な保存・認証・起動境界は[技術アーキテクチャ](12-technical-architecture.md)を正本とする。


## 位置づけ

本書は既存OSS・公開研究と現行要件を比較した提案であり、未採用の変更候補を含む。Codex RuntimeとLuna実行に関する決定は採用済みで、正本の各要件文書へ反映している。それ以外は採用判断前の提案を含む。

## 結論

現行の中核方針は維持する。

- `Raw Event → Trajectory → Work Episode / Decision Point → Learning Case → Projection → Fine-tuning`を正本とする。
- 承認待ちフロー、事例ごとのconfidence/strength score、モデル比較UXを設けない。
- 内部CoTを取得せず、観測事実とActionIntent等のclaimを分ける。
- Go製CLI/Collector＋Imbue Platform＋交換可能なCloud Providerとする。
- Semantic Curatorは会話を継続しないステートレスRunとする。

一方、精度と実装可能性のため、次の変更を推奨する。

1. Codexを標準かつ必須の`Agent Runtime`とし、独自Harnessを実装しない。
2. 観測でき、収集policyで許可された全Tool CallとObservationを、順序と対応関係を失わずRaw Ledgerへ保存する。
3. LLM呼び出し時に実際に見えていたContextをmanifest化する。
4. Projectionを対象Runtime、tool schema、chat template、tokenizerへ結び付ける。
5. 継続学習は固定BaseからActive Dataset全体で新しいLoRAを作る方式を既定にする。
6. Training、Artifact location、InferenceのProvider責務を分割する。
7. CuratorはユーザーのChatGPT管理認証下にあるCodexで、GPT-5.6 Lunaをephemeral実行する。
8. ユーザー向けスコアとは別に、Dataset整形とartifactの内部回帰検証を行う。

## 推奨アーキテクチャ

```text
Codex / Claude / その他Agent
        │
        ▼
Local Go Collector
  ├─ Agent Adapter / Tool Gate
  ├─ policy内のEvent・Contextを意味欠落なく収集
  ├─ encrypted WAL / retry
  └─ Codex Broker
       └─ Stateless Luna Curator Run
        │
        ▼
Imbue Platform
  ├─ Control Plane
  │    └─ Account / Project / Policy / Subscription
  ├─ Learning Data Plane
  │    └─ Ledger / Trajectory / Episode / Decision / Case / Dataset / Projection
  ├─ Execution Plane
  │    └─ Curator / Training / Deployment Workflow
  ├─ Artifact Registry / Vault（export可能な場合）
  │    └─ Provider ref / LoRA / config / manifest / provenance
  └─ Analytics Plane
       └─ 再構築可能な検索・集計用read model
        │
        ├─ Curator Job Contract
        ├─ TrainingProvider
        ├─ ServingProvider
        └─ Responses API Model Gateway
                │
                ▼
       Fireworks（初期実装）/ その他Provider（将来追加可能）

Local `imbue run`
        │
        ▼
Codex Runtime
  ├─ system prompt
  ├─ context / compaction
  ├─ tool protocol / execution loop
  ├─ sandbox
  └─ Imbue Model Gateway ──► Current Model
```

### Codex Runtimeとの互換性を先に定義する

モデルの重みだけではcoding agentとして動作しない。ただし、Imbueが汎用Agent Runtimeを再実装する必要はない。Codexを標準Runtimeとし、次を`Codex Compatibility Profile`としてversion固定する。

```text
compatibility_profile_version
minimum_codex_version
system_prompt_hash
tool_schema_hash
context_policy_version
compaction_policy_version
sandbox_profile
responses_api_contract_version
model_gateway_profile
required_capabilities
```

`imbue run`は生成済みProfileでCodexを起動し、ImbueのResponses API互換Model Gatewayへ接続する。tool loop、context・compaction、sandbox、approval、MCP、Skill、subagentはCodexが担う。ImbueはProvider差分、tenant認証、Current Model alias、利用量をGatewayで吸収する。

学習側では、CodexやClaudeのTrajectoryを対象Modelの異なるchat templateやTool protocolへそのまま流さない。tokenizer、chat template、loss mask、Tool schema、exporter versionはModel/Profile側で別途固定し、Codex Runtimeとの入出力互換をProjection生成時に検証する。

## データ収集とCanonical Dataset

### 意味を落とさないRaw、読みやすいActionGroup

Adapterが観測でき、収集policyで許可されたTool Call、引数、Observation、対応ID、順序はRaw Ledgerへ残す。大きな出力はblob参照にし、retentionで制御する。Observe Modeの欠損、provider側truncate、redaction、除外は`capture_quality`、Adapter capability、gapとして明示する。

`ActionGroup`は表示・検索・Case抽出用の派生indexとする。探索操作をRaw段階で捨てると、「モデルが何を見て次の操作を選んだか」を後から復元できない。

### Context Window Manifest

各LLM呼び出しに次を記録する。

- ordered message / event refs
- system prompt、Tool、Skill、Memory、AGENTS.md等のcontent-addressed refとhash
- agent harnessとmodelのversion
- compaction・summary境界
- input/output event refs
- 未観測・truncateされた範囲

要約後のモデルが見ていない過去原文を学習入力へ復元しない。context compaction境界を跨ぐ場合は学習列を分割する。

### Feedbackの対象

Learning Caseへ次を追加する。

```text
feedback_target_event_ids
feedback_target_action_group_ids
feedback_target_artifact_hunks
relation: corrects | extends | retracts | clarifies
task_spec_before_ref
task_spec_after_ref
```

これにより、誤り訂正と追加要件を区別し、一つの指摘が複数変更へ影響した場合も根拠を保持できる。

### Subagentと継続Task

並行Subagentを一列へflattenせず、次を持つ。

```text
run_id
trajectory_id
parent_trajectory_id
delegated_by_step_id
continued_from_trajectory_id
```

[ATIF](https://github.com/harbor-framework/harbor/blob/main/rfcs/0001-trajectory-format.md)互換import/exportを用意するが、内部CoT用フィールドは採用しない。[Agent Data Protocol](https://github.com/neulab/agent-data-protocol)のように、Raw形式、Canonical形式、対象Harness別形式を分離する。

## Dataset Projection

Training用Projectionを明確に分ける。Raw Event、コード、diff、test等はEvidence Data、Work Episode、Decision Point、Learning CaseはAbstract Experience Data、以下の形式はTraining Projection Dataとして別々に保持する。自然言語への抽象化後も元コードEvidenceを失わず、最終コードだけを教師targetにしない。

| Projection | 内容 | 成立条件 |
|---|---|---|
| `trajectory_sft` | 成立Evidenceを持つAction / Observation列 | 因果順序、当時利用可能だったContext、用途別Outcome Evidenceを復元できる |
| `refinement_sft` | 初回出力＋ユーザー原文Feedback→修正後出力 | 行動直後の`after`と後続artifactを区別でき、後続修正で覆されず、verificationまたは明示的な採用Evidenceがある |
| `artifact_sft` | 初期Context→後に成立したartifact | 後続追加要件の逆流がなく、当時のContextと成立Evidenceがある |
| `counterfactual_trajectory_sft` | 再構成した理想Action列 | 同じbase stateからreplay成功済み |
| `dpo` | 同じ有効入力`x`に対するchosen / rejected | system prompt、messages、repo state、Tool、Runtime、Contextが同一 |
| `rft` | prompt＋sandbox＋verifier | 機械的で再実行可能な判定がある |
| `judge` | state＋候補行動→用途別Outcome | Outcome軸を統合せず、観測Eventまで追跡できる |

全ProjectionにCodex Compatibility Profile、Base Model revision、tokenizer、chat template、Tool schema、exporter versionを固定する。Curatorが作った「理想Trajectory」をreplayせず教師にしない。

個人適応の初期ablationでは、raw trajectory中心、abstract decision中心、必要なコードcontextと抽象化を併用するhybridを同一Base Modelで比較する。各Projectionは入力artifact、target artifact、loss mask policyを明示し、`artifact_sft`はコード変更への直接Evidenceとverificationがある場合だけ有効化する。

Training sequenceでは、Agentが生成したtokenだけをloss対象にし、User、Tool、Environment ObservationはContextへ残してmaskする。前Callのprompt＋responseと次Callのpromptがtoken-prefixとして連続する場合だけ列を結合し、compaction、system prompt変更、Tool schema変更、未復元truncateで分割する。[Agent Lightning](https://github.com/microsoft/agent-lightning)

CaseとProjectionには`origin: observed_user | public_rollout | synthetic`、source dataset、license、generation methodを持たせ、公開・合成データを観測された個人・チームのExperienceと暗黙に混ぜない。

### Sampling

Active Dataset全体を正本として保持し、学習時はtask family、repository、言語、feedback relation、verifier、Trajectory長で層化する。同一Task・Decision familyには上限を設け、簡単な反復事例がDatasetを支配するのを防ぐ。

これは事例の強さを点数化する設計ではなく、再現可能なsampling policyである。SWE-GymやSWE-smithでもTask単位の件数制限が使われている。[SWE-Gym](https://arxiv.org/abs/2412.21139)、[SWE-smith](https://arxiv.org/abs/2504.21798)

## Curator

Semantic Curatorのモデルは`GPT-5.6 Luna`、Agent RuntimeはCodexを標準かつ必須とする。PlatformとLocal Brokerの境界は次のJob interfaceへ閉じる。

```go
type CuratorJobRunner interface {
    GeneratePatch(context.Context, EvidencePacket) (LearningCasePatch, error)
}
```

Local Codex BrokerがユーザーのChatGPT管理認証を使い、Codex App Serverまたは`codex exec --ephemeral`でJobごとに新しいLuna Runを起動する。Imbue PlatformはEvidence Packet、schema、lease、attempt、結果だけを管理し、ChatGPT tokenやLunaの会話状態を保持しない。

Codexのnon-interactive modeはJSON Schema出力と、rollout fileを保存しない`--ephemeral`を提供する。[OpenAI Non-interactive mode](https://learn.chatgpt.com/docs/non-interactive-mode) App ServerはChatGPT OAuth、device code、rate limit取得を提供する。[OpenAI Codex App Server](https://learn.chatgpt.com/docs/app-server)

初期設定時にChatGPT管理認証、最低Codex version、非永続実行、構造化出力、Luna availability、rate limitを確認する。Local BrokerやLunaが利用不能な間はJobを`blocked_by_codex`として保留し、Raw Eventと直前のActive Caseを維持する。Imbue管理のAPI Runtimeへは自動fallbackしない。

Curator Runは専用の空作業directoryで実行し、Evidence Packet以外をmountしない。MCP、Plugin、Skill、Memory、AGENTS.md、repo、network、外部Toolを無効化し、固定schema、token上限、timeoutを強制する。ユーザーの作業Taskへ会話を混在させず、別Caseや過去Jobの会話状態も継承しない。

精度優先の場合も、LLMのconfidence scoreでは確定しない。

正本フローは`deterministic Candidate Builder → Curator → deterministic Evidence Validator → Case commit`とする。第二のLLMによるEvidence Adjudicatorは、精度向上を実証できた場合だけ追加する実験候補とする。

- 異なるCaseは並列、同一Caseはversion順に直列化する。
- 新revision到着時は未開始の旧Jobをstale化する。
- 全attemptを追記保存し、schema、Evidence参照、CASを最初に通過したPatchだけをcommitする。
- Validatorが保証するのはschema、Evidence参照、version、replay結果であり、適用範囲や理由は通過後も`system_inference`である。
- 人間が検証・訂正したEvidence Packetと期待`LearningCasePatch`の組をCurator回帰セットへ追加する。
- ActionIntentはProjection適格条件にせず、Target Runtimeが生成しない場合はloss対象外とする。強制Gateによる行動分布の変化はcapability別に検証する。

## 継続学習

既定は次とする。

```text
固定Base Model revision
  + Active Model-scoped Dataset全体
  + 必要なら分離管理したAgent能力維持用anchor dataset
  → 毎回新しいModel-scoped LoRA
```

前回LoRAから毎回warm-startし、同じ過去Caseを繰り返し学習する方式は既定にしない。warm-startはDatasetが大規模な場合の例外とし、`新規・変更Case＋層化した過去Case replay＋必要なanchor`だけを使う。Caseごとの累積exposureを記録し、定期rebase時はBase ModelからActive Dataset全体を学習する。

Base Model revisionを変更する場合は旧LoRAを継承せず、Active Dataset全体から新しい`Model Line`を作る。小規模な個人Datasetだけでagent能力を壊さないよう、Runtime互換の公開Trajectoryから共有`Platform Agent Base`を作る案は、ライセンスと実測を確認したうえで検証する。

自社Cloud Workerを持つ場合のreference backend候補を[TRL](https://huggingface.co/docs/trl/index)＋[PEFT](https://huggingface.co/docs/peft/index)とし、[Axolotl](https://docs.axolotl.ai/)をconfig-driven runner候補とする。LoRA、DPO、tokenizer、distributed training自体は独自実装しない。[LLaMA-Factory](https://github.com/hiyouga/LlamaFactory)と[Unsloth](https://github.com/unslothai/unsloth)は対応方式と最適化の比較対象に留める。

モデルと学習設定はLLMの自由推論で決めず、versionedな`ModelProfile`と`RecipePolicy`で解決する。Model Profileにはlicense、fine-tuning/deployment可否、Tool対応、Context長、Runtime互換性、artifact export capabilityを持たせる。Recipeは件数、token数、p95/p99長、Tool比率、Projection比率、言語分布からProvider defaultとmodel別実績を選び、必要時だけlearning rate、epoch、LoRA rankを限定sweepする。解決後の設定と選定理由をTraining Runへ保存する。

## Cloud PlatformとProvider

### Provider Contractを分割する

```go
type TrainingProvider interface {
    Capabilities(context.Context) (TrainingCapabilities, error)
    UploadDataset(context.Context, TrainingProjectionArtifact, ValidationProjectionArtifact) (DatasetRef, error)
    StartTraining(context.Context, TrainingRequest) (TrainingRef, error)
    GetTraining(context.Context, TrainingRef) (TrainingState, error)
    CancelTraining(context.Context, TrainingRef) error
    ListArtifacts(context.Context, TrainingRef) ([]ArtifactLocation, error)
}

type ServingProvider interface {
    Capabilities(context.Context) (ServingCapabilities, error)
    RegisterArtifact(context.Context, ArtifactLocation) (ModelRef, error)
    EnsureDeployment(context.Context, ModelRef) (DeploymentRef, error)
    Invoke(context.Context, DeploymentRef, InferenceRequest) (InferenceResponse, error)
    StopDeployment(context.Context, DeploymentRef) error
}
```

初期AdapterはFireworks、Base ModelはQwen3.8-27B、学習はManaged TrainingのLoRA、実行はOn-demand Deploymentとする。Canonical DatasetとcontractはProvider非依存に保ち、他Providerは必要時に追加する。

学習済みartifactは、Providerとlicenseがexportを許す場合、Imbue CloudのArtifact Vaultへ安全にsnapshotする。exportできない場合はProvider Native Refを正本として保持する。

```text
artifact_type: lora | full_model | checkpoint
adapter_model.safetensors
adapter_config.json
base_model_revision
immutable tokenizer files / chat template / model config
training_manifest
dataset_manifest_hash
```

Artifact locationは`ProviderNativeRef | PlatformVaultRef`として表現する。VaultはrollbackとProvider移行の可能性を高めるが、LoRAだけではBase Model廃止へ対応できない。license上可能ならBase weightもCloudへmirrorし、できない場合は互換Baseを提供するProviderが存在する範囲だけ移行可能と明記する。ユーザーPCにはweightを保存しない。

### Durable Workflow

Training、Deploymentと、再試行・callback・cancelを要するCurator lifecycleには[Temporal](https://github.com/temporalio/temporal)を推奨する。Event一件や単純なProjection変換には使わず、外部副作用を冪等なActivityへ分離し、retry、cancel、timeout、callback、復旧をWorkflowで管理する。Workflow payloadとSearch AttributeにはProject-scopedな参照IDとhashだけを入れ、Evidence本文、prompt、diff、secretを複製しない。Temporal HistoryはCanonical Ledgerでも監査正本でもない。

Event取り込みは次とする。

```text
Local encrypted WAL
  → Local exclusion / redaction
  → redacted batchをat-least-once送信
  → Cloud Object Storageへ保存
  → PostgreSQL receipt + transactional outbox
  → Canonical Event処理
```

- `event_id`はLocalで生成し、ACKまで同じIDで再送する。
- exactly-once deliveryではなく、at-least-once＋Project-scopedな冪等commitと定義する。
- `collector_instance_id`、`producer_sequence`、`batch_id`を追加する。
- ClickHouseは必要になった時点で再構築可能な検索・集計read modelとして追加する。
- 全Object、queue、cache、Workflow ID、blob path、Provider refを`organization_id / project_id / workspace_id`でscopeし、tenant別concurrency、quota、PostgreSQL RLS、operator監査を設ける。
- `Agent OTLP Ingest Adapter`と`Platform OTLP Exporter`はendpoint、credential、schema、retentionを分離し、運用traceがLearning Caseへ再取り込みされるloopを防ぐ。

[Langfuse](https://github.com/langfuse/langfuse)はOLTP、Object Storage、queue、分析DBの分離を参考にし、Trace/Score modelそのものは採用しない。[OpenTelemetry GenAI](https://github.com/open-telemetry/semantic-conventions-genai)はDevelopment statusの互換Adapterとして使い、機密性の高いprompt、Tool引数、結果は既定送信しない。

### CLI運用

`train`は非同期Job IDを即時返し、`--wait`時だけ追跡する。`status`にはspool件数、最古Event、最終ACK、gap、quarantine、Curator backlog、Dataset Version、Current Modelを表示する。

```bash
imbue doctor
imbue sync
imbue auth whoami
imbue jobs list|show|retry|cancel
```

共通optionは`--project`、`--profile`、`--json`、`--wait`とする。

## 精度の扱い

現行方針どおり、ユーザーへ総合精度スコアや旧モデルとの比較画面を見せず、スコアでCurrent Modelを昇格させない。

ただし、Dataset自体を汚染しないため、次の内部検証は必須とする。

- Curator/Projection回帰は、人間が訂正した別Golden Datasetで行う。
- Training recipe検証はtask、time、repository単位のtemporal cross-validationを使い、同一Task、Work Episode、Decision chainをfold間で跨がせない。
- 最終Personal/Team LoRAは検証後にActive Model-scoped Dataset全体で再学習し、holdout結果をCurrent Model昇格条件にしない。
- exact/near duplicate、DPOの有効入力同一性、Tool Call ID、secret、licenseを検査する。
- target tokenizer適用後のtruncate、loss対象token数、drop理由を保存する。
- Golden DatasetでCuratorの分類、Case分割、Scope、Evidence参照を回帰検証する。
- LoRAとBase revision、tokenizer、chat templateの一致を検査する。
- Deployment後にformat、Tool parse、patch apply、build、test、sandbox replayを確認する。
- RFTは学習用verifierと非公開のholdout verifierを分け、reward hackingを検出する。

これらは事例や好みの強度を数値化する仕組みではなく、学習データとartifactが成立していることの検証である。主観的な改善は引き続き日常利用と後続のUser Preference・Collaboration Outcome Evidenceで扱う。

## 採用しない設計

- ATIF等の内部reasoning / CoTを必須化する。
- merge、編集量、速度、短いTrajectoryを合成したreward scoreを作る。
- Test成功、PR merge、Session成功だけで全Trajectoryを正例化する。
- 同じintent文字列だけを根拠にDPO pairを作る。
- Curatorが合成した未検証Trajectoryを教師にする。
- 一修正ごとに学習する。
- 前回LoRAから無期限にwarm-startする。
- LLM judgeの単一scoreでRFTやモデル昇格を決める。
- Provider固有ID、Dataset形式、GPU SKUをCanonical contractへ入れる。
- [Episodic](https://github.com/StageWhisperIO/episodic)、[OpenPipe](https://github.com/OpenPipe/OpenPipe)、Langfuse等を製品中核としてそのまま組み込む。

これらのOSSからは、hook収集、append-only episode、outcome連携、Dataset export、耐障害性、観測設計を参照し、Imbue固有のLearning Caseとprovenanceを維持する。

## 採用前に検証する事項

1. Codex App Server/CLIの最低version、ChatGPT管理認証、非永続実行、Luna availability、rate limit。
2. Imbue Model GatewayのResponses API互換性と、対象オープンウェイトモデルのCodex Tool精度。
3. FireworksとTogetherで同一Projectionを学習した際の対応model、artifact export、Tool精度。
4. FireworksからArtifact Vaultへのartifact export可否、Base Model保持条件、再deploy。
5. fresh-from-baseとwarm-start＋replayのDataset規模別の挙動。
6. 公開Trajectoryを用いたPlatform Agent Baseの品質とライセンス。
7. 全Tool Observation保存時の容量、redaction、retention。
8. 第二LLMによるEvidence Adjudicatorが精度向上に寄与するか。
9. ActionIntent強制がAgentの行動分布と最終品質へ与える影響。
10. ATIFのversion matrix、unknown field保持、import migration、version固定export。

## 実装の依存順序

これは機能を削った初期版ではなく、完成要件を壊さず実装するための依存順序である。

1. Collector / Ledgerの基盤とCodex Compatibility Profile、Responses API Model Gateway contract
2. policy内Eventの意味保存、Context Window Manifest、Subagent構造
3. Feedback targetとLearning Case
4. target-aware ProjectionとDataset validator
5. Local Codex Broker、Luna Curator Jobと回帰セット
6. Cloud Training、Artifact Vault、Serving Provider
7. 継続学習policyと閉ループ運用

Codex Compatibility ProfileとModel Gateway contractはtarget-aware ProjectionとTraining開始前に確定する。Collector、Ledger、Learning Case schemaは並行実装できるが、Codex Runtimeとの互換性を決めずにProvider用JSONLを固定すると、学習時と推論時のTool protocolが一致せず、データ量を増やしても精度が出ない。
