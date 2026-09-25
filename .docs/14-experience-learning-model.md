# Experience Learning Model

## 目的

Imbueは、完成コードや明示的な修正だけを集める仕組みではない。会話、質問、ツール選択、実行、成果物、ユーザーの後続発言、検証、後日の修正を一つの仕事の経験として接続し、言語化されていないが行動に現れる判断傾向を学習可能にする。

本書は、観測された仕事の流れをどのように整理し、何を事実として残し、どの条件でLearning Caseや学習Projectionへ昇格させるかを定義する。基盤モデルに一般能力を追加することより、少量・低ランクの追加学習で個人の判断傾向が定着し、未学習Taskへ汎化するかを検証することに重点を置く。

## 学習するもの

本プロダクトが学習対象とするのは、一般知識だけではなく、実務で繰り返される次の判断である。

- Task Policy: 何を実装し、どの設計・変更範囲・検証方法を選ぶか。
- Tool Policy: 何を調べ、どのToolをどの順序と深さで使うか。
- Collaboration Policy: いつ質問し、案を見せ、承認を求め、委任を受けて進めるか。
- Scoped Judgment: 個人、チーム、repository、task familyごとに異なる品質・選好の境界。

これらは「常にこのコマンドを実行する」のような決定的手順とは異なる。状況による例外と競合が多く、本人が完全に言語化できないため、Skillだけへ列挙しない。

ここで学ぶのは人間の非公開な内部思考ではない。依頼の解釈、選択した方針、変更範囲、Tool利用、検証、修正、承認に現れる観測可能な判断傾向である。基盤モデルが既に持つ能力のうち何を優先するかという差が、少数の判断軸と小さな重み差分へ圧縮できる可能性を仮説として扱う。

## Context、Memory、Skillとの境界

| 層 | 主に扱うもの | 例 |
|---|---|---|
| Context / RAG | 今回必要な外部事実 | 現在のAPI仕様、対象ファイル、Issue |
| Memory | 後で想起する明示的な事実・背景 | 担当領域、過去の決定、ユーザーが明言した好み |
| Skill / Rule | 明文化できる安定した知識・規約・複数段階のWorkflow | 設計原則、コーディング規約、Toolの使い分け、実装から検証・PRまでの手順 |
| Experience Learning | 状況依存の行動選択と協働判断 | どこまで調べるか、今は質問するか、今回は抽象化するか |

Learning Caseは必ず重み学習へ送るものではない。明文化できる安定手順はSkill、参照すべき事実はMemoryまたはContext、再現可能な失敗はEval、例から学ぶ必要がある判断境界はSFT、Preference、Judge、Adapter等へProjectionする。

## 三層の学習データ構成

コードを自然言語へ変換して捨てるのではなく、次の三層を維持する。

### 1. Evidence Data

ユーザー発言、Agent出力、Tool操作、コードsnapshot、diff、test、CI、review等の観測事実。コードは特定の判断が生じたstateと結果を示すgroundingであり、後から別の抽象化を作れるよう保持する。

### 2. Abstract Experience Data

Evidence DataをWork EpisodeとDecision Pointへ接続し、「どの情報がある状態で何を選び、どう修正され、何が起きたか」をLearning Caseとして表す。判断傾向、Scope、例外は自然言語または構造化フィールドで表すが、Semantic Curatorの解釈はannotationであり、元Evidenceを置き換えない。

### 3. Training Projection Data

Learning CaseをSFT、refinement SFT、DPO、RFT、Judge、Eval等の形式へ変換した派生物。Task、Tool、Collaboration Policyを学ぶ場合は、必要最小限のコードcontextと抽象化された判断を組み合わせる。コード自体を教師targetにするのは、入力条件、修正関係、Outcome Evidence、Scopeが成立する場合に限る。

三層を分ける目的は、コードの表層を丸暗記させることと、コードを捨てて一般論だけを学ばせることの両方を避けるためである。抽象化は汎化候補を作り、コードと実行結果はその候補を現実のstateへ接地する。

## 観測単位

### Sessionは学習単位ではない

UI上のsession、thread、Agent Turnは収集境界には使えるが、仕事の意味的な終了を表さない。

