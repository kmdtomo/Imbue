# 学習とトレーニング

## 初期のローカル構成

初期実装は一人のPCで動かす。本文のPlatformは、初期には同じPC上のGo API・Worker・Model Gatewayを指す。PostgreSQL（pgx＋sqlc）とローカルファイルを正本とし、ジョブはPostgreSQLのテーブルで管理する。クラウドホスティング、Object Storage、外部Queue、サービス認証・課金は後段とする。CodexおよびFireworksによる外部AI実行・学習は維持するため、完全オフライン構成ではない。具体的な保存・認証・起動境界は[技術アーキテクチャ](12-technical-architecture.md)を正本とする。


## 方針

本プロダクトの中核は、高品質なWork EpisodeとLearning Caseを継続的に蓄積・編集し、その人・チーム・repositoryにおけるTask Policy、Tool Policy、Collaboration Policyの学習へ接続することにある。MemoryやSkillsは必要な場合だけ生成する補助経路とする。

仕事のしやすさの改善と、コード・Agent能力の改善を分離する。確認や委任の粒度を合わせることはCollaboration Policyの改善であり、技術的正しさを直接意味しない。未学習の同種Taskで設計、Tool選択、検証、成果品質が改善した場合に、そのScopeでのTask能力が改善したと扱う。

基盤モデルが一般的なコーディング能力を既に持つ場合、個人差は新しい知識の獲得ではなく、既存の選択肢に対する優先順位の差として学習できる可能性がある。少量のExperienceとLoRAでその差を圧縮できることを「低次元の個人適応仮説」と呼ぶ。これは保証ではなく、未学習Taskへの汎化と一般能力の維持を含めて検証する。

学習は一件の新規差分だけではなく、学習時点の `active dataset` 全体をsnapshot化して行う。過去の有効事例を毎回含め、最近の修正だけへ過適合するdelta-only学習を避ける。

## 用途への振り分け

一つのLearning Caseは複数用途へ利用できる。振り分けは恣意的な品質スコアではなく、必要なevidenceが揃っているかという明示条件で決める。

| 用途 | 必要な根拠 | 主な目的 |
|---|---|---|
| SFT | 元の依頼、最終成果、成立を確認できるverification | 最初から望ましい回答・実装を生成する |
| refinement SFT | 初回出力、ユーザーフィードバック、修正後出力 | 自然言語の指摘を正しく反映して修正する |
| DPO | **同一context**に対するrejectedとchosen、選好を示す直接根拠 | 好ましい判断を相対的に選ぶ |
| RFT | 再実行可能な環境と機械的verifier | テスト可能な複数段階の行動を改善する |
| Judge / Outcome Model | state、候補行動、用途別Outcome Evidence | Task、Tool、Collaboration Policyの候補を目的別に評価する |
| Eval | 再現可能な入力、期待条件、判定方法 | 失敗と期待動作を再現する |
| Skill | 明示可能で再現可能な手順・決定的ルール | 重み学習より確実に手順を適用する |
| Memory | 明示的に即時利用する事実、好み、事例 | 有効化時だけ関連contextを補う |

### SFT

最終成果を元の依頼に対する教師として使える場合だけ採用する。最終コードが存在する、明示的に承認された、コミットされた、PRがmergeされたという一事実だけで正解扱いしない。途中の失敗軌跡を完成回答として混ぜない。

元のDecision Pointより後で追加された要求を入力または既知のScoped Judgmentとして利用できない場合、その要求を反映した最終成果を元依頼への教師にしない。ユーザーが中間成果を見て初めて形成した選好や、承認したかった分岐を、最初から一度で到達すべき正解へ変換しない。

### コードと抽象化の扱い

学習データは次の三層から生成する。

1. Evidence Dataに会話、コード、diff、Tool操作、testを観測事実として保持する。
2. Abstract Experience Dataに「どのstateで、何を選び、どう修正され、どの結果が観測されたか」を判断傾向として表す。
3. Training Projection Dataに、用途に必要な最小限のコードcontextと抽象化された判断、期待する行動またはartifact changeを出力する。

最終コードだけを教師にせず、コードを一律に除外もしない。Task、Tool、Collaboration Policyを学ぶprojectionでは、コードは主にstateと結果を接地するcontextとして使う。コード自体をloss対象にするのは、コードレベルの変更に直接Evidenceがあり、Scopeと入力条件を復元できる場合に限る。抽象化文はSemantic Curatorのannotationであり、それだけを正解として学習させない。

### Refinement SFT

`初回出力 -> ユーザー後続発言 -> 修正結果` の連鎖を保持し、フィードバックを反映する能力を学習する。correction、preference refinement、requirement addition、explorationを区別し、追加要件を直前出力の負例にしない。初回品質を上げるSFTとは別projectionとして評価する。

### DPO

chosenとrejectedが同じcontextへの候補である場合に限定する。ユーザーの追加指示によってcontextが変わった再生成結果を、元回答とのDPO pairにしない。対応が曖昧な事例はrefinement SFTかEvalへ回す。

### Judge / Outcome Model

候補行動またはTrajectoryと、後続のOutcome Evidenceの関係を学ぶ。ユーザー承認、機能検証、Collaboration、Delivery、耐久性を一つのrewardへ早期統合せず、目的別の予測または判定として保持する。質問への回答、短いTrajectory、無反応を単独で正例にしない。

### RFT

テスト、静的解析、再現可能な環境制約など、実行結果から判定できるタスクに使う。「良い設計」「本人らしい」といった主観を数値化したgraderを前提にしない。自然言語の好みはSFT/DPOやEvalで扱う。

