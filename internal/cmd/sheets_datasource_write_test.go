package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"google.golang.org/api/sheets/v4"

	"github.com/openclaw/gogcli/internal/app"
)

func TestSheetsDataSourceRefreshByID(t *testing.T) {
	fixture := newConnectedSheetsFixtureServer(t)
	fixture.SetBatchReply(&sheets.Response{RefreshDataSource: &sheets.RefreshDataSourceResponse{
		Statuses: []*sheets.RefreshDataSourceObjectExecutionStatus{{
			Reference:           &sheets.DataSourceObjectReference{SheetId: "102"},
			DataExecutionStatus: &sheets.DataExecutionStatus{State: "RUNNING"},
		}, {
			Reference: &sheets.DataSourceObjectReference{
				DataSourceTableAnchorCell: &sheets.GridCoordinate{SheetId: 103, RowIndex: 2, ColumnIndex: 1},
			},
			DataExecutionStatus: &sheets.DataExecutionStatus{State: "RUNNING"},
		}},
	}})
	svc := newSheetsServiceFromServer(t, fixture.server)

	result := executeWithSheetsTestService(t, []string{
		"--json", "--account", "services@openclaw.org",
		"sheets", "datasource", "refresh", "connected1", "ds-query",
	}, svc)
	if result.err != nil {
		t.Fatalf("refresh data source: %v", result.err)
	}

	request := fixture.OnlyBatchUpdate(t).Requests[0].RefreshDataSource
	if request == nil {
		t.Fatalf("batchUpdate did not carry a refreshDataSource request")
	}
	if request.DataSourceId != "ds-query" || request.IsAll || request.Force {
		t.Fatalf("refresh request = %#v", request)
	}

	var payload struct {
		DataSourceID string `json:"dataSourceId"`
		All          bool   `json:"all"`
		Statuses     []struct {
			Reference string `json:"reference"`
			State     string `json:"state"`
		} `json:"statuses"`
	}
	if err := json.Unmarshal([]byte(result.stdout), &payload); err != nil {
		t.Fatalf("decode refresh JSON: %v\n%s", err, result.stdout)
	}
	if payload.DataSourceID != "ds-query" || payload.All || len(payload.Statuses) != 2 {
		t.Fatalf("unexpected refresh payload: %#v", payload)
	}
	// A reply references data source objects, not the source: a DATA_SOURCE sheet
	// by id, and an extract by its anchor cell, which arrives zero-based.
	if payload.Statuses[0].Reference != "sheet 102" || payload.Statuses[0].State != "RUNNING" {
		t.Fatalf("sheet reference = %#v", payload.Statuses[0])
	}
	if payload.Statuses[1].Reference != "table sheetId=103 B3" {
		t.Fatalf("anchor reference = %#v", payload.Statuses[1])
	}
}

func TestSheetsDataSourceRefreshAllIgnoringState(t *testing.T) {
	fixture := newConnectedSheetsFixtureServer(t)
	svc := newSheetsServiceFromServer(t, fixture.server)

	result := executeWithSheetsTestService(t, []string{
		"--json", "--account", "services@openclaw.org",
		"sheets", "datasource", "refresh", "connected1", "--all", "--ignore-state",
	}, svc)
	if result.err != nil {
		t.Fatalf("refresh all data sources: %v", result.err)
	}

	request := fixture.OnlyBatchUpdate(t).Requests[0].RefreshDataSource
	// --ignore-state maps to RefreshDataSourceRequest.force, which is what lets a
	// source already in error state be retried at all.
	if request == nil || !request.IsAll || !request.Force || request.DataSourceId != "" {
		t.Fatalf("refresh-all request = %#v", request)
	}
	// isAll replies carry only failures, so no statuses means nothing failed.
	if !strings.Contains(result.stdout, `"statuses": []`) {
		t.Fatalf("expected an empty status list: %s", result.stdout)
	}
}