- 一つの仕事が複数sessionや日付をまたぐことがある。
- 一つのsessionで複数の仕事が並行することがある。
- ユーザーの無反応は完了、承認、失敗のいずれも意味しない。
- 後日のテスト、レビュー、障害、revertが過去の判断を再評価することがある。

したがって、Eventは時系列だけで結合せず、目的、対象artifact、参照関係、ActionIntent、変更の継続性、ユーザー発言の対象からWork Episodeへ関連付ける。

### Work Episode

Work Episodeは、一つの仕事の目的に関係するEvent、Trajectory、Decision Pointを接続する開いた単位である。`inactive`や`quiescent`になっても成功とはみなさず、後続Eventで再開・再評価できる。

```text
Goal / Request
  -> Decision Point
  -> Action / Question / Proposal
  -> Observation
  -> User Feedback
  -> Revision / Verification
  -> Delayed Outcome
```

### Decision Point

Decision Pointは、Agentがその時点で利用可能だった情報から次の行動を選んだ単位である。

- 質問する
- 計画または選択肢を提示する
- Toolで調査する
- artifactを変更する
- 検証する
- ユーザーへ制御を返す

後から判明した要件や結果を、その時点で利用可能だったcontextへ含めない。これにより、未来の情報を使って過去の行動を評価するhindsight leakageを防ぐ。

## 自然言語による連続修正

ユーザーがdiffやコードを直接見ず、自然言語だけでAgentへ編集を続けさせる場合も、Agentが作った各artifact snapshotを接続する。

```text
C0: 実装前
  -> User: 最初の依頼
C1: Agentの初回実装
  -> User: もっとシンプルに
C2: 簡略化した実装
  -> User: 設定項目は残して
C3: 再修正版
  -> User: これでいい
C4: 明示的に受け入れられた状態
```

C4は唯一の正解コードではなく、このcontextでユーザーが受け入れたartifactである。価値があるのはC4だけでなく、C1からC4へ収束する過程と、各変更を生じさせた発言との関係である。

## フィードバック関係の分類

後続のユーザー発言は、直前の出力への一律な正負ラベルにしない。

| 関係 | 例 | 学習上の扱い |
|---|---|---|
| correction | 「違う。既存関数を使って」 | 直前判断への明確な修正候補 |
| preference refinement | 「もっとシンプルに」 | 特定方向の相対選好候補 |
| requirement addition | 「あとログも追加して」 | contextの追加。直前出力を負例にしない |
| exploration | 「別の案も見たい」 | 要求形成の過程。最短化対象にしない |
| approval | 「これでいい」 | ユーザー選好に関する直接Evidence |
| delegation | 「任せる。そのまま進めて」 | Collaboration Policyに関する直接Evidence |
| change of mind | 「やっぱり最初の案に戻して」 | 恒久的選好へ直ちに一般化しない |
| question | 成果への評価を含まない質問 | 正負ラベルにしない |
| unknown | 対象・意図が特定できない | Raw Eventまたは未確定関係として保持 |

分類はSemantic Curatorによる`system_inference`であり、ユーザー発言そのものとは分離する。用途別Validatorは、分類名だけでProjectionを有効化せず、直接Evidenceと入力条件を検証する。

## 終了、無反応、遅延結果

会話の停止やsessionの終了だけでは、次を区別できない。

- 満足した
- 諦めた
- 後で確認する
- 別の仕事に移った
- 返信または実行を忘れた

そのため無反応は成功にも失敗にもせず、結果未観測として扱う。Work Episodeを閉じて正例化せず、観測窓の終了または`quiescent`として記録する。

明示的な「これでいい」はユーザー選好の直接Evidenceであるが、技術的正しさの証明ではない。逆にテスト成功は機能面のEvidenceであるが、ユーザーが望む設計や協働方法の証明ではない。

後日のEventは過去のOutcome Evidenceを補強または反証できる。

```text
explicit approval
  -> later CI passed
  -> PR merged
  -> later reverted
```

この場合、最初の承認Eventは削除せず、後日のrevertを追加して用途別適格性を再計算する。

## 良い反応を単一ラベルにしない

`good_response=true`や単一のreward scoreをCanonical Dataに保存しない。少なくとも次の観測軸を別々に保持する。

