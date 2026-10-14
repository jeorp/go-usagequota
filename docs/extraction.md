# English Skillからの切り出し

## 出発点

English Skillのバックエンドには次の処理があります。

| 元の場所（バックエンド内） | 役割 | このライブラリで扱う部分 |
| --- | --- | --- |
| `internal/entitlement/period.go` | UTCの暦月と契約期間の決定 | 暦月の計算を `quota.Month` として切り出し、汎用の `Period` を用意 |
| `internal/usagequota/service.go` | 機能ごとの使用量・上限・リセット時刻の組み立て | 日次の期間計算を `quota.Day` に一般化。画面用の集約は対象外 |
| `internal/platform/firestoreenglishevent/draft_quota.go` | 日次カウンターをトランザクションで更新 | 上限判定と原子的な加算の考え方 |
| `internal/platform/firestoreroleplay/repository.go` | 文字数・利用回数の制限とデータ作成 | 数量付き利用枠と、読み取り・判定・更新を同じトランザクションに置く構造 |
| `internal/platform/firestoreenglishevent/course_quota.go` | 同じコースの重複計上を防ぐ | ID単位で二重消費を防ぐ考え方 |

**単純なファイル移動ではありません。** 既存処理の共通部分をもとに新しいAPIと保存形式へ再構成しています。Reserve/Commit/Cancelの状態機械、共通Storeインターフェース、メモリストア、ストア契約テストは今回追加したものです。

## 切り出していないもの

- Free/Casual/Platinumといった商品・契約のルール。
- user IDを認証情報から取り出す処理。
- 英語予定・コース・音声・レビュー固有のエラー。
- 画面に返す上限一覧や機能別の集計処理。
- Stripeの契約更新やCloud Tasksへの登録。

これらをライブラリに持ち込むと、別のアプリでも使える境界が失われます。呼び出し側が主体・機能・期間・要求量・上限を渡します。

## 既存アプリへの組み込み

English Skillには、まず次の2か所へ組み込みました。

1. `internal/entitlement/period.go` の `MonthPeriod` が `quota.Month` を呼びます。アプリの `Period.Key` は従来どおり `YYYY-MM` とし、請求期間の判定もアプリに残しています。
2. `internal/platform/firestoreenglishevent/draft_quota.go` が `quota.Day` と `quota.Consume` を呼びます。既存の `users/{uid}/customCourseDraftDays/{UTC日付}` を同じトランザクション内で読み、更新します。既存のcountとexpireAtを使用し、生成失敗でも戻さず、32日後のTTLを維持します。

`Consume` は今回の実導入に合わせて追加した純粋関数です。要求IDは導入せず、従来どおりAPIの試行ごとに消費します。予約APIの冪等性を、既存の日次制限へ追加したわけではありません。すでに使用した回数をそのまま読み込むため、この導入にはデータ移行が不要です。不正な負数カウンターは加算せずエラーにします。

タグ付きリリースの導入前は、アプリの `backend/third_party/go-usagequota` に、モジュールファイルとコアパッケージの実行用ソースを固定しています。`backend/scripts/sync_go_usagequota.py` で元のライブラリから同期し、`--check` で元との一致、`--verify` で保存済みハッシュとの一致を検査できます。アプリの `go.mod` はこのスナップショットを参照し、DockerビルドとCIも単独のチェックアウトで実行できます。モジュール名は `github.com/jeorp/go-usagequota` です。タグ付きリリース後はそのバージョン参照へ置き換える想定です。

## 予約APIの導入は別途必要

上記の導入は既存トランザクションを維持するものです。Reserve/Commit/Cancelおよび予約用Firestoreアダプターは、English Skillの業務処理にはまだ導入していません。そのアダプターは別のスキーマを使用し、既存のカウンターを直接読み込むことはできません。

特にロールプレイ作成は、利用量の更新とセッション作成を同じFirestoreトランザクションで行っています。これを `Reserve` → セッション作成という2回の独立した書き込みに置き換えると、途中停止時の挙動が変わります。本ライブラリのFirestoreアダプターは独自にトランザクションを開始するため、そのまま既存のトランザクションへ参加させることはできません。

導入時には、次のどちらかを機能ごとに判断します。

1. 予約とジョブ状態を照合して回復する仕組みを用意し、このAPIを使う。
2. 業務データとの一括コミットを優先し、既存トランザクションに組み込めるAPIを別途設計する。

既存データの移行では、現在の消費量とリクエストID、開始済みの処理をどう引き継ぐかを決める必要があります。既存利用者の枠を突然リセットしないため、次の利用期間から新方式を適用するなど、切り替え境界を定めます。

期間計算と試行回数の制限から実際に使用し、予約APIが必要な機能については回復・保持・移行の要件を確認してから広げます。
