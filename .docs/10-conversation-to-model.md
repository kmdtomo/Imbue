# 会話からモデル学習まで

## 目的

一つの会話をそのまま学習へ流さず、会話、質問、Tool実行、成果物、自然言語による連続修正、承認、検証、後日の結果をWork Episodeとして接続する。そこから、その時点で利用可能だった情報と根拠を持つLearning Caseだけを作り、適格な形式だけをモデル学習へ使う。

```text
Evidence Data
  -> 会話 / Tool / code / diff / test
Abstract Experience Data
  -> Normalized Trajectory / Work Episode / Decision Point / Learning Case
Training Projection Data
  -> SFT / refinement SFT / DPO / RFT / Judge / Eval
  -> Active Dataset snapshot
  -> fine-tuning
```

## ターンごとの処理

1. ユーザー発言を受信したら、直前のAgent作業との関連候補を機械的に記録する。
2. AgentのTool、ActionIntent、diff、testをRaw Eventへ追記する。
3. Agentが制御を返したら、その時点で利用可能だったcontext、Decision Point、質問または実行、成果物、後続発言、Outcome EvidenceをWork Episodeへ接続する。
4. 同じ会話の未処理の完了Turnが既定10件たまった時、または手動要求時に、Codex内のLunaを起動する。Semantic Curatorがcorrection、preference refinement、requirement addition、exploration、approval、delegation、change of mind等の関係候補を付け、一つ以上のLearning Caseへまとめて整形する。Tool呼び出しや進捗説明はTurnに数えず、毎Turnの整形は行わない。
5. Evidence条件を満たす用途だけへProjectionする。

セッション終了は待たない。修正が続けば既存Caseを更新し、別の判断なら新しいCaseへ分ける。Sessionの終了、無反応、Agentのfinal messageを成功または承認とみなさない。

## 具体例

### 自然言語だけで編集する

```text
C0: 実装前
User: 設定画面を作って

C1: Agentが初回実装
User: もっとシンプルに

C2: Agentが設定項目も削減
User: 項目は残して、見た目だけシンプルに

C3: Agentが再修正
User: これでいい

C4: 明示的に受け入れられたartifact
```

ユーザーがdiffを見なくても、各Agent Turnの前後でartifact snapshotとdiffを取得する。C4はこのcontextでユーザーが受け入れた状態であり、唯一の正解コードではない。

この例では次を分離する。

- C1からC2は「シンプルに」という相対的な選好を反映したrefinement候補。
- 「項目は残して」がC0時点の暗黙要求だったか、C2を見て初めて形成された要求かは観測だけでは確定しない。
- C2を無条件なrejected、C3をC0へのchosenとしてDPO pairにしない。
- 「これでいい」はUser Preferenceの直接Evidenceだが、testやreviewの代わりにはならない。
- C0からC4へのDirect SFTは、C4の条件がC0時点で利用可能だったとEvidence Validatorが確認できる場合だけ許可する。

一方、次のRefinement SFTは構成できる。

```text
input  = C2時点のcontext + 「項目は残して、見た目だけシンプルに」
target = C3を作った実際の行動とartifact change
```

### 確認を好む可能性がある

過去にC4へ到達するまで3回の確認があったとしても、最短化を正解にしない。ユーザーは中間成果から要求を発見した、または重要な分岐を一つずつ承認したかった可能性がある。

質問後にユーザーが毎回「はい」と答えた事実だけでは、確認を好むとも、質問が不要だったとも確定できない。方針変更、新しい制約、明示的な確認要求、「任せる」「いちいち聞かないで」等の直接EventをOutcome Evidenceとして別々に保持する。

したがって学習目標は「3往復を1往復にする」ではなく、回避可能な修正と、望まれている確認・探索を区別することである。

### 実装・検証を伴う例

#### 1. 最初の実装

依頼:

> 請求確定APIを既存設計に合わせて実装し、APIレスポンスは変更しない。

Agentが請求状態を更新した後、イベントブローカーへ直接publishした。Unit Testは成功した。この時点では正解扱いせず、Raw EventとTrajectoryだけを保存する。

#### 2. トランザクション境界の修正

フィードバック:

> 請求更新とoutboxへの追加を同じtransactionで行い、既存のInvoiceOutboxRepositoryを使う。

Agentが同一transactionへ修正し、rollbackを含むIntegration Testが成功したら、次のCaseを生成する。