- User Preference: 明示的承認、明示的却下、自然言語による修正。
- Functional Verification: test、lint、build、grader、CI。
- Delivery: commit、review、merge、release。
- Collaboration: 確認要求、委任、質問過多への不満、無断実行への差し戻し。
- Durability: 後続修正、revert、障害、一定期間後の継続利用。

「ユーザーが喜んだ」「テストが通った」「後から直されなかった」を同じ意味にしない。Projectionごとに必要なEvidenceの組み合わせを定義する。

## 質問と確認の価値

質問回数やTrajectory長をそのまま損失にしない。質問には異なる役割がある。

| 質問種別 | 目的 |
|---|---|
| information | 不足している事実を得る |
| preference | ユーザーの選好を発見する |
| approval gate | 実行権限または重要な分岐の承認を得る |
| exploration | 出力を見ながら要求を共同形成する |
| redundant confirmation | 既知の情報または委任済み事項を再確認する |

質問の後にユーザーが回答したという事実だけでは、その質問が有益だったとも不要だったとも断定しない。

有益性の直接Evidenceになり得るもの:

- 回答によって実行方針、選択肢、scopeが変わった。
- 新しい制約または選好が明示された。
- ユーザーがその種類の操作は事前確認するよう求めた。
- 確認しなかった同種操作が差し戻された。

確認を減らす直接Evidenceになり得るもの:

- 「任せる」「最後まで進めて」「今後は確認不要」と明示された。
- 同じ既知情報を尋ねたことに対し「いちいち聞かないで」と指摘された。

単に毎回「はい」と返されたことは、確認を好むことも、仕方なく応答していることもあり得るため、単独では一般化しない。

## 最短Trajectoryを正解にしない

過去にC4へ到達するまで複数往復が必要だったとしても、学習後にC0からC4へ一度で到達することを常に改善とはみなさない。

- Agentの回避可能な誤りを直す往復だった可能性がある。
- ユーザーが中間成果を見ながら要求を発見した可能性がある。
- ユーザーが重要な分岐を一つずつ承認したかった可能性がある。
- C4に含まれる条件がC0時点には存在しなかった可能性がある。

C0からC4への直接SFTを許可するのは、C4の望ましい行動がC0時点の情報、またはその時点までに成立していた安定したScoped Judgmentから導ける場合に限る。後続発言で初めて追加された要求をC0の教師へ含めない。

## 事実、結果、仮説の分離

### Observed Fact

発言原文、Tool呼び出し、artifact snapshot・diff、test、Git、CI等。追記型で保存し、後の解釈で上書きしない。

### Outcome Evidence

明示的承認、却下、修正、検証、merge、revert等の観測Event。これ自体を総合成功スコアへ変換しない。

### Interpretation / Hypothesis

「このチームは新規抽象化より局所変更を好む」等の意味解釈。Semantic Curatorが提案できるが、根拠Event、反証Event、Scope、生成versionを持つannotationであり、正解ラベルではない。

同じパターンが別Work Episodeでも反復された場合は、複数Evidenceを持つ同一判断候補として統合できる。反復回数だけで一般化せず、context、task family、結果、反証例を保持する。数値confidenceを正本にせず、どの成立条件とEvidenceが揃ったかで用途別適格性を判断する。

## 学習Projection

### Refinement SFT

その時点のcontext、Agent出力、ユーザーの後続指示から、指示を反映した次の行動を学ぶ。追加要件も利用できるが、直前出力の負例とは扱わない。

### Direct SFT / Trajectory SFT

その時点で利用可能だった情報から、後に成立が確認された行動を教師にする。後から追加された要求、採用されなかった中間試行、無関係なTool操作を混ぜない。最終コードを自動的にtargetとせず、判断学習では必要なコードをstateのcontextとして残す。コード変更自体をtargetにする場合は、変更との直接的なEvidenceとverificationを要求する。

### Preference / DPO

同じstateと入力条件に対する比較で、ユーザーの選択、明示的な却下、検証結果等の直接Evidenceがある場合だけchosen/rejectedを構成する。contextが変わった修正版を同一条件のpairにしない。

### Judge / Outcome Model

