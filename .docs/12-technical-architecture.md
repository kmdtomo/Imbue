# 技術アーキテクチャ

## プロダクト形態

Imbueは、CLI/CollectorとPlatformを同じPCで動かす、一人用のローカルアプリとして実装を始める。Dataset・Job・Model Registryの正本はPC上に置き、AI実行・学習・モデルweightの保持は外部サービスを利用する。クラウドサービス化は後段とする。Agent実行はCodexへ集約し、Imbueは独自のtool loop、context管理、sandbox、subagent機構を実装しない。Web UIは必須とせず、通常操作はCLIで完結する。

```text
収集: Codex / Claude Code ──► imbued ──► Imbue Platform

整形: Imbue Platform ──Curator Job──► imbued
                                                └──► Codex / Luna
                                                       └──► PatchをPlatformへ返却

実行: imbue run ──► Codex ──Responses API──► Model Gateway
                                                            └──► Fireworks On-demand / Current Model
```

- リポジトリへ実行データやモデルを自動追加しない。
- Codexを`imbue run`とSemantic Curatorの標準かつ必須Runtimeとする。
- ローカルはAgentイベントの収集、redaction、buffer、CLI操作、Codexの起動を担う。
- Platformはアカウント、プロジェクト、Dataset、Curator Job、学習、モデル、利用枠を管理する。
- 初期は利用者がFireworksのアカウントとAPI keyを用意する。Provider固有の設定は接続設定へ閉じ、Dataset contractへ露出させない。サービス側でcredentialを管理する形はクラウド化時に検討する。
- Base Model、checkpoint、LoRA、推論用weightをユーザーPCへ保存しない。

## 採用技術

| 領域 | 技術 |
|---|---|
| Local Daemon / CLI | Go |
| CLI | Cobra |
| Local IPC | Unix Domain Socket、Windows Named Pipe、JSON-RPC |
| Local buffer / index | SQLite、append-only JSONL、content-addressed cache |
| 設定 | TOML |
| Contract | JSON Schema |
| Dataset export | JSONL、必要時にParquet |
| Platform API / Orchestrator | Go |
| Platform metadata | ローカルPostgreSQL（開発時はDocker Composeで起動、永続volumeを使用） |
| Platform DB access | pgx + sqlc（pgxで接続・実行し、sqlcでSQLから型付きGoコードを生成） |
| Dataset / artifact storage | ローカルfilesystem（content-addressed blob、版固定manifest） |
| Background Job | PostgreSQLのジョブテーブル＋Go Worker |
| Agent Runtime | Codex CLI / Codex App Server |
| Semantic Curator | Codex上のステートレスなGPT-5.6 Luna Run |
| Model Gateway | Codex向けOpenAI Responses API互換Gateway |
| Training / Inference | 初期実装はFireworks。Qwen3.8-27BをManaged TrainingでLoRA学習し、On-demandへ配備 |

Goは、単一バイナリ配布、常駐Collector、subprocess監視、filesystem処理、並行Jobを一つのruntimeで扱うために採用する。標準構成にローカルPython学習環境を含めない。ローカル学習を追加する場合も、Cloud Providerと同じinterfaceを実装する任意Adapterとする。

## Process構成

```text
Codex / Claude Code / その他Agent
        │
        ▼
Provider Adapter / Hook
        │
        ▼
imbued（Go）
  ├─ Event Collector
  ├─ Local Redactor
  ├─ Upload Queue / Retry
  ├─ Local Cache / Export
  └─ Codex Broker
        ├─ Stateless Luna Curator Run
        └─ Current Model Run
        │ loopback HTTP / local IPC
        ▼
Imbue Platform
  ├─ Local Owner / Project
  ├─ Evidence Data / Event Ingestor / Ledger / Artifact
  ├─ Abstract Experience Data / Trajectory / Episode / Decision Point
  ├─ Candidate Builder
  ├─ Semantic Curator Job Queue
  │     └─ Evidence Packet / Result Contract
  ├─ Evidence Validator
  ├─ Training Projection Data / Dataset / Projection Engine
  ├─ Training Orchestrator
  ├─ Model Registry
  └─ Usage / Budget（課金・subscriptionは後段）
        │ Provider Adapter
        ▼
Fireworks（初期実装）/ その他Provider
  ├─ Base Model
  ├─ Training GPU
  ├─ Checkpoint / LoRA
  └─ Inference Deployment
```

Collectorは収集失敗時にもAgent作業を止めず、再送可能なbufferへ保存する。PlatformはEvidence Data、Abstract Experience Data、Training Projection Dataを別々に版管理し、Providerへは固定Dataset Versionと実行設定だけを渡す。コード・diff・testはEvidenceとして保持し、自然言語の抽象化後も参照可能にする。

## 責務境界

### Local CLI / Collector

