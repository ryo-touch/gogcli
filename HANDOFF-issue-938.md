# 申し送り: issue #938 Connected Sheets write path

> **このブランチは upstream へ PR しない。** ryo-touch/gogcli (fork) 上の作業引き継ぎ専用。
> 最終更新: 2026-08-25

別マシンの Claude Code がこの作業を引き継ぐための状態記録。

## 何をやっているか

[openclaw/gogcli#938](https://github.com/openclaw/gogcli/issues/938) の Connected Sheets
write path。read path は 0.37 で land 済み ([#989](https://github.com/openclaw/gogcli/pull/989))、
live 検証で見つけた 2 件の defect も修正済み ([#1001](https://github.com/openclaw/gogcli/pull/1001))。

2026-08-25 に maintainer (steipete) から write path の GO サインが出た
([該当コメント](https://github.com/openclaw/gogcli/issues/938#issuecomment-5408626756))。条件:

- **1 本にまとめず、狭くスコープした PR に分ける。** `refresh` を先行させ、`add` / `update` / `delete` を個別に提案する
- 既存 read-only 挙動の保持 / 追加認可の opt-in 維持 / 再認可時の既存 services 保持 / mutation には適切な Sheets write 権限
- 各 PR に redacted な live 証跡（捨てスプレッドシート + scratch dataset）、provider 失敗・status 挙動、dry-run/safety、cleanup
- `delete` は明示的確認を必須にする
- 別立てで `cloud-platform` superset ケースの再現可能な redacted scope 一覧 + Connected Sheets 成功出力

## 現在の状態

### 実装は完了済み・soak 済み

`fork/feat/connected-sheets-write` (2 commits, +1687 行) に **5 verb すべて実装済み**:
`refresh` / `cancel-refresh` / `add` / `update` / `delete`。fixture テスト 12 本付き。docs も書けている。

このブランチからビルドしたバイナリが `~/.local/bin/overrides/gog` として 2026-08-24 から
実務投入されている (soak)。**別マシンにはこの shim は無い**ので、`gog --version` で確認すること。

したがって残作業は新規実装ではなく **分割 + live 証跡 + 再現ハーネス**。

### 出した PR

| PR | 内容 | 状態 |
|---|---|---|
| [#1023](https://github.com/openclaw/gogcli/pull/1023) | `docs(sheets)`: BigQuery 認可の記述を正確にする | open。self-review 済み、対応推奨 1 件を修正して push 済み (2 commits) |

`#1023` のブランチ: `fork/docs/connected-sheets-scope-accuracy`

self-review で潰した指摘 (commit `7b2eb21e`): 「Google は既存 grant に対して認可する」という
書き方が **DWD (service account) 経路では偽**で、直前の段落と矛盾していた。stored user OAuth に
限定し、delegation ケースを対比として明記した。主語も "gog" から "the Connected Sheets client" に
狭めた (他のクライアントは stored grant を照合するため。例: `NewGmailBatchDelete` は
`requireStoredGrant=true`)。

未対応で残した指摘 (いずれも「対応任意」以下):

- 「保存済みトークンが BigQuery read access を持っているか」の判定手段が docs に無い。
  ローカル照合が無いので 403 を踏むまで分からない。`gog auth list --json` が stored scopes を出す
  (`internal/cmd/auth_list_helpers.go:37`) ので案内先としては妥当。docs-only の最小差分方針で見送り
- `:12` の "least-privilege scope" が無出典の最上級主張。steipete 自身が
  "least-privilege defaults" という語を使っているので共有語彙として許容と判断
- `internal/cmd/sheets_datasource.go:596` のエラー文言が訂正前の無条件「require OAuth scope X」形を
  維持している。実際の 403 後にのみ発火するので案内内容は正しいが、レビューで問われる可能性がある

### まだ出していない PR (計画)

**逐次で出す。stacked にしない。** PR 1 が共有 write 基盤を含むため、基盤にレビュー指摘が入ると
下流の stacked PR 全部が rebase になる。steipete の "then propose add, update, and delete
independently" も逐次を示唆している。**PR N は N-1 が land してから出す。**

1. **`refresh` + 共有 write 基盤** — 基盤だけの先行 PR は未使用コードの追加になりレビューで嫌われるので同梱する
2. **`cancel-refresh`** — steipete が列挙した 4 verb 外なので独立させる。存在は PR 1 の本文で予告する
3. **`add`** → 4. **`update`** → 5. **`delete`**（この順に実装依存がある。下記参照）

## 分割の材料

### 共有基盤 (全 verb が依存 → PR 1 に同梱)

- `internal/googleapi/sheets.go` — `NewConnectedSheetsWriter` (`spreadsheets` + `bigquery.readonly`、Drive は要求しない)
- `internal/cmd/service_helpers.go` — `requireConnectedSheetsWriterService`
- `internal/cmd/sheets_datasource_write.go` — `connectedSheetsMutation` / `runConnectedSheetsMutation`
  (`dryRunExit` と `dryRunAndConfirmDestructive` を出し分け、`internal/cmd/dryrun_contract_test.go` の
  AST 契約を満たす)、`wrapConnectedSheetsWriteError`
- `internal/cmd/sheets_datasource.go` — `isInsufficientScopeError` への抽出 (read/write 共有)、`connectedSheetsWriteScope`
- 登録系 — `internal/app/runtime.go`、`internal/cmd/runtime_services.go`、`internal/googleapi/factory.go`

横断テスト (PR 1 に置き以降で拡張): `TestSheetsDataSourceWriteDryRunIssuesNoRequests`、
`TestSheetsDataSourceWritePrefersWriterService`、`TestWrapConnectedSheetsWriteError`

### verb 固有

| verb | 主な関数 | テスト |
|---|---|---|
| `refresh` | `SheetsDataSourceRefreshCmd`、`validateSheetsDataSourceTarget`、`collectSheetsDataSourceRefreshStatuses`、`describeSheetsDataSourceRefresh`、`formatSheetsDataSourceObjectReference`、`formatSheetsGridCoordinate` | `...RefreshByID`、`...RefreshAllIgnoringState`、`...RefreshTargetValidation` |
| `cancel-refresh` | `SheetsDataSourceCancelRefreshCmd`、`collectSheetsDataSourceCancelStatuses`、`describeSheetsDataSourceCancel` | `...CancelRefresh` |
| `add` | `SheetsDataSourceAddCmd`、`bigQuerySpecInput`、`buildBigQueryDataSourceSpec`、`resolveSheetsDataSourceQuery`、`addedDataSource`、`sheetsDataSourceMutationPayload` | `...AddQuerySource`、`...AddTableSourceDefaultsTableProject` |
| `update` | `SheetsDataSourceUpdateCmd`、`buildBigQueryDataSourceUpdate`、`buildBigQueryTableSpecUpdate`、`updatedDataSource` | `...UpdateFieldMaskFollowsTypedFlags` |
| `delete` | `SheetsDataSourceDeleteCmd` | `...DeleteRequiresConfirmation` |

`add` が spec builder 系を持ち込み、`update` が field-mask 系を足すので **add → update** の順に依存がある
(steipete の指定順と一致)。

### 未実装で追加が必要なもの

**live 検証ハーネス。** `scripts/live-tests/` に Connected Sheets モジュールが無い。
steipete が当初の deferral で求めた "repeatable live Connected Sheets validation" の実体はここ。
`scripts/live-tests/classroom.sh:14-38` の optional module idiom (env 未設定なら skip、`STRICT` なら fail) に従い、
`common.sh` の `run_optional` / `register_drive_cleanup` / `$TS` / `$LIVE_TMP` を再利用する。

## 確定した判断とその理由

### superset の分離実証は取れない → docs は証明可能な範囲に絞った

steipete は `cloud-platform` 単独で Connected Sheets が通ることの証拠を求めたが、**取得不能**と判断した。

- Google は付与済み scope を**累積**する。現アカウントのトークンは `cloud-platform` と
  `bigquery.readonly` の**両方**を持っているため、そこからの scope 一覧は superset の証明にならない
- `bigquery.readonly` を持たないトークンを作るにはアプリ認可を revoke して付け直すしかなく、
  22 サービス分の scope が乗った実務トークンでそれをやる価値はない
- **専用 Google アカウントは用意できない**（ユーザー明言）

代わりに、コードから証明できる事実を docs に書いた: Connected Sheets のクライアントは
**ローカルの scope 照合をしない**。`NewConnectedSheets` / `NewConnectedSheetsWriter` はどちらも
`optionsForAccountScopes` (= `requireStoredGrant=false`) を通るため、
`internal/googleapi/client_auth.go:437` の `scopesContainAll` ゲートがバイパスされ、
認可判定は Google 側に委ねられる。`InsufficientScopeError` はそのスキップされる分岐でしか構築されない。

**`bigquery.readonly` は least-privilege デフォルトとして維持し、superset は列挙しない。**

これに伴い、8/17 のコメントで述べた「`cloud-platform` トークンで再認可なしに動いた」という主張の
superset 部分は未検証として撤回した (PR #1023 本文に記載)。read path の指摘自体は #1001 で
再現・修正済みなので影響しない。

### 「throwaway account」ではなく「throwaway spreadsheet + scratch dataset」

steipete が要求しているのは捨て**スプレッドシート**と scratch **dataset** で、捨て**アカウント**ではない。
これらは既存アカウント・既存 GCP プロジェクト内に作れる。**write path の PR は superset の件では
ブロックされていない。**

## 環境・落とし穴

- **証跡は `v0.37.0` タグでは撮れない。** v0.37.0 には #1001 で直したバグが入っており、
  `datasource table read` が SYNC_ALL 抽出で全滅する。証跡は #1001 を含む main の
  **クリーンビルド** (`-dirty` なし) から撮る。ローカル main での `make build` → `./bin/gog`
- `make docs-commands` / `docs-check` は `GOG_GMAIL_NO_SEND` と `GOG_DISABLE_COMMANDS` を拾って
  全ページを差分化する。`env -u GOG_GMAIL_NO_SEND -u GOG_DISABLE_COMMANDS make docs-check` で実行する
- `gog` は soak shim に差し替わっている環境がある。`gog --version` で確認する
- **PR 本文に GitHub の comment ID / URL を書くときは `gh api` で実在確認してから書く。**
  PR #1023 で捏造して投稿後に修正した
- 既存 services の保持は `persistingTokenSource` が `mergeStringSet` で scope と services を
  **マージ**することで担保されている (`internal/googleapi/client_auth.go:274-288`)。
  steipete の "preserve existing account services when reauthorizing" 要件の根拠として PR 1 で使える
- **認可経路は 2 つあり挙動が逆。** service account 経路が stored OAuth より**先**に試される
  (`client_auth.go:374`)。SA 経路は `google.JWTConfigFromJSON(keyJSON, scopes...)` で literal scope を
  署名付き JWT に assert するため (`internal/googleapi/service_account.go:25`)、DWD では
  Google が literal scope 文字列に対して認可し管理者の事前承認が必要。stored OAuth 経路だけが
  「既存 grant に委ねる」挙動になる。`NewConnectedSheetsWriter` も同じ経路構造なので、
  **PR 1 の docs で write scope を説明するときも同じ区別が必要**
  (DWD 利用者は `spreadsheets` + `bigquery.readonly` の管理者承認が要る)

## live 証跡の要件

### ブートストラップ上の制約

`refresh` の検証には既存 data source が必要だが `add` は PR 3 まで land しない。したがって:

- **PR 1**: Sheets UI で手作りした捨てスプレッドシートを対象にする。ハーネスも
  `GOG_LIVE_CONNECTED_SHEET=<spreadsheetId>` のような既存リソース指定で gate する
  (`GOG_LIVE_CLASSROOM_COURSE` と同じ形)
- **PR 3 以降**: `add` が入ればハーネスが自前でスプレッドシートを作れる。gate を project/dataset 指定に
  切り替え、`register_drive_cleanup` で後片付けする

### 各 PR で撮るもの

- 成功パス (`--json`)
- provider 失敗 / status 挙動: 存在しないテーブルや壊れた SQL を指す source で error state を作り、
  `refresh` が即失敗すること → `--ignore-state` で再試行できることを示す
- `--all` で `statuses` が空 = 失敗なし の挙動
- `--dry-run` が API を叩かないこと、`gog --readonly` が Google 到達前にブロックすること
- cleanup (スプレッドシート削除、dataset 後片付け)
- `delete` PR では非対話 + `--force` なしが prompt せず拒否すること

redact 対象: アカウントのメールアドレス、spreadsheetId、dataSourceId、GCP project / dataset 名。

## 次のアクションと、そこで詰まっている点

次は捨てスプレッドシート + scratch dataset の準備。**ユーザーの回答待ちの項目が 2 件ある**:

1. scratch dataset を置く GCP プロジェクトはどれを使うか
2. Connected Sheets の作成 (スプレッドシート作成 → BigQuery 接続) は UI 操作なのでユーザー実行が必要。
   それで進めるか、`add` を先に land させて自動生成できる順序へ変えるか

2 について: steipete の指定順は `refresh` 先行だが、`refresh` の live proof には既存 data source が
必要なので、どこかで一度は手作りが必要になる。

## 検証コマンド

```bash
# 各 PR で push 前に
make build
go test ./internal/cmd/ -run 'DataSource|DryRun' -count=1
go test ./internal/googleapi/ -count=1
env -u GOG_GMAIL_NO_SEND -u GOG_DISABLE_COMMANDS make docs-check

# live
GOG_LIVE_CONNECTED_SHEET=<throwaway id> scripts/live-test.sh --account <test account>
```

PR 作成直後に self-review skill をフルで実行し、「対応推奨」を潰してからレビュー依頼する。

## ブランチ一覧 (fork)

| ブランチ | 内容 |
|---|---|
| `feat/connected-sheets-write` | write path 実装全部 (分割元。**このまま PR にしない**) |
| `fix/connected-sheets-read-live-findings` | #1001 の作業ブランチ (land 済み) |
| `docs/connected-sheets-scope-accuracy` | PR #1023 |
| `handoff/issue-938-connected-sheets` | このファイル |
