# Lunaの日本語指示とraw入力

2026-09-18: policy `imbue-curator-4`、出力 `learning-case-patch-2`。

## 実行されるプロンプト

正本は [`internal/curator/instructions_ja.md`](../internal/curator/instructions_ja.md)。Goのembedで組み込み、Lunaへの指示として直接使う。編集後はビルド・Daemon再起動が必要。

抽出対象は要求の要約ではなく、時系列の行動と修正から読み取れる状況依存の判断。観測と仮説を分け、未来の情報を過去へ戻さず、根拠不足・別解釈・一般化の限界を保持する。単なる要求や事実しか得られない入力ではCaseを生成しなくてよい。

## 入力形式

Lunaに送る形式は `imbue-raw-timeline-2`。保存されたEvidence Packetを決定的に変換する。秘密情報のマスク・除外は従来どおり適用する。画像コンテンツ、画像URL・スクリーンショット、テキスト中のdata:image URLは画像除外マーカーへ置換し、元のEvidenceは変更しない。残る本文は要約しない。rawは文字列化したJSONではなくJSON値。会話・ターン・元位置を保持し、異なる会話の隣接を因果関係とみなさない。

```json
{
  "format": "imbue-raw-timeline-2",
  "job_id": "example",
  "repository": "/example",
  "policy_version": "imbue-curator-4",
  "schema_version": "learning-case-patch-2",
  "coverage": {
    "part": 1,
    "parts": 1,
    "from_seq": 0,
    "through_seq": 42,
    "excluded_kinds": ["tool.command", "image.content"],
    "limitations": ["コマンドの実行記録は入力対象外。Agentの報告だけで検証済みとしない。"]
  },
  "timeline": [
    {
      "order": 1,
      "origin": "selected",
      "evidence_id": "E000001",
      "conversation_id": "conversation-a",
      "turn_id": "turn-2",
      "occurred_at": "2026-09-18T01:00:00Z",
      "source_offset": 1200,
      "kind": "user.message",
      "raw": {
        "type": "UserMessage",
        "content": [{"type": "text", "text": "項目は残して、見た目だけシンプルに"}]
      }
    }
  ],
  "existing_cases": [],
  "allowed_evidence_ids": ["E000001"]
}
```

これは形式説明用の架空の一イベントであり、単独で判断経験が成立する例ではない。

`origin=context`は再掲した補助文脈。`order`は時刻が揃う場合の入力内時系列順であり、元ログの絶対sequenceではない。時刻欠損時は入力の列挙順を維持し、coverageで明示する。長いrawの断片は元のIDとfragment_index / fragment_countを保ち、全体を見たと誤認させない。

## 出力

従来のjudgment・conditions・exceptionsに加え、experienceを必須化する。

- situation: 判断時点の目的、情報、制約。
- observations: 観測された経緯。factとevent_idsを持つ。
- hypothesis: 根拠がある場合だけ残す、より広い判断の暫定仮説。空文字を許容し、Case成立の条件にしない。
- alternative_interpretations: 別の説明や反証。
- unknowns: 根拠欠損、結果未観測、適用限界。

観測の参照は許可されたEvidence IDかつCaseの根拠集合内であることを検証する。短縮IDは入れ子の観測を含めて元IDへ復元する。旧Caseはそのまま読める。新しい生成では要約のみのCaseを受理しない。TUIの詳細にも経験の各欄を表示する。

## 今回の境界

コマンド全文と出力の除外を維持する。画像本体もLuna・学習入力から除外し、画像があった事実と未確認であること、元データのハッシュ・サイズを残す。原本の画像データはEvidenceに保持する。画像の内容を学習用の観測事実として推測させない。

分割は会話ごとのターンを優先する。予算に収まるターンを途中で切らず、境界には直前の依頼・最終応答と次ターンのユーザー発言をcontextとして添える。次の発言を以前の行動時点で利用可能だった情報にはしない。小さい会話のまとまりはIDによる区別を保って同じ入力に詰める。一ターン自体が予算を超える場合に限り、原文を無欠損の断片へ分割する。

過去の処理済みターンは会話ごとに直近2件を取得し、selectedと区別する。補助文脈では発言を再掲し、過去のツール結果は繰り返さない。巨大な補助発言は本文未提示の参照として明示する。独立した仕事の意味的な関連判定や永続Work Episodeは追加していない。

予算は短縮IDを適用した実際のモデル入力JSONのバイト数で検査し、送信直前にも検証する。監査にはinput_formatとinput_bytesを記録する。意味の異なる判断を一つへまとめる自動統合は行わず、結果の完全一致重複排除は従来どおり。境界に再掲した文脈のみから同じCaseを再作成しないよう指示する。

## オフライン比較

画像を多く含む保存済みJob `18036483ab3a3bae89795b91031430d9` の44分割を復元して再分割した。旧・新の両方を同じ入力JSON形式で計測し、約6,893,499 bytes / 44 parts → 354,808 bytes / 3 parts（約94.85%削減）。過去の入力範囲を復元した比較であり、最新のDB境界選択を含む全件ベンチマークではない。実トークン数やLunaの抽出品質は別途確認する。Luna推論はこの比較では実行していない。

任意のローカルfixtureに対する再現コマンド：

```sh
IMBUE_INPUT_REPLAY=/absolute/path/to/fixture.json \
  go test -v ./internal/curator -run TestOfflineInputReplay -count=1
```

fixtureはpacketとold_partsを持つ。個人のrawログをリポジトリへ追加しない。

構造・根拠参照のテスト通過は、抽出の意味的品質を保証しない。旧出力と新出力を同じ一連の実ログで比較し、本人の意図との一致、過度の一般化、見逃しを別途確認する。

推測の境界を日本語プロンプトに明記した。発言と行動の対応関係の解釈は行うが、記録にない動機・恒久的な好み・理想行動を作らない。状況・行動・修正の経験が成立すれば、広い判断仮説なしでも保存できる。