- Agent Adapter、Managed Tool Gate、Observe Modeの実行
- 送信前の除外、secret検出、redaction
- offline buffer、再送、状態表示
- Datasetの確認・編集・export操作
- Platformへの認証済みcommand送信
- PlatformのCurator Jobを取得し、ユーザーのChatGPT管理認証下でCodex/Lunaを起動する
- `imbue run`用のCodex Profileを生成し、Codexを起動する

### Codex Runtime

- tool loop、shell・filesystem操作、context・compaction、sandbox、approval、MCP、Skill、subagentを実行する
- Semantic Curatorをユーザーの作業Taskとは別の非対話・非永続Runとして実行する
- Current Model利用時はImbue Model Gatewayをcustom model providerとして呼び出す
- ChatGPTのaccess/refresh tokenをImbue Platformへ渡さない

### Imbue Platform

- Local Owner、Project、利用量、予算上限。Account・subscriptionはクラウド化時に追加
- Evidence Data、Abstract Experience Data、Training Projection Dataの保存と版管理
- Curator JobとEvidence Packet、Evidence Validator、Projection生成
- 学習設定の自動決定、Job監視、再試行
- Model Registry、Current Model、rollback、Deployment lifecycle
- Provider credentialとユーザー別利用量の管理
- CodexへCurrent Modelを提供するResponses API互換Model Gateway

### Training / Inference Provider

- Base Modelと学習済みartifactの保持
- GPU上のfine-tuning
- checkpoint、LoRA、deploymentの生成
- inference endpointの提供

初期ProviderはFireworksとする。Provider固有IDと設定はPlatform内部に閉じ、Canonical DatasetとCLI contractへ露出させない。

## Codex Runtime境界

`imbue run`はImbue独自のAgent Harnessを起動せず、生成済みProfileでCodexを起動する。CodexはImbue Model Gatewayをcustom model providerとして利用する。

```toml
model = "current"
model_provider = "imbue"

[model_providers.imbue]
base_url = "http://127.0.0.1:8787/codex/v1"
env_key = "IMBUE_TOKEN"
wire_api = "responses"
```

Imbue Model GatewayはCodexが要求するResponses API contractを提供し、Current ModelのDeploymentへ変換する。Fireworks等が同contractを直接提供できても、認証、tenant分離、利用量、model aliasを統一するためGatewayを製品境界とする。

Imbueが保持するのは`Codex Compatibility Profile`であり、独自Runtimeではない。最低限、Codex version、model/provider設定、instruction version、tool capability、context/compaction policy、Responses API互換versionを固定する。学習側のtokenizer、chat template、loss maskはModel/Profile側で別にversion固定する。

生成Profileとcredential参照は`~/.imbue/`またはCodexのユーザー設定へ置き、対象repositoryへ自動追加しない。CollectorやManaged Tool Gateは観測・記録のIntegrationであり、Agentの実行ループを持たない。

## Provider Contract

```go
type TrainingProvider interface {
    UploadDataset(ctx context.Context, snapshot DatasetSnapshot) (DatasetRef, error)
    StartTraining(ctx context.Context, req TrainingRequest) (TrainingRef, error)
    GetTraining(ctx context.Context, ref TrainingRef) (TrainingState, error)
    GetArtifact(ctx context.Context, ref TrainingRef) (ModelRef, error)
}

type InferenceProvider interface {
    EnsureDeployment(ctx context.Context, model ModelRef) (DeploymentRef, error)
    LoadAdapter(ctx context.Context, deployment DeploymentRef, model ModelRef) error
    RunInference(ctx context.Context, deployment DeploymentRef, req InferenceRequest) (InferenceResponse, error)
    StopDeployment(ctx context.Context, deployment DeploymentRef) error
}
```

`TrainingRequest`にはDataset hash、base model、projection type、予算上限、再現用設定を含める。ProviderのGPU SKUやLoRA実装詳細はAdapterが変換する。

## Go package境界

```text
cmd/
  imbue/       # CLI
  imbued/      # Local Collector

internal/
  adapter/              # Codex、Claude、Generic
  collect/              # hook、tool gate、event正規化
  codex/                # App Server/CLI起動、profile、capability check
  curator/              # Local Luna Broker、schema入出力
  redact/               # 除外とsecret処理
  spool/                # offline buffer、retry、cursor
  client/               # Platform API client
  rpc/                  # CLIとDaemonのLocal JSON-RPC
  export/               # Dataset、監査データの可搬出力

platform/
  ingest/
  trajectory/
  episode/
  decision/
  outcome/
  curator/
  case/
  projection/
  dataset/
  training/
  model/
  gateway/responses/
  provider/fireworks/
```

Agent固有処理はlocal `adapter`、学習基盤固有処理はPlatformの`provider`へ閉じる。

## 保存構成

```text
~/.imbue/
  config/config.toml
  auth/credential-ref
  state/collector.db
  data/projects/          # Ledger・Evidence・Dataset・manifestの正本
  spool/events/
  cache/
  exports/
```