func TestSheetsDataSourceCancelRefresh(t *testing.T) {
	fixture := newConnectedSheetsFixtureServer(t)
	fixture.SetBatchReply(&sheets.Response{CancelDataSourceRefresh: &sheets.CancelDataSourceRefreshResponse{
		Statuses: []*sheets.CancelDataSourceRefreshStatus{{
			Reference:                 &sheets.DataSourceObjectReference{SheetId: "102"},
			RefreshCancellationStatus: &sheets.RefreshCancellationStatus{State: "CANCEL_FAILED", ErrorCode: "QUERY_EXECUTION_COMPLETED"},
		}},
	}})
	svc := newSheetsServiceFromServer(t, fixture.server)

	result := executeWithSheetsTestService(t, []string{
		"--json", "--account", "services@openclaw.org",
		"sheets", "datasource", "cancel-refresh", "connected1", "ds-query",
	}, svc)
	if result.err != nil {
		t.Fatalf("cancel refresh: %v", result.err)
	}

	request := fixture.OnlyBatchUpdate(t).Requests[0].CancelDataSourceRefresh
	if request == nil || request.DataSourceId != "ds-query" || request.IsAll {
		t.Fatalf("cancel request = %#v", request)
	}
	// CANCEL_SUCCEEDED only means the call was accepted, so the state and error
	// code both have to reach the caller rather than being flattened to "ok".
	if !strings.Contains(result.stdout, `"state": "CANCEL_FAILED"`) ||
		!strings.Contains(result.stdout, `"errorCode": "QUERY_EXECUTION_COMPLETED"`) {
		t.Fatalf("cancel status not reported: %s", result.stdout)
	}
}

func TestSheetsDataSourceAddQuerySource(t *testing.T) {
	fixture := newConnectedSheetsFixtureServer(t)
	fixture.SetBatchReply(&sheets.Response{AddDataSource: &sheets.AddDataSourceResponse{
		DataSource:          &sheets.DataSource{DataSourceId: "ds-new"},
		DataExecutionStatus: &sheets.DataExecutionStatus{State: "RUNNING"},
	}})
	svc := newSheetsServiceFromServer(t, fixture.server)

	result := executeWithSheetsTestService(t, []string{
		"--json", "--account", "services@openclaw.org",
		"sheets", "datasource", "add", "connected1",
		"--billing-project", "billing-proj", "--query", "SELECT 1",
	}, svc)
	if result.err != nil {
		t.Fatalf("add data source: %v", result.err)
	}

	spec := addedBigQuerySpec(t, fixture)
	if spec.ProjectId != "billing-proj" || spec.TableSpec != nil {
		t.Fatalf("query source spec = %#v", spec)
	}
	if spec.QuerySpec == nil || spec.QuerySpec.RawQuery != "SELECT 1" {
		t.Fatalf("query source raw query = %#v", spec.QuerySpec)
	}
	if !strings.Contains(result.stdout, `"dataSourceId": "ds-new"`) ||
		!strings.Contains(result.stdout, `"state": "RUNNING"`) {
		t.Fatalf("add output missing new identity or status: %s", result.stdout)
	}
}

func TestSheetsDataSourceAddTableSourceDefaultsTableProject(t *testing.T) {
	fixture := newConnectedSheetsFixtureServer(t)
	svc := newSheetsServiceFromServer(t, fixture.server)

	result := executeWithSheetsTestService(t, []string{
		"--json", "--account", "services@openclaw.org",
		"sheets", "datasource", "add", "connected1",
		"--billing-project", "billing-proj", "--dataset", "samples", "--table", "shakespeare",
	}, svc)
	if result.err != nil {
		t.Fatalf("add table data source: %v", result.err)
	}

	spec := addedBigQuerySpec(t, fixture)
	if spec.QuerySpec != nil || spec.TableSpec == nil {
		t.Fatalf("table source spec = %#v", spec)
	}
	// Left unset the API assumes the billing project. Sending it explicitly keeps
	// the request agreeing with how the read path renders a table source.
	if spec.TableSpec.TableProjectId != "billing-proj" ||
		spec.TableSpec.DatasetId != "samples" ||
		spec.TableSpec.TableId != "shakespeare" {
		t.Fatalf("table spec = %#v", spec.TableSpec)
	}
}

