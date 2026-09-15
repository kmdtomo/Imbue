# データモデル

## 原則

- 観測した事実を追記型で保存し、後から得た解釈で上書きしない。
- 学習の一次根拠は `context / trajectory / decision / action / feedback / before / after / outcome evidence` とする。
- AIが推論した「なぜ」は `annotation` であり、一次根拠ではない。
- 完全な内部思考や隠れたCoTは収集要件にしない。
- 根拠への参照を作れない推論はLearning Caseへ昇格させず、raw eventに留める。
- 完全重複は除外する。一方、別の場面で同じ修正が反復された事実は自然な学習信号として残す。
- `confidence` や `strength` のような恣意的スコアは持たない。事実の種類、由来、検証状態をそのまま保持する。
- Session終了、無反応、最終artifact、短いTrajectoryを成功の代理変数にしない。
- ユーザー選好、機能検証、協働、Delivery、耐久性のOutcome Evidenceを別々に保持する。

## 三層のデータ構成

本システムは、コードを消して自然言語へ置き換えるのではなく、次の三層を別々に版管理する。

| 層 | 内容 | コードの扱い |
|---|---|---|
| 1. Evidence Data | Raw Event、発言、Tool操作、artifact、diff、test、CI | 判断時の状態と結果を示す一次証拠として保持する |
| 2. Abstract Experience Data | Trajectory、Work Episode、Decision Point、Outcome Observation、Learning Case、annotation | コードから推測した意味で上書きせず、必ず一次証拠を参照する |
| 3. Training Projection Data | SFT、refinement SFT、DPO、RFT、Judge、Eval等の入力・target | 用途に必要な最小限をcontextまたはtargetへ含め、loss対象を明示する |

第一層は再解釈可能な正本、第二層は根拠付きで編集可能な抽象化、第三層は再生成可能な用途別派生物である。第二層と第三層から第一層へ追跡できなければならない。

コードは次の規則で扱う。

- 最終コード、commit、mergeが存在するだけでは、コードを教師targetにしない。
- コードを一律に除外しない。判断が適用されたstate、変更範囲、成立結果を理解するためのgroundingとして保持する。
- 自然言語へ抽象化しても、元のsnapshot、diff、testへの参照を失わない。
- AIが生成した抽象化はannotationであり、観測された選択・修正・検証なしに教師ラベルへ昇格させない。
- コード自体をtargetにするのは、入力条件、修正関係、Outcome Evidence、Scopeが用途別Validatorを満たす場合に限る。

## 1. Immutable Raw Event

Codex、Claude Code、Git、CIなどから受け取ったイベントの原本。追加訂正は新しいイベントで表し、既存イベントは変更しない。大きなコード、画像、ログはcontent-addressed blobとして外出しできる。

```ts
type RawEvent = {
  id: string
  schemaVersion: string
  sessionId: string
  sequence: number
  occurredAt: string
  source: "codex" | "claude-code" | "git" | "ci" | "user" | string
  actor: "user" | "agent" | "tool" | "system"
  kind: string
  payload: unknown
  artifactRefs: ArtifactRef[]
  parentEventIds: string[]
  contentHash: string
  sensitivity: "normal" | "personal" | "secret"
}
```

## 2. Normalized Trajectory

プロバイダー固有イベントを共通の行動列へ正規化したもの。メッセージ、ツール呼び出し、観測結果、ファイル変更、サブエージェントを順序と因果参照付きで表す。raw eventへの参照を必須とし、正規化し直せるようにする。

```ts
type TrajectoryStep = {
  id: string
  trajectoryId: string
  order: number
  kind: "message" | "tool-call" | "observation" | "artifact-change" | "handoff"
  rawEventIds: string[]
  inputArtifactRefs: ArtifactRef[]
  outputArtifactRefs: ArtifactRef[]
  causedByStepIds: string[]
}
```

Trajectoryはセッションと同一とは限らない。一つのタスクが継続セッションやサブエージェントへまたがる場合も、参照で接続する。

## 3. Work EpisodeとDecision Point

Work Episodeは、一つの仕事の目的に関係するTrajectory、Decision Point、artifact、遅延Eventを接続する開いた単位である。`quiescent`は観測上の休止であり、成功または完了を意味しない。

```ts
type WorkEpisode = {
  id: string
  goalEventIds: string[]
  trajectoryStepIds: string[]
  decisionPointIds: string[]
  artifactRefs: ArtifactRef[]
  outcomeObservationIds: string[]
  relatedEpisodeIds: string[]
  observationState: "active" | "quiescent" | "resumed" | "abandoned" | "unknown"
}

type DecisionPoint = {
  id: string
  episodeId: string
  availableContextEventIds: string[]
  causedByStepIds: string[]
  actionKind: "ask" | "propose" | "inspect" | "edit" | "verify" | "deliver" | "yield"
  actionEventIds: string[]
  feedbackEventIds: string[]
  beforeRefs: ArtifactRef[]
  afterRefs: ArtifactRef[]
  outcomeObservationIds: string[]
  scopeRefs: ScopeRef[]
}
```

