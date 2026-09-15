# 参考資料

以下は、形式・実装・評価を検討するための参考先であり、採用や依存を確定するものではない。

## トレースと共通データ形式

- [OpenTelemetry GenAI Semantic Conventions](https://github.com/open-telemetry/semantic-conventions-genai): model呼び出し、tool実行、retrieval、memoryなどのspan・event・attribute命名を参考にする。プロダクト固有のLearning Case schemaは別に持つ。
- [ATIF / Harbor](https://github.com/harbor-framework/harbor/blob/main/rfcs/0001-trajectory-format.md): メッセージ、tool call、observation、subagent、multimodal contentを含む可搬なagent trajectory形式とvalidatorを参考にする。
- [Agent Data Protocol](https://github.com/neulab/agent-data-protocol) / [論文](https://arxiv.org/abs/2510.24702): 異なるagent datasetをActionとObservation中心へ正規化し、学習器へ接続する考え方を参考にする。

## 修正事例・ルール・Skills

- [Episodic](https://github.com/StageWhisperIO/episodic): coding-agentのhook収集、append-only event、CodingEpisode、GitHub結果との接続、SFT/DPO/RLDS export、replayを参考にする。mergeや評価値をそのまま正解にする設計は採用しない。
- [TellOnce / TRACE](https://github.com/YujunZhou/tellonce): ユーザー修正から実行時ルールを抽出し、次回の違反を防ぐ仕組みを参考にする。すべての暗黙知をルールへ変換する前提にはしない。
- [Microsoft SkillOpt](https://github.com/microsoft/SkillOpt): agent履歴からSkillを生成・更新し、replayで検証する流れを参考にする。Skillは学習先の一つであり、ファインチューニングの代替ではない。

## Memoryと個人化

- [CIPHER / PRELUDE](https://arxiv.org/abs/2404.15269) / [実装](https://github.com/gao-g/prelude): ユーザー編集から潜在的な好みを推論し、類似contextで検索・注入する流れと、編集量による評価を参考にする。推論した好みはannotationとして扱う。
- [Codex Memories実装](https://github.com/openai/codex/tree/main/codex-rs/memories): rollout単位の抽出、全体統合、検索向け `MEMORY.md`、短いsummary、Skillsへの整理を参考にする。公開実装上も外部メモリの整理とcontext注入であり、セッションごとの重み更新とは分けて考える。
- [Claude Code Memory](https://code.claude.com/docs/en/memory): 人間が書く `CLAUDE.md` と、Claudeが書くrepository単位のauto memory、常時ロードするindexと必要時に読むtopic fileの分離を参考にする。いずれもcontextとして渡され、強制設定ではない。
- [Mem0](https://github.com/mem0ai/mem0): 会話からのメモリ抽出、名前空間、検索APIを参考にする。
- [Letta / MemGPT](https://github.com/letta-ai/letta): 常時contextに置くcore memoryと、必要時に検索する外部memoryの分離を参考にする。
- [Graphiti](https://github.com/getzep/graphiti): episode由来の事実、時間、有効期間、矛盾・失効、provenanceの扱いを参考にする。

## 学習・配備基盤

- [Hugging Face TRL](https://github.com/huggingface/trl): SFT、DPO、reward model、GRPOなどの参照実装・trainer候補。
- [Axolotl](https://github.com/axolotl-ai-cloud/axolotl): オープンウェイトモデルのdataset設定、LoRA、分散学習を含む学習runner候補。
- [Unsloth](https://github.com/unslothai/unsloth): 対応環境での省メモリ・高速なfine-tuning候補。
- [Fireworks AI Fine-tuning](https://docs.fireworks.ai/fine-tuning/finetuning-intro): managed SFT/DPO/RFT、checkpoint、deploymentの外部backend候補。上流datasetとevidence modelは特定providerへ依存させない。

## Codex RuntimeとSemantic Curation

- [Codex Configuration Reference](https://learn.chatgpt.com/docs/config-file/config-reference): custom model provider、Responses API、model/profile設定を使い、Current ModelをCodexから実行する構成の根拠とする。
- [Codex App Server](https://learn.chatgpt.com/docs/app-server): Thread、Turn、Item、`turn/completed`、ChatGPT管理認証、rate limit取得を参考にする。
- [Codex Non-interactive mode](https://learn.chatgpt.com/docs/non-interactive-mode): `codex exec`、`--ephemeral`、構造化出力を使うバックグラウンド実行を参考にする。
- [GPT-5.6 Luna](https://developers.openai.com/api/docs/models/gpt-5.6-luna): 高頻度の構造化整形に使う必須Curator model。利用可否はCodex accountとmodel availabilityに従う。

## 評価

- [LongMemEval-V2](https://github.com/xiaowu0162/LongMemEval-V2): 長いagent trajectoryにおける状態変化、workflow、環境固有のgotcha、前提認識と、retrievalを含むend-to-end評価を参考にする。
- [Fine-tuning from User Edits](https://arxiv.org/abs/2601.19055): 同じユーザー編集をSFT、preference、rewardとして利用する際の定式化と注意点を参考にする。DPO projectionはsame contextに限定する。