func TestSheetsDataSourceUpdateFieldMaskFollowsTypedFlags(t *testing.T) {
	for _, test := range []struct {
		name     string
		args     []string
		wantMask string
		assert   func(*testing.T, *sheets.BigQueryDataSourceSpec)
	}{{
		name:     "query only",
		args:     []string{"--query", "SELECT 2"},
		wantMask: "spec.bigQuery.querySpec.rawQuery",
		assert: func(t *testing.T, spec *sheets.BigQueryDataSourceSpec) {
			t.Helper()
			// The mask is what stops an unmentioned projectId from being cleared,
			// so the spec must genuinely omit it rather than send an empty string.
			if spec.ProjectId != "" || spec.TableSpec != nil {
				t.Fatalf("spec should carry only the query: %#v", spec)
			}
			if spec.QuerySpec == nil || spec.QuerySpec.RawQuery != "SELECT 2" {
				t.Fatalf("query spec = %#v", spec.QuerySpec)
			}
		},
	}, {
		name:     "billing project only",
		args:     []string{"--billing-project", "other-proj"},
		wantMask: "spec.bigQuery.projectId",
		assert: func(t *testing.T, spec *sheets.BigQueryDataSourceSpec) {
			t.Helper()
			if spec.ProjectId != "other-proj" || spec.QuerySpec != nil || spec.TableSpec != nil {
				t.Fatalf("spec should carry only the project: %#v", spec)
			}
		},
	}, {
		name:     "table fields and project together",
		args:     []string{"--billing-project", "other-proj", "--dataset", "d2", "--table", "t2"},
		wantMask: "spec.bigQuery.projectId,spec.bigQuery.tableSpec.datasetId,spec.bigQuery.tableSpec.tableId",
		assert: func(t *testing.T, spec *sheets.BigQueryDataSourceSpec) {
			t.Helper()
			// table-project was not typed, so it stays out of both mask and spec.
			if spec.TableSpec == nil || spec.TableSpec.TableProjectId != "" {
				t.Fatalf("table spec should omit an untyped table project: %#v", spec.TableSpec)
			}
		},
	}} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newConnectedSheetsFixtureServer(t)
			svc := newSheetsServiceFromServer(t, fixture.server)

			args := append([]string{
				"--json", "--account", "services@openclaw.org",
				"sheets", "datasource", "update", "connected1", "ds-query",
			}, test.args...)
			result := executeWithSheetsTestService(t, args, svc)
			if result.err != nil {
				t.Fatalf("update data source: %v", result.err)
			}

			request := fixture.OnlyBatchUpdate(t).Requests[0].UpdateDataSource
			if request == nil || request.DataSource == nil {
				t.Fatalf("batchUpdate did not carry an updateDataSource request")
			}
			if request.Fields != test.wantMask {
				t.Fatalf("field mask = %q, want %q", request.Fields, test.wantMask)
			}
			if request.DataSource.DataSourceId != "ds-query" {
				t.Fatalf("update lost the data source id: %#v", request.DataSource)
			}
			if request.DataSource.Spec == nil || request.DataSource.Spec.BigQuery == nil {
				t.Fatalf("update carried no BigQuery spec: %#v", request.DataSource.Spec)
			}
			test.assert(t, request.DataSource.Spec.BigQuery)
		})
	}
}

func TestSheetsDataSourceDeleteRequiresConfirmation(t *testing.T) {
	t.Run("refused without force", func(t *testing.T) {
		fixture := newConnectedSheetsFixtureServer(t)
		svc := newSheetsServiceFromServer(t, fixture.server)

		result := executeWithSheetsTestService(t, []string{
			"--json", "--no-input", "--account", "services@openclaw.org",
			"sheets", "datasource", "delete", "connected1", "ds-query",
		}, svc)
		if result.err == nil || ExitCode(result.err) != 2 {
			t.Fatalf("err = %v, exit = %d, want a usage error", result.err, ExitCode(result.err))
		}
		// The prompt has to name the collateral damage, not just the id.
		if !strings.Contains(result.err.Error(), "linked DATA_SOURCE sheet") {
			t.Fatalf("confirmation text should describe the blast radius: %v", result.err)
		}
		if got := len(fixture.BatchUpdates()); got != 0 {
			t.Fatalf("batchUpdate calls = %d, want 0 before confirmation", got)
		}
	})

	t.Run("proceeds with force", func(t *testing.T) {
		fixture := newConnectedSheetsFixtureServer(t)
		svc := newSheetsServiceFromServer(t, fixture.server)

		result := executeWithSheetsTestService(t, []string{
			"--json", "--force", "--account", "services@openclaw.org",
			"sheets", "datasource", "delete", "connected1", "ds-query",
		}, svc)
		if result.err != nil {
			t.Fatalf("delete data source: %v", result.err)
		}

		request := fixture.OnlyBatchUpdate(t).Requests[0].DeleteDataSource
		if request == nil || request.DataSourceId != "ds-query" {
			t.Fatalf("delete request = %#v", request)
		}
		if !strings.Contains(result.stdout, `"deleted": true`) {
			t.Fatalf("delete output = %s", result.stdout)
		}
	})
}