```json
{
  "case_id": "case-001",
  "case_version": 1,
  "feedback": {
    "relation_kind": "correction",
    "interpretation": "状態更新と対応イベントの永続化を同一transactionで確定する"
  },
  "before": {"artifact_refs": ["diff:direct-publish"]},
  "after": {"artifact_refs": ["diff:transactional-outbox"]},
  "outcome_evidence": [
    {"dimension": "user-preference", "event_ids": ["feedback:transactional-outbox"]},
    {"dimension": "functional", "event_ids": ["test:rollback"]}
  ],
  "applicability": {
    "conditions": [
      "DB更新とドメインイベントを不可分に確定する必要がある",
      "既存のtransactional outbox基盤がある"
    ],
    "generality": "workspace"
  },
  "evidence_state": "grounded"
}
```

#### 3. 冪等性設計の修正

続くフィードバック:

> request IDはbackfillに存在しない。invoice ID、event type、aggregate versionで一意にする。

これはtransaction境界とは別の判断なので、再送・retry・backfillでも再構成可能な冪等性キーを扱う `case-002` とする。`case-001`の修正直後を示す`after`は変えず、後続artifactとEventは同じWork Episodeの新しいDecision Pointとして接続する。

#### 4. 追加要件

> retryをまたいだ障害調査のため、outboxへtrace IDも保存する。

これは以前の実装の誤りとは限らない。`relation_kind=requirement_addition` とし、元の依頼に対するrejected/chosenや初回SFTへ変換しない。

## 三層のデータと学習形式

会話、Tool、コード、diff、testはEvidence Dataとして保持する。Learning Caseは、そこから判断と結果の関係を表したAbstract Experience Dataである。学習形式はLearning Caseと元Evidenceから再生成するTraining Projection Dataとする。

コードは最終成果だけを正解として取り出さず、判断が行われたstateと結果を接地する証拠として残す。Projectionでは目的に必要なコードだけをcontextへ含め、コード自体をtargetにする場合は、その変更が望まれたと判断できる直接Evidenceとverificationを要求する。自然言語の抽象化だけで元Evidenceを置き換えない。

| Projection | この例での利用 | 条件 |
|---|---|---|
| SFT | 最初からtransactional outboxを選ぶ | 元依頼、関連コード、最終成果、verificationが揃う |
| refinement SFT | 初回実装へ修正を反映する | 初回出力、フィードバック、修正後出力が揃う |
| DPO | 利用しない | 修正版は追加フィードバックを含み、初回と同一contextではない |
| RFT | transaction、retry、backfillを検証する | 再実行可能な環境と機械的verifierがある |
| Judge | 候補設計と用途別Outcome Evidenceの関係を学ぶ | Outcome軸とstateを分離して復元できる |
| Eval | 同じ失敗条件を再現する | 入力、期待条件、判定方法を固定できる |

Tool利用を学習できるモデルには、関係するActionIntent、Tool call、必要な観測、最終diff、testだけをTrajectory SFTとして出力する。関係のない探索、重複read、長大なlog、採用されなかった途中試行は除く。

## Datasetの増え方

```text
dataset-v1 = case-001@1
dataset-v2 = case-001@2 + case-002@1
dataset-v3 = case-001@2 + case-002@1 + case-003@1
```

- Raw Eventは追記型で残す。
- Active Datasetには各Caseの現在有効なversionだけを含める。
- Caseの訂正・分割・除外後はProjectionを再生成する。
- 同じEventの再送は重複排除する。
- 別タスクで同じ修正が再発した場合は、反復した実例として別Caseに残す。
- 追加要件を過去のAgent判断の誤りへ変換しない。
- Session終了や無反応を正例追加の契機にしない。
- 後日の承認、修正、CI、Review、Revertは既存Work Episodeへ追加し、Projection適格性を再計算する。

## モデル更新

整形はターンごとに行うが、fine-tuningは独立したTraining Runとする。学習時点のActive Dataset全体を固定し、新規Caseだけのdelta学習にはしない。

```text
Model v1 <- Dataset v1
Model v2 <- Dataset v2 全体
Model v3 <- Dataset v3 全体
```

各RunはDataset hash、Case version、Projection version、base model、trainer設定、学習コード、生成artifactを記録する。成功した新モデルはCandidate Modelとして保持し、個人適応と一般能力回帰のEvalを通過した場合だけCurrent Modelへ昇格する。旧モデルと旧Datasetはrollback用に保持する。
