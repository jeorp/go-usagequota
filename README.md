# go-usagequota

**Go向けの利用枠管理ライブラリ。** 同時実行と再送を考慮した予約・確定・返却と、既存カウンターに組み込める消費量の計算を提供します。

「AIレビューは月100回」「音声生成は月50,000文字」といった制限で、処理前に利用枠を予約し、成功時に確定、実行されなかった場合に返却できます。English SkillのGoバックエンドで使っている期間計算・利用量管理の考え方を切り出し、アプリ固有のプランやコレクション構成から独立させました。

現在は開発版です。English Skillでは期間計算と下書き生成の日次消費に導入しています。予約用のFirestoreアダプターは別の保存形式を使うため、既存アプリのDB構造と互換な置き換えではありません。

## すぐに試す

Go 1.24以上が必要です。デモとコアのテストにはクラウド資格情報は不要です。

```sh
go run ./examples/reservation
go test -race ./quota ./quota/memory
```

デモの出力:

```text
first request: created=true state=pending
same request: created=false state=pending
another request: limit exceeded
first request cancelled: capacity returned
usage: reserved=0 committed=1 remaining=0
```

## 使い方

```go
ctx := context.Background()
limiter := quota.New(memory.New())

key := quota.Key{
    Subject:  "user-123",
    Resource: "ai-reviews",
    Period:   quota.Month(time.Now()),
}

result, err := limiter.Reserve(ctx, key, "request-456", 1, 100)
if err != nil {
    return err // errors.Is(err, quota.ErrExceeded) で上限超過を判定
}
if !result.Created {
    // 新たに実行せず、アプリ側のジョブ・保存済み結果を確認する。
    return nil
}

// ここでアプリの処理を実行する。
// 成功を確認できた場合:
return limiter.Commit(ctx, key, "request-456")
```

インポート先は `github.com/jeorp/go-usagequota/quota` と `github.com/jeorp/go-usagequota/quota/memory` です。
実行されなかったことが確実なら `Cancel(ctx, key, requestID)` で利用枠を返します。通信タイムアウトで外部処理の結果が不明な場合は、自動的に返却せず、その処理の状態を照合します。

`Created` は利用枠を新しく予約できたことを示します。外部API呼び出しのexactly-once実行や、ジョブの永続化を保証するものではありません。

## パッケージ

| パッケージ | 役割 |
| --- | --- |
| `quota` | 期間計算、利用枠の予約・確定・返却、ストアの契約。標準ライブラリのみを使用 |
| `quota/memory` | 同じプロセス内で共有するメモリストア。テスト・デモ用 |
| `quota/firestorestore` | Firestoreトランザクションを使う永続ストア |

Firestoreの例:

```go
// client は呼び出し元で作成・Closeする *firestore.Client。
store := firestorestore.New(client, client.Collection("quotas"))
limiter := quota.New(store)
```

コレクションは渡したクライアントから作成してください。アダプターは予算ごとに1ドキュメント、リクエストごとに1ドキュメントを保存します。既存アプリとは別の専用コレクションを指定します。

## 既存のカウンターに組み込む

失敗しても消費を戻さない試行回数の制限には `quota.Consume` を使えます。

```go
// 既存トランザクション内で読み取った消費量を渡す。
next, err := quota.Consume(used, 1, 10)
if err != nil {
    return err
}
// 同じトランザクション内で next を保存する。
```

これは上限判定と消費後の値を計算する純粋関数です。保存・排他制御・冪等性は提供しません。読み取り、判定、保存を同じトランザクションで行ってください。既存の保存形式を変える必要はありません。

**実際の導入例：English Skill**

- 月次の利用期間を `quota.Month` で計算し、既存のDBキーを維持。
- カスタムコース下書きの日次制限に `quota.Day` と `quota.Consume` を使用。
- 既存のFirestoreカウンターを読み、その場で上限を確認して更新するため、データ移行や回数のリセットは不要。
- 生成失敗でも1回消費する仕様と、32日後のTTLを維持。
- 導入時点のコアのソースをアプリ内へ固定し、ハッシュで同一性を検査。Docker・CIは隣のリポジトリを必要としない。

導入側では、既存カウンターからの継続、残り1回への同時要求、UTC日付境界、生成失敗後の再要求をテストしています。[切り出しと導入の詳細](docs/extraction.md)を参照してください。

## 仕様

| 項目 | 挙動 |
| --- | --- |
| 利用枠の識別 | 利用者・機能・期間の開始/終了日時の組み合わせ |
| 冪等性の識別 | 利用枠のキー + リクエストID |
| 上限判定 | 予約中 + 確定済み + 新しい要求量が上限以下なら許可 |
| 同じID・同じ量の再送 | 既存の状態を返す。二重に加算しない |
| 同じID・異なる量の再送 | `ErrConflict` |
| 確定・返却の再送 | 同じ操作は成功。確定後の返却や返却後の確定は `ErrFinalized` |
| 返却済みIDの再予約 | `ErrFinalized`。新しい仕事には新しいIDを使う |
| 期間 | 開始を含み、終了を含まない。新しい予約の時刻をトランザクションの試行ごとに検査 |
| 期間終了後 | 新しい予約は拒否。既存予約の再送・確定・返却は元の期間に適用 |
| 上限0 / 負数 | 0は新しい利用を禁止。負数は不正値。無制限の特殊値は設けない |
| 上限の変更 | 予約ごとに信頼できる設定値を渡す。減額しても既存予約は取り消さない |

UTCの暦月は `quota.Month(now)`、利用者の現地日付は `quota.Day(now.In(location))` で作成します。日次の区切りは24時間固定ではなく、夏時間の切り替えを考慮します。請求期間は `quota.Period{Start: start, End: end}` で指定します。

## 検証

```sh
make test       # 全パッケージのテスト + race検出
make vet        # 静的検査
make fuzz       # 状態遷移を参照モデルと照合するファズテスト
make demo

# Dockerを使うFirestore結合テスト。アプリのエミュレータとは別の8086番ポート。
make emulator-up
make integration
make emulator-down
```

ポートが使用中の場合は、起動とテストの両方で `QUOTA_EMULATOR_PORT=18086` などを指定します。

メモリとFirestoreに共通のテストで、残り1回への100件同時要求、同一IDの100件同時再送、確定と返却の競合、期間境界、最大整数付近、トランザクションのロールバックを確認します。Firestoreの競合でAbortedとなった要求は、未コミットを確認できるため同時要求後に1回再試行し、上限と冪等性を検査します。別クライアント間の状態共有も検証します。結合テストはローカルのエミュレータを必須とし、指定がなければ失敗します。

GitHub Actionsに通常テストとFirestore結合テストを用意しています。

## 設計と適用範囲

- [設計判断・保証範囲・障害時の扱い](docs/design.md)
- [English Skillから切り出した部分と導入時の注意点](docs/extraction.md)

メモリストアは再起動で状態を失い、自動削除はしません。Firestoreストアも自動期限切れ・自動返却・削除処理を提供しません。予約の期限切れによる二重利用を避けるため、未確定予約の回復はアプリのジョブ状態と照合して行います。

## 別プロジェクトから使う

```sh
go get github.com/jeorp/go-usagequota@latest
```

```go
import (
    "github.com/jeorp/go-usagequota/quota"
    "github.com/jeorp/go-usagequota/quota/memory"
)
```

現時点ではタグ付きリリース前の開発版です。Goが解決したバージョンを利用側の`go.mod`に固定してください。ローカルでライブラリも編集する場合は、利用側の`go.mod`に次を追加できます。

```go
replace github.com/jeorp/go-usagequota => /path/to/go-usagequota
```