`availableContextEventIds`にはDecision Pointより前に利用可能だった情報だけを含める。後続発言で追加された要求を過去のDecision Pointへ書き戻さない。`after`は行動直後のartifactであり、後続の最新artifactと同一視しない。

質問、提案、制御返却も`actionKind`として保持する。質問後に回答があった事実だけでは、その質問の有益性を確定しない。

```ts
type OutcomeObservation = {
  id: string
  episodeId: string
  decisionPointIds: string[]
  dimension: "user-preference" | "functional" | "collaboration" | "delivery" | "durability"
  kind: string
  observedEventIds: string[]
  observedAt: string
}
```

明示的承認、修正、test、CI、review、merge、後続修正、revert等を`OutcomeObservation`として保存する。総合的な`success`や`good response`へ圧縮せず、後続Eventが到着した場合は新しいObservationを追加する。

## 4. Learning Case

一つ以上のWork EpisodeとDecision Pointを、学習・Eval・Skill・Memoryへ再利用できる形にした編集可能な単位。根拠を要約したものではなく、根拠への参照を持つ。

```ts
type LearningCase = {
  id: string
  evidence: {
    episodeIds: string[]
    decisionPointIds: string[]
    availableContextEventIds: string[]
    trajectoryStepIds: string[]
    feedbackEventIds: string[]
    beforeRefs: ArtifactRef[]
    afterRefs: ArtifactRef[]
    outcomeObservationIds: string[]
  }
  annotations: Annotation[]
  scopeRefs: ScopeRef[]
  state: "active" | "conflicted" | "superseded" | "excluded" | "tombstoned"
  supersedesCaseIds: string[]
  duplicateOfCaseId?: string
}

type Annotation = {
  text: string
  kind: "feedback-relation" | "question-purpose" | "inferred-reason" | "applicability" | "exception" | "note"
  authoredBy: "user" | "agent"
  groundedInEventIds: string[]
}
```

annotationが誤っていてもevidenceは失われない。ユーザーによる修正はannotationの新しい版として残し、データセットの履歴から追跡できるようにする。

## Evidence Chain

各Learning Caseから次の経路をたどれることを必須とする。

```text
Learning Case
  -> Work Episode / Decision Point
  -> Normalized Trajectory Step
  -> Immutable Raw Event
  -> Artifact / diff / test output
```

派生物には生成元ID、変換コードのversion、生成時刻を記録する。削除要求はraw、派生物、export、学習runの参照を辿って処理する。

## Projection

ProjectionはLearning Caseを用途別フォーマットへ変換した再生成可能な派生物であり、正本ではない。

- SFT / refinement SFT dataset
- DPO pair
- Judge / Outcome Model dataset
- RFT rollout taskとverifier入力
- Eval case
- Skill候補
- Memory候補

各projectionは `sourceCaseIds`、変換version、dataset snapshot IDを保持する。同じLearning Caseを複数用途へ投影してよいが、用途ごとの適格条件は独立して判定する。

## 検証・配備の追加レコード

[検証計画](15-validation-plan.md)に対応して、以下を版管理する。

| Record | 必須情報 |
|---|---|
| Experiment Plan | 対象仮説、Scope、比較条件、指標、最小件数、反復回数、改善幅・回帰許容差、不確実性算出法、費用・時間上限、固定時刻 |
| Split Manifest | Episode・成果物の派生関係group ID、train/development/final evaluation割当、分割version・hash、評価集合の使用履歴 |
| Audit Record | 標本抽出法・分母、元Evidence、監査者、本人照合、分類・Scope・教師適格性の判定、不一致、訂正前後 |
| Evaluation Run | 計画・分割・dataset・model参照、実行環境、条件別結果・件数・不確実性、passed/failed/inconclusiveと理由 |
| Model Lineage | 基盤モデル、親checkpointと祖先、学習Case version・派生物参照、Scope、Adapter ID、来歴確認状態 |
| Scope Selection | 所有者、実行Scopeと根拠、選択Adapterまたは基盤モデル、選択理由、規則version |
| Correction Impact | 対象Case旧版、依存Dataset・Run・配備先、データ除外状態、モデルstale/隔離状態、再学習元、反映完了した配備version |

監査の判定はCaseの承認待ち状態として扱わない。評価の判定不能は不合格と区別し、どちらもモデル昇格を許可しない。削除対象の本文や秘密値を監査レコードへ複製せず、識別子の保持も削除ポリシーに従う。
