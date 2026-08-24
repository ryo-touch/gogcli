---
title: Connected Sheets
description: Inspect and manage BigQuery and Looker data sources, execution status, and anchored Connected Sheets extracts.
---

# Connected Sheets

`gog sheets datasource` works with external data sources in a spreadsheet. It can list sources, return the complete source specification and execution status, discover anchored data-source tables (called extracts in the Sheets editor), read a bounded number of extract rows, and manage the BigQuery data source lifecycle: add, update, refresh, cancel a refresh, and delete.

The inspection commands never mutate the spreadsheet. The lifecycle commands do, and they honor `--dry-run` and `--readonly` like every other mutating `gog` command.

## Authorize BigQuery access explicitly

Google requires `https://www.googleapis.com/auth/bigquery.readonly` whenever a Sheets API response contains BigQuery Connected Sheets data. Ordinary `sheets` authorization intentionally does not request that scope.

Re-authorize the account with its existing service selection, append the scope, and force the consent screen. For a Sheets-only token:

```bash
gog auth add you@example.com \
  --services sheets \
  --extra-scopes https://www.googleapis.com/auth/bigquery.readonly \
  --force-consent
```

If the account token covers more services, keep that existing `--services` selection instead of narrowing it to `sheets`. Domain-wide delegated service accounts must also have both the Sheets read-only and BigQuery read-only scopes approved by the Workspace administrator; the read-only Connected Sheets client requests only those two scopes.

Re-authorization is unnecessary when the stored token already carries a superset. A token holding `https://www.googleapis.com/auth/cloud-platform` and `https://www.googleapis.com/auth/spreadsheets`, for example, covers both the BigQuery read and Sheets scopes without another consent round.

The lifecycle commands need spreadsheets write access rather than read-only, so their client requests `https://www.googleapis.com/auth/spreadsheets` alongside the same BigQuery read scope. Neither Connected Sheets client requests Drive. A `--readonly` token cannot mutate a data source at all, and `gog --readonly` blocks the attempt before it reaches Google.

Looker data sources reuse the account's existing Looker link, but the same inspection commands and output shape apply. The lifecycle commands below build BigQuery specifications only.

## List and describe data sources

```bash
gog --readonly --account you@example.com \
  sheets datasource list <spreadsheetId>

gog --readonly --account you@example.com \
  sheets datasource describe <spreadsheetId> <dataSourceId> --json
```

`list` returns a compact source summary joined with its `DATA_SOURCE` sheet and current `DataExecutionStatus`. It deliberately does not print custom SQL. `describe` returns the complete API `DataSource`, associated sheet properties, execution status, and refresh schedules, so its JSON can include a BigQuery raw query and error messages.

## Discover and read extracts

A data-source table has no standalone ID in the Sheets API. Its definition lives only on the table's top-left anchor cell, so the CLI identifies extracts with an A1 anchor that includes the sheet name.

```bash
gog --readonly --account you@example.com \
  sheets datasource table list <spreadsheetId>

gog --readonly --account you@example.com \
  sheets datasource table describe <spreadsheetId> 'Extracts!B3' --json

gog --readonly --account you@example.com \
  sheets datasource table read <spreadsheetId> 'Extracts!B3' \
  --max-rows 250 --json
```

Table discovery asks `spreadsheets.get` only for anchor definitions and related sheet metadata. `read` then uses the selected table's configured columns and row limit to construct a bounded `spreadsheets.values.get` request. The default is at most 1,000 data rows plus the header; JSON output reports `truncated: true` when the configured extract can contain more rows. Use `--render FORMULA` or `--render UNFORMATTED_VALUE` when formatted display values are not suitable.

An extract that syncs every column keeps its column list on the linked `DATA_SOURCE` sheet rather than on the anchor, and the anchor lookup is range-scoped, so `read` issues one additional `spreadsheets.get` for those column definitions. Add pacing when reading many extracts in a loop; back-to-back reads can reach the Sheets per-minute quota.

## Refresh a data source

```bash
gog --account you@example.com \
  sheets datasource refresh <spreadsheetId> <dataSourceId> --json

gog --account you@example.com \
  sheets datasource refresh <spreadsheetId> --all --ignore-state --json

gog --account you@example.com \
  sheets datasource cancel-refresh <spreadsheetId> <dataSourceId> --json
```

Pass either a data source ID or `--all`; passing both is an error rather than a silent choice. `--ignore-state` maps to the API's `force` flag, which is what allows a source already sitting in an error state to be retried — without it such a refresh fails immediately. (`--force` is `gog`'s global confirmation-skipping flag and does not mean this.)

Refresh is asynchronous. These commands report the execution status the API returns at queue time and never block or poll, so an initial `RUNNING` is the normal result. Poll `datasource list` or `datasource describe` until `state` is `SUCCEEDED` or `FAILED`.

A reply references data source *objects* rather than the source itself: a `DATA_SOURCE` sheet, an extract anchor, a pivot table, or a chart. Output identifies each by sheet ID, because the API does not send sheet titles here. With `--all` the API reports only failures, so an empty `statuses` list means nothing failed.

## Add, update, and delete data sources

```bash
gog --account you@example.com \
  sheets datasource add <spreadsheetId> \
  --billing-project my-gcp-project --query-file report.sql --json

gog --account you@example.com \
  sheets datasource add <spreadsheetId> \
  --billing-project my-gcp-project --dataset samples --table shakespeare --json

gog --account you@example.com \
  sheets datasource update <spreadsheetId> <dataSourceId> --query-file report.sql --json

gog --account you@example.com --force \
  sheets datasource delete <spreadsheetId> <dataSourceId> --json
```

`--billing-project` names the BigQuery project charged for queries against the source. It is not called `--project` because `gog` already uses that as an alias of the global `--select`; passing `--project` here sets that instead and then fails for a missing billing project.

`add` takes either custom SQL (`--query` or `--query-file`, where `-` reads stdin) or a table (`--dataset` and `--table`, with `--table-project` defaulting to the billing project) — never both. It creates the data source and its linked `DATA_SOURCE` sheet in one request.

`update` sends a field mask built from the flags actually passed, so anything unmentioned keeps its current value instead of being cleared. Passing a flag with an empty value is an error rather than a silent no-op. Switching an existing source between custom SQL and a table is not supported: mixing query and table flags is rejected.

`delete` removes the data source, its linked `DATA_SOURCE` sheet, and every extract, chart, and pivot table bound to it anywhere in the spreadsheet. It requires confirmation, so scripts need `--force`; in a non-interactive context without `--force` it refuses rather than prompting.