func TestSheetsDataSourceWriteDryRunIssuesNoRequests(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		op   string
	}{
		{name: "refresh", args: []string{"refresh", "connected1", "ds-query"}, op: "sheets.datasource-refresh"},
		{name: "cancel-refresh", args: []string{"cancel-refresh", "connected1", "--all"}, op: "sheets.datasource-cancel-refresh"},
		{name: "add", args: []string{"add", "connected1", "--billing-project", "p", "--query", "SELECT 1"}, op: "sheets.datasource-add"},
		{name: "update", args: []string{"update", "connected1", "ds-query", "--query", "SELECT 2"}, op: "sheets.datasource-update"},
		{name: "delete", args: []string{"delete", "connected1", "ds-query"}, op: "sheets.datasource-delete"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newConnectedSheetsFixtureServer(t)
			svc := newSheetsServiceFromServer(t, fixture.server)

			args := append([]string{
				"--json", "--dry-run", "--no-input", "--account", "services@openclaw.org",
				"sheets", "datasource",
			}, test.args...)
			result := executeWithSheetsTestService(t, args, svc)
			if result.err != nil && ExitCode(result.err) != 0 {
				t.Fatalf("dry run: %v", result.err)
			}

			var payload struct {
				DryRun bool   `json:"dry_run"`
				Op     string `json:"op"`
			}
			if err := json.Unmarshal([]byte(result.stdout), &payload); err != nil {
				t.Fatalf("decode dry-run JSON: %v\n%s", err, result.stdout)
			}
			if !payload.DryRun || payload.Op != test.op {
				t.Fatalf("dry-run payload = %#v, want op %q", payload, test.op)
			}
			// A dry run that still reached the API would defeat the point, and
			// delete must not even get as far as its confirmation prompt.
			if got := len(fixture.Queries()); got != 0 {
				t.Fatalf("HTTP calls = %d, want 0 during a dry run", got)
			}
		})
	}
}

func TestSheetsDataSourceRefreshTargetValidation(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{name: "no target", args: []string{"refresh", "connected1"}, want: "pass a dataSourceId, or --all"},
		{name: "both targets", args: []string{"refresh", "connected1", "ds-query", "--all"}, want: "not both"},
		{name: "cancel no target", args: []string{"cancel-refresh", "connected1"}, want: "pass a dataSourceId, or --all"},
		{name: "add without project", args: []string{"add", "connected1", "--query", "SELECT 1"}, want: "--billing-project is required"},
		{name: "add without source", args: []string{"add", "connected1", "--billing-project", "p"}, want: "--query/--query-file for custom SQL"},
		{name: "add with both sources", args: []string{"add", "connected1", "--billing-project", "p", "--query", "q", "--table", "t"}, want: "mutually exclusive"},
		{name: "add partial table", args: []string{"add", "connected1", "--billing-project", "p", "--dataset", "d"}, want: "--dataset and --table are both required"},
		{name: "update without changes", args: []string{"update", "connected1", "ds-query"}, want: "nothing to update"},
		// Typed-but-empty is not the same as absent: deciding by value rather than
		// by flagProvided would silently report "nothing to update" here and send
		// no request at all.
		{name: "update with empty query", args: []string{"update", "connected1", "ds-query", "--query", ""}, want: "--query cannot be empty"},
		{name: "update with empty billing project", args: []string{"update", "connected1", "ds-query", "--billing-project", ""}, want: "--billing-project cannot be empty"},
		{name: "update with empty dataset", args: []string{"update", "connected1", "ds-query", "--dataset", ""}, want: "--dataset cannot be empty"},
		{name: "update with both sources", args: []string{"update", "connected1", "ds-query", "--query", "q", "--dataset", "d"}, want: "mutually exclusive"},
		{name: "query and query-file", args: []string{"add", "connected1", "--billing-project", "p", "--query", "q", "--query-file", "f"}, want: "--query and --query-file are mutually exclusive"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newConnectedSheetsFixtureServer(t)
			svc := newSheetsServiceFromServer(t, fixture.server)

			args := append([]string{
				"--json", "--no-input", "--account", "services@openclaw.org",
				"sheets", "datasource",
			}, test.args...)
			result := executeWithSheetsTestService(t, args, svc)
			if result.err == nil || !strings.Contains(result.err.Error(), test.want) {
				t.Fatalf("err = %v, want %q", result.err, test.want)
			}
			if ExitCode(result.err) != 2 {
				t.Fatalf("ExitCode = %d, want 2", ExitCode(result.err))
			}
			if got := len(fixture.Queries()); got != 0 {
				t.Fatalf("HTTP calls = %d, want 0 for a rejected invocation", got)
			}
		})
	}
}