### RLHFとの関係

セッションごとの指摘、質問への応答、無反応、短いTrajectoryを単一報酬値へ変換するオンラインRLHFは行わない。ユーザー修正は、同一contextの比較が成立すればDPO、実行可能なverifierがあればRFT、それ以外はSFT、refinement SFT、JudgeまたはEvalへ投影する。

### Eval

Evalは、失敗や期待条件を再現可能な形で保存するProjectionとする。技術的失敗だけでなく、同じ明示指示の反復、不要な確認、望まれていない先回り、必要な承認の欠落も、再現・判定できる場合は別Evalとして扱う。

Current Modelへの昇格を要求するCandidateには個人適応Eval、Capability Preservation Eval、Scope外誤適用の検査を必須とする。異なるOutcome軸を単一スコアへ統合せず、passed・failed・inconclusiveを分ける。評価件数不足や未完了はinconclusiveとし、昇格しない。評価はユーザーの体感を代替しないが、モデル切替の必須条件である。

指標、最小件数、改善幅、回帰許容差、不確実性の扱いは実行前に固定する。Work Episodeとその派生事例を跨がせず、train・development・final evaluationを分割する。詳細は[仮説検証と段階的な実装](15-validation-plan.md)に従う。

初期検証では同一基盤モデルを使い、少なくとも未学習版、Skill・Context利用版、チューニング版を比較する。初期検証はhybridのDirect SFTに絞り、後段では生のTrajectory中心、自然言語の抽象化中心、必要なコードcontextと抽象化を併用するhybridの三方式を分け、未学習Taskでの判断一致、反復修正、技術的正しさ、一般能力の回帰を測る。フロンティアモデルは外部参照として扱い、異なる基盤モデル間の差を個人適応の効果と混同しない。

### SkillsとMemory

明文化できる手順はSkillへ、明示的に即時利用したい事実・好み・具体例はMemoryへ投影できる。いずれも任意であり、ファインチューニング用datasetの代替ではない。状況依存の質問、Tool選択、調査深度、設計判断を無理に手順へ列挙せず、推論されたannotationだけから強制ルールを生成しない。

## 継続学習パイプライン

ターン単位の具体的なデータ変換とDatasetの増え方は[会話からモデル学習まで](10-conversation-to-model.md)を参照する。

```text
event ingestion
  -> Evidence Data（Raw Event / code / diff / test）
  -> Abstract Experience Data（Trajectory / Episode / Decision / Case）
  -> Training Projection Data（SFT / DPO / RFT / Judge / Eval）
  -> active dataset snapshot
  -> fine-tuning
  -> artifact validation
  -> model registry / current model更新
```

新規イベントの取り込みとLearning Case生成はイベント駆動で継続する。ファインチューニングは`agent-learning train`の明示実行時だけ独立ジョブとして開始し、`init`やデータ蓄積を自動実行条件にしない。

学習runはactive dataset全体を入力とする。samplingやcurriculumを使う場合も、古い有効事例を暗黙に除外せず、snapshotと設定から再現できるようにする。

## 実行基盤と設定

初期Base ModelはQwen3.8-27Bとし、Fireworks Managed TrainingでLoRA学習する。Canonical Dataset、Projection、Training RunはFireworks固有形式へ固定しない。Base Model、checkpoint、LoRA、推論用weightはクラウドで保持し、ユーザーPCへダウンロードしない。

`agent-learning train`はActive Datasetを固定し、用途、互換性、予算上限からprojection、hyperparameter、学習構成を決定する。`--budget`だけを上級者向けoverrideとして公開し、Provider固有設定は内部contractへ閉じる。

学習完了後のartifactはPlatformのModel Registryから参照する。`agent-learning run`はCurrent Model用のFireworks On-demand Deploymentを作成または起動し、Codexを接続する。DeploymentはScale-to-zeroを標準とし、起動中の`DEPLOYMENT_SCALING_UP`はModel Gatewayが待機・再試行する。初期は利用者がFireworks接続を設定し、ローカルPlatformで予算と利用量を管理する。サービス認証とsubscriptionは後段とする。

## VersionとRollback

各runは少なくとも次を固定する。

- dataset snapshot IDとcontent hash
- 含まれるLearning Case ID
- projection変換version
- base modelまたは親checkpoint
- tokenizer、学習方式、設定、seed
- 学習コードと実行環境version
- Experiment Plan、分割manifest、Eval dataset versionと結果（未完了・判定不能を含む）
- 親checkpointの祖先を含む学習データの来歴と、Scope・Adapter選択規則version
- 出力model artifact

データセット、projection、model、deploymentを別々にversion管理する。学習とartifact検証に成功した新モデルはCandidate Modelとなり、個人適応EvalとCapability Preservation Evalを通過した場合だけCurrent Modelへ昇格できる。既存artifactは上書きしない。問題時は、直前のmodelだけでなく、そのmodelを生んだdataset snapshotと設定まで追跡してrollback・再学習できるようにする。

Learning Caseを後から除外・訂正した場合、既存modelからその影響が自動的に消えたとはみなさない。影響を受けるrunを特定し、対象の旧Caseや派生物を学習していないことを祖先まで確認できる基盤モデルまたはcheckpointから、修正後のactive dataset全体で新しいmodel versionを生成する。旧Caseの影響を持つモデルからの継続学習を、除外反映済みとは扱わない。データ除外済みと配備モデルへの反映完了を別状態で追跡し、通常訂正はstale、削除・秘密情報混入による利用停止は隔離として扱う。詳細は[検証計画](15-validation-plan.md)に従う。
