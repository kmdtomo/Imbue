# 未確定事項

以下は、現在の要件から一意に決められず、実装または検証前に判断が必要な事項だけを挙げる。

## 初期スタックの確定事項と残りの選定

- 確定: 一人用ローカル実行、Go＋Cobra、PostgreSQL＋pgx＋sqlc、ローカルファイル保存、PostgreSQLジョブテーブル＋Go Worker。
- 後段: クラウドホスティング、Object Storage、外部Queue、サービス認証・課金。初期実装を待たせる選定事項ではない。
- 残り: GoのHTTPルーター、DB migrationツール、対応Codex versionと起動方式、GatewayのResponses API互換性、Fireworksでの対象モデル・学習・推論の実機確認。

## 取得と境界

1. Codex、Claude Codeなど各agentから取得できるeventの差を、共通collectorでどこまで吸収し、どこからprovider固有adapterとして残すか。
2. 通常ターンはユーザー発言とAgentの制御返却を観測境界にする。サブエージェント、並行編集、複数Sessionを含む場合に、どのTrajectory、Work Episode、Decision Pointへ結合するか。
3. 一つの自然言語フィードバックが複数のコード変更へ影響した場合、before / afterとOutcome Evidenceをどう対応付けるか。
4. 同じSession内の別Taskと、後日再開された同じTaskを、目的・artifact・参照関係からどの精度で分割・結合できるか。

## 学習事例の成立条件

5. 明示的承認、テスト、CI、レビュー、merge、revert、後続修正の組み合わせを、Task種別とProjectionごとにどのような成立条件として扱うか。
6. correction、preference refinement、requirement addition、exploration、approval、delegation、change of mindを、どのEvidence条件で区別するか。
7. 質問による方針変更・新制約・権限確認と、冗長な確認を、単純な質問回数やユーザー応答以外の何から判定するか。
8. 初期は所有者・repository・task familyごとの単一Scope Adapterとし、不明・競合・対象外は基盤モデルへ戻す。複数Scope統合時の表現、競合解決、path等の粒度は後段で決定する。
9. 無反応のWork Episodeを結果未観測のままどの期間保持し、後続Eventとの関連候補をいつまで探索するか。

## 学習と評価

10. Qwen3.8-27BのLoRAを前提に、用途、データ量、予算からSFT、DPO、RFT、Judge、sampling、hyperparameterをどう決定するか。
11. active dataset全体を使いつつ、長期の有効事例を忘れないsamplingとcurriculumをどう構成するか。
12. User Preference、Functional Verification、Collaboration、Delivery、Durabilityを単一rewardへ潰さず、Judgeと学習方式をどう分離するか。
13. C0から過去の最終artifactへ直接到達するProjectionについて、hindsight leakageと望まれた確認・探索をどう自動検証するか。
14. model artifactの生成、読込、形式互換性、秘密情報混入など、Current Model切替前の技術的検証をどこまで行うか。
15. RFTへ回せる安全なsandboxとverifierを、repositoryごとにどう定義・実行するか。
16. 個人差が少数の判断軸と低ランクAdapterへ圧縮できるという仮説を、どの未学習Task、repository、データ量で棄却または支持するか。
17. 生のTrajectory中心、自然言語の抽象化中心、必要なコードcontextと抽象化を併用するhybridを、同一基盤モデルでどう比較するか。
18. 判断学習に必要なコードcontext、コードtokenのloss mask、コード変更を教師targetにできるEvidence条件をどう決めるか。

## 保存・所有・安全性

19. Local bufferとPlatform Ledgerの再送、重複排除、競合、保持期間、削除伝播をどう保証するか。
20. 個人情報・secretの検出、redaction、保持期間、削除伝播の実装詳細をどう定めるか。学習済みmodelの影響追跡、隔離、汚染のない来歴からの再学習、データ除外と配備反映の状態分離は確定要件とする。
21. Web、issue、tool outputなどのuntrusted contentがMemoryやSkillへ昇格するのを防ぐ、書き込み時・利用時の具体的な安全条件をどう定めるか。

## プロダクト体験

22. CLIを正本としたうえで、evidence、annotation、dataset diff、model versionの編集・監査にTUIや任意UIをどこまで追加するか。
23. UIの暗黙的判断を扱うため、スクリーンショット、DOM、viewport、レンダリング環境のどこまでをevidenceとして保存するか。
24. ユーザーへ追加ラベル付けを要求せず、曖昧で影響の大きいCollaboration Policyだけを低負担で確認するUXを設けるか。

## PlatformとProvider

25. Fireworks On-demand Deploymentのcold start、再試行上限、Scale-to-zeroまでの時間をどう設定するか。
26. Fireworks以外のProviderへ切り替えた場合に、model artifactの可搬性と同一Training Runの再現性をどこまで保証するか。
27. ChatGPT管理認証下のCodexでLunaを必須実行するため、対応する最低Codex version、利用可能plan、capability検出とrate limit判定をどの組み合わせで保証するか。

## 実験前に具体化する値

[検証計画](15-validation-plan.md)で四段階の仮説、初期範囲、監査、分割、昇格規則は確定した。以下の値はpilotで具体化し、昇格用Experiment Planの実行前に固定する。

- 監査標本数と抽出法、許容誤抽出率、必要な有効事例の収集量。
- 判断領域ごとの評価課題数、反復数、主要指標、最小改善幅、能力回帰・Scope違反の許容条件、不確実性の算出法。
- Context条件の検索・提示予算、学習・推論の費用上限、実用検証の期間と継続利用判断。

これらの未設定を自動的な合格やモデル昇格で補わない。