- `spool/`は送信完了後に保持ポリシーで削除できる一時bufferである。
- `cache/`は再取得可能な表示・編集用cacheであり、正本にしない。
- `exports/`はユーザーが明示的に生成する可搬Datasetであり、必要ならGit管理できる。
- credential本体はOS credential storeへ保存する。
- model weight用ディレクトリは設けない。

ローカルPlatformではPostgreSQLにLocal Owner、Project、Job、Case index、version、利用量を、`data/projects/`にLedger、Evidence、Dataset、manifestを保存する。SQLiteはCollectorのbuffer・cursor用であり、PostgreSQLの代替ではない。Model artifact本体はProviderに保持し、Platformは参照ID、hash、provenance、状態をModel Registryで管理する。

## CLI

初期の通常操作は次の3コマンドを中心にする。

```bash
imbue init
imbue train
imbue run
```

初期はサービスへの`login`を必要としない。`init`でローカル所有者・Project・保存先・接続設定を作成し、DBとローカルAPIの利用可否を確認する。FireworksのAPI keyはOS credential storeに保存し、設定には参照だけを置く。未設定でも収集・Case管理を使えるようにし、学習・推論時に不足を表示する。これとは別に、Semantic CuratorにはChatGPT管理認証済みのCodexを必須とし、そのcredentialはローカルのCodexだけが保持する。

```bash
imbue status
imbue case list
imbue case show <id>
imbue case edit <id>
imbue dataset diff <a> <b>
imbue dataset export --format jsonl
imbue training status
imbue model list
imbue model rollback <version>
```

`imbue run`はCodexを生成Profileで起動し、Current ModelをModel Gateway経由で利用する。独自TUIやAgent loopは起動しない。

初期Base ModelはQwen3.8-27Bに固定し、Platformがその他の学習設定を決定する。必要な場合だけ次を上級オプションとして提供する。

```bash
imbue train --budget <limit>
```

## 配布と安全境界

- 署名済みGoバイナリをpackage managerまたはinstallerで配布する。
- Daemonはユーザー権限で動かし、Local Socketを同一ユーザーだけに許可する。
- LocalとPlatformの双方でsecret検出とredactionを行う。
- CuratorはChatGPT管理認証下のCodexで、固定schema、限定Evidence、無効化した外部Toolによるステートレス実行とする。
- Codex/Lunaが利用できない場合はCurator Jobを保留し、Imbue管理のAPI実行へ自動fallbackしない。
- Providerへはredaction済みの固定Dataset Versionだけを渡す。
- Provider credentialはローカルOS credential storeで管理し、log、Dataset、repositoryへ記録しない。
- 初期はProvider利用量と予算を記録する。subscriptionと無料枠の製品化は後段とする。

## ローカル起動・永続化の実装方針

- Dockerで動かすのはPostgreSQLだけとする。GoのCLI・Daemon・API・Worker・Curator Broker・GatewayはMac上で直接実行する。初回はログ収集までを実装し、Luna整形・学習・Gateway実行は後続段階で追加する。
- 整形はChatGPT管理認証済みのCodex内でGPT-5.6 Lunaを非対話・非永続Runとして起動する。ImbueからOpenAI APIを直接呼び出す構成にはしない。既定は同じ会話の未処理10 Turnで起動し、詳細は[Semantic Curator](11-semantic-curator.md)に従う。

- 初期は`imbued`内にCollector、API、Worker、Curator Broker、Gatewayを同居させる。packageの責務境界は維持し、別サーバーの配備を必須にしない。
- PostgreSQLはDocker Composeで起動し、DB接続設定はTOMLにcredential参照として保持する。通常停止で永続volumeを削除しない。
- APIとGatewayは`127.0.0.1`にのみbindし、ローカル生成tokenを要求する。CLI・Codexへ安全に渡し、外部認証サービスは導入しない。Local IPCは同一OSユーザーに制限する。
- ジョブテーブルはlease、attempt、next_run_at、冪等キー、状態を持ち、Workerがtransaction内で取得する。異常終了後はlease失効で再開できるようにし、外部学習の重複開始はProviderの実行参照と照合する。
- ファイルは一時書き込み・hash検証・atomic rename後にDB参照を確定する。途中失敗時の孤立ファイルは照合・回収し、保存前にCollectorのbufferを消さない。
- PostgreSQLと`data/`を整合した単位でバックアップ・復元する。`data/`はcacheと異なり、自動掃除の対象にしない。
- 所有者はOSユーザーに紐づくローカルIDとし、Project・repository・task familyのScope判定、削除伝播、redaction、必須Evalは初期にも適用する。
- 外部送信はCodexによるCurator実行、Fireworksへの学習Dataset送信、推論要求等で発生する。アプリとデータの正本がローカルでも、完全オフライン・完全ローカル推論とは表現しない。
- HTTPルーター、migrationツール、最低Codex version、CLIとApp Serverの使い分け、Responses API互換性は実装前に具体化する。クラウドホスティング・Object Storage製品・外部Queue・認証サービスの選定は初期実装の前提にしない。