// TestSheetsDataSourceWritePrefersWriterService pins the plumbing: mutations must
// go through the client carrying spreadsheets write scope, not the read-only
// Connected Sheets client, whose token cannot batchUpdate at all.
func TestSheetsDataSourceWritePrefersWriterService(t *testing.T) {
	fixture := newConnectedSheetsFixtureServer(t)
	svc := newSheetsServiceFromServer(t, fixture.server)

	readerCalls, writerCalls := 0, 0
	result := executeWithTestRuntime(t, []string{
		"--json", "--account", "services@openclaw.org",
		"sheets", "datasource", "refresh", "connected1", "ds-query",
	}, &app.Runtime{Services: app.Services{
		ConnectedSheets: func(context.Context, string) (*sheets.Service, error) {
			readerCalls++

			return svc, nil
		},
		ConnectedSheetsWriter: func(context.Context, string) (*sheets.Service, error) {
			writerCalls++

			return svc, nil
		},
	}})
	if result.err != nil {
		t.Fatalf("refresh data source: %v", result.err)
	}
	if writerCalls != 1 || readerCalls != 0 {
		t.Fatalf("writer calls = %d, reader calls = %d; want 1 and 0", writerCalls, readerCalls)
	}
}

func TestWrapConnectedSheetsWriteError(t *testing.T) {
	cause := errors.New("Request had insufficient authentication scopes")
	err := wrapConnectedSheetsWriteError(cause, "services@openclaw.org")
	if err == nil {
		t.Fatalf("expected wrapped guidance")
	}
	// A caller stuck here needs both scopes named: the read-path guidance alone
	// would send them back with a token that still cannot mutate.
	if !strings.Contains(err.Error(), connectedSheetsWriteScope) ||
		!strings.Contains(err.Error(), connectedSheetsBigQueryScope) ||
		!strings.Contains(err.Error(), "--readonly") {
		t.Fatalf("missing reauthorization guidance: %v", err)
	}

	plain := errors.New("permission denied for BigQuery table")
	if got := wrapConnectedSheetsWriteError(plain, "services@openclaw.org"); !errors.Is(got, plain) {
		t.Fatalf("ordinary permission error should be preserved: %v", got)
	}
}

func TestFormatSheetsDataSourceObjectReference(t *testing.T) {
	for _, test := range []struct {
		name      string
		reference *sheets.DataSourceObjectReference
		want      string
	}{
		{name: "nil", reference: nil, want: ""},
		// DataSourceObjectReference.sheetId is a string in the Sheets SDK, unlike
		// every other sheet id on the wire.
		{name: "sheet", reference: &sheets.DataSourceObjectReference{SheetId: "42"}, want: "sheet 42"},
		{
			name:      "table anchor",
			reference: &sheets.DataSourceObjectReference{DataSourceTableAnchorCell: &sheets.GridCoordinate{SheetId: 7, RowIndex: 0, ColumnIndex: 0}},
			want:      "table sheetId=7 A1",
		},
		{
			name:      "pivot anchor",
			reference: &sheets.DataSourceObjectReference{DataSourcePivotTableAnchorCell: &sheets.GridCoordinate{SheetId: 7, RowIndex: 4, ColumnIndex: 26}},
			want:      "pivot sheetId=7 AA5",
		},
		{name: "chart", reference: &sheets.DataSourceObjectReference{ChartId: 9}, want: "chart 9"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := formatSheetsDataSourceObjectReference(test.reference); got != test.want {
				t.Fatalf("formatSheetsDataSourceObjectReference = %q, want %q", got, test.want)
			}
		})
	}
}

func addedBigQuerySpec(t *testing.T, fixture *connectedSheetsFixture) *sheets.BigQueryDataSourceSpec {
	t.Helper()
	request := fixture.OnlyBatchUpdate(t).Requests[0].AddDataSource
	if request == nil || request.DataSource == nil || request.DataSource.Spec == nil {
		t.Fatalf("batchUpdate did not carry an addDataSource spec")
	}
	if request.DataSource.Spec.BigQuery == nil {
		t.Fatalf("addDataSource spec is not a BigQuery spec: %#v", request.DataSource.Spec)
	}

	return request.DataSource.Spec.BigQuery
}
