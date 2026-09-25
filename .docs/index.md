# 文書目次

## 再設計案

- [原文の経験を中心にした学習データの再設計案](18-trajectory-first-redesign.md) — 生成した判断文から、原文のやり取りと学習対象の明示へ。未実装の提案であり、下記の現行仕様とは区別する。

## 読む順序

1. [プロダクト定義](01-product-definition.md) — 課題、価値、原則、対象範囲
2. [ユーザー体験](02-user-experience.md) — 導入から継続学習、任意編集、rollbackまで
3. [システムアーキテクチャ](03-system-architecture.md) — Event駆動の全体構成とManaged/Observe Mode
4. [データモデル](04-data-model.md) — Evidence、Abstract Experience、Training Projectionの三層と各schema
5. [学習とトレーニング](05-learning-and-training.md) — SFT、DPO、RFT、継続学習、version管理
6. [セキュリティとガバナンス](06-security-and-governance.md) — 所有、Scope、PII、削除、共有
7. [実装要件](07-implementation-requirements.md) — component、contract、状態機械、障害処理、受入条件
8. [Experience Learning Model](14-experience-learning-model.md) — 終端のない仕事、質問・確認、自然言語修正、Outcome Evidence、学習Projection
9. [会話からモデル学習まで](10-conversation-to-model.md) — ターンごとの収集とバッチ整形、具体例、DatasetとModelの増え方
10. [Semantic Curator（Luna）](11-semantic-curator.md) — 非表示の整形Worker、ステートレスRun、入出力と並行実行
11. [技術アーキテクチャ](12-technical-architecture.md) — Go CLI/Collector、Imbue Platform、学習・推論Providerの境界

12. [仮説検証と段階的な実装](15-validation-plan.md) — 四段階の仮説、初期範囲、監査、評価・昇格、Scope、訂正後の再学習

## 補助資料

- [ローカル実装と動作確認](16-local-implementation.md) — 導入、対応Codex版、収集境界、Luna整形、確認コマンド
- [参考資料](08-references.md) — 標準、OSS、論文、外部基盤
- [未確定事項](09-open-questions.md) — 今後決める必要がある実装判断
- [OSS調査に基づく設計監査・提案](13-oss-architecture-review.md) — 現行要件の維持点、変更候補、非採用案、採用前検証

## 文書上の前提

- 初期は一人用のローカル構成とし、PlatformもPC上で実行する。PostgreSQL（pgx＋sqlc）、ローカル正本ファイル、DBジョブテーブルを使う。クラウド配備・サービス認証・課金は後段とし、AI実行・学習には引き続き外部サービスを使う。

- SessionではなくWork EpisodeとDecision Pointを経験の単位とし、Session終了・無反応・最終artifactを成功の代理変数にしない。
- データセットを正本とし、実ファインチューニングを主要な利用経路とする。
- コードを一律に除外せずEvidenceとして保持し、自然言語の抽象化、用途別Training Projectionと三層に分ける。
- 少量・低ランク学習による個人適応は前提ではなく、同一基盤モデルと未学習Taskで検証する仮説とする。
- 通常運用に必須の承認フローや恣意的なconfidence/strength scoreを設けない。実験用の標本監査と条件別比較結果の記録は行い、Candidateの昇格には必須Evalを要求する。
- Context、Memory、Skillへの即時反映は任意Projectionとする。
- 質問回数やTrajectory長を一律に減らさず、Task、Tool、Collaboration Policyを分離して学習する。
- 明示的承認、技術的検証、協働、Delivery、耐久性のOutcome Evidenceを単一の成功ラベルへ圧縮しない。
- 内部CoTではなく、観測事実と結果を見る前のActionIntentを記録する。
- Base Model、checkpoint、LoRAはローカルへ保存せず、Imbue Platform経由でクラウド管理する。
- 初期構成はQwen3.8-27BをFireworks Managed TrainingでLoRA学習し、On-demand Deploymentで実行する。学習は`imbue train`の明示実行時だけ開始し、Candidateは個人適応と一般能力回帰を確認してからCurrent Modelへ昇格する。
- Codexを標準かつ必須のAgent Runtimeとし、Imbueは独自Harnessを実装しない。
- Semantic CuratorはユーザーのChatGPT管理認証下にあるCodexで、GPT-5.6 LunaをJobごとにステートレス実行する。