候補行動またはTrajectoryについて、特定のOutcome Evidenceが後続する可能性を予測する。ユーザー満足、機能検証、協働、耐久性を一つのscoreへ早期統合せず、目的別に出力・検証する。

### Eval

同じ誤判断、不要な確認、望まれていない先走り、技術的失敗を再現し、期待条件を判定可能にする。

### Skill / Memory

明文化可能で安定した判断だけをSkill候補にし、即時想起すべき事実だけをMemory候補にする。状況依存の判断を無理に自然言語ルールへ圧縮しない。

## 学習への昇格

Raw Eventから直接モデル学習へ送らない。次の順序を維持する。

```text
Evidence Data
  -> Raw Event / code / diff / test
Abstract Experience Data
  -> Normalized Trajectory / Work Episode / Decision Point
  -> Outcome Evidence / Interpretation / Learning Case
  -> 用途別Evidence Validator
Training Projection Data
  -> Projection
  -> Dataset Version
  -> Training Run
```

AIが生成した理想行動、要約、判断理由は、実際の実行・選択・検証によるEvidenceがない限り教師データにしない。曖昧な事例はRaw EventまたはLearning Case候補として保持し、無理に正負へ分類しない。

## 低次元の個人適応仮説

本プロダクトは、個人の思考全体を少量データで再現できるとは仮定しない。検証するのは、作業ログに反復して現れる自律性、調査範囲、変更Scope、リスク許容度、品質基準、方針選択等が、少数の判断軸として低ランクAdapterへ圧縮できるかである。

比較する条件は以下とする。初期実験では1・2・5を同一基盤モデルで比較し、3・4との表現比較は後段で行う。

1. 未学習モデル。
2. 同じSkill・Contextを与えたモデル。
3. Evidenceを多く残したTrajectory学習モデル。
4. 自然言語の抽象化を中心に学習したモデル。
5. 必要なコードcontextと抽象化を併用したhybrid学習モデル。

学習に使っていないTaskとrepositoryで、判断傾向の再現、反復修正の減少、機能的正しさ、一般的Agent能力の回帰、Scope外への誤適用を測る。フロンティアモデルは外部参照として比較できるが、基盤能力の差を個人適応の効果として扱わない。

## 評価

モデル改善を単一の「会話回数」や「ユーザーの良い反応」で評価しない。少なくとも次を分離する。

### 仕事のしやすさ

- 同じ明示指示を繰り返させた回数。
- 回避可能な修正、不要な確認、望まれていない先走り。
- ユーザーが判断したい分岐で適切に制御を返せたか。

### ScopedなTask能力

- 初回成果の採用・修正量。
- test、CI、review、revert、後続障害。
- 未学習の同種Taskで望ましい判断を再現できたか。

### 一般的なAgent能力

- 未学習repository・task familyでもTool選択、調査、検証、回復が改善したか。
- Scopedな好みを無関係な環境へ誤適用していないか。

本プロダクトの主目的は、基盤モデルの一般知能を上げたと断定することではない。汎用Agentが経験を通じ、その人・チーム・repositoryで仕事をしやすく、かつ成果を出しやすくなることを目指す。

## 採用しない単純化

- session終了または無反応を成功とみなす。
- 最終artifact、commit、mergeを無条件に正解とみなす。
- 質問数やTrajectory長を一律に減らす。
- 後続の追加要件を、最初のAgent出力の誤りへ変換する。
- 明示的承認を技術的正しさと同一視する。
- test成功をユーザー選好の成立と同一視する。
- Semantic Curatorの解釈を教師ラベルへ直結する。
- 反復回数だけでScopeを個人・組織全体へ広げる。
- 状況依存判断をすべてSkillまたはMemoryへ圧縮する。
- コードを一律に学習対象から除外する。
- AIが生成した自然言語の抽象化だけを教師データにする。

## 検証の運用

抽出・再利用・追加学習・実用の仮説分離、標本監査、評価集合の分割、昇格の合否・判定不能、Scopeの実行方法は[仮説検証と段階的な実装](15-validation-plan.md)に従う。Scopeの記録だけで重みに学習した好みの適用範囲を保証せず、Adapter選択と対象外評価で検証する。
