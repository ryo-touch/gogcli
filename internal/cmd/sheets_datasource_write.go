package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/alecthomas/kong"
	"google.golang.org/api/sheets/v4"

	"github.com/openclaw/gogcli/internal/errfmt"
	"github.com/openclaw/gogcli/internal/outfmt"
	"github.com/openclaw/gogcli/internal/sheetsa1"
	"github.com/openclaw/gogcli/internal/ui"
)

type SheetsDataSourceRefreshCmd struct {
	SpreadsheetID string `arg:"" name:"spreadsheetId" help:"Spreadsheet ID"`
	DataSourceID  string `arg:"" name:"dataSourceId" optional:"" help:"Data source ID; omit and pass --all for every source"`
	All           bool   `name:"all" help:"Refresh every data source in the spreadsheet"`
	IgnoreState   bool   `name:"ignore-state" help:"Refresh regardless of current state; without it a source already in error state fails immediately"`
}

type SheetsDataSourceCancelRefreshCmd struct {
	SpreadsheetID string `arg:"" name:"spreadsheetId" help:"Spreadsheet ID"`
	DataSourceID  string `arg:"" name:"dataSourceId" optional:"" help:"Data source ID; omit and pass --all for every source"`
	All           bool   `name:"all" help:"Cancel refreshes for every data source in the spreadsheet"`
}

type SheetsDataSourceAddCmd struct {
	SpreadsheetID  string `arg:"" name:"spreadsheetId" help:"Spreadsheet ID"`
	BillingProject string `name:"billing-project" help:"BigQuery project billed for queries against this data source (global --project is an alias of --select)"`
	Query          string `name:"query" help:"Custom SQL for the data source"`
	QueryFile      string `name:"query-file" help:"Read custom SQL from a file, or - for stdin"`
	TableProject   string `name:"table-project" help:"Project owning the table (defaults to --billing-project)"`
	Dataset        string `name:"dataset" help:"BigQuery dataset ID"`
	Table          string `name:"table" help:"BigQuery table ID"`
}

type SheetsDataSourceUpdateCmd struct {
	SpreadsheetID  string `arg:"" name:"spreadsheetId" help:"Spreadsheet ID"`
	DataSourceID   string `arg:"" name:"dataSourceId" help:"Data source ID"`
	BillingProject string `name:"billing-project" help:"BigQuery project billed for queries against this data source"`
	Query          string `name:"query" help:"Replacement custom SQL"`
	QueryFile      string `name:"query-file" help:"Read replacement custom SQL from a file, or - for stdin"`
	TableProject   string `name:"table-project" help:"Project owning the table"`
	Dataset        string `name:"dataset" help:"BigQuery dataset ID"`
	Table          string `name:"table" help:"BigQuery table ID"`
}

type SheetsDataSourceDeleteCmd struct {
	SpreadsheetID string `arg:"" name:"spreadsheetId" help:"Spreadsheet ID"`
	DataSourceID  string `arg:"" name:"dataSourceId" help:"Data source ID"`
}

type sheetsDataSourceRefreshStatus struct {
	Reference       string `json:"reference,omitempty"`
	State           string `json:"state,omitempty"`
	LastRefreshTime string `json:"lastRefreshTime,omitempty"`
	ErrorCode       string `json:"errorCode,omitempty"`
	ErrorMessage    string `json:"errorMessage,omitempty"`
}

type sheetsDataSourceCancelStatus struct {
	Reference string `json:"reference,omitempty"`
	State     string `json:"state,omitempty"`
	ErrorCode string `json:"errorCode,omitempty"`
}

func (c *SheetsDataSourceRefreshCmd) Run(ctx context.Context, flags *RootFlags) error {
	spreadsheetID, dataSourceID, err := validateSheetsDataSourceTarget(c.SpreadsheetID, c.DataSourceID, c.All)
	if err != nil {
		return err
	}

	request := &sheets.RefreshDataSourceRequest{
		DataSourceId: dataSourceID,
		Force:        c.IgnoreState,
		IsAll:        c.All,
	}

	return runConnectedSheetsMutation(ctx, flags, connectedSheetsMutation{
		op: "sheets.datasource-refresh",
		dryRunPayload: map[string]any{
			"spreadsheet_id": spreadsheetID,
			"data_source_id": dataSourceID,
			"all":            c.All,
			"ignore_state":   c.IgnoreState,
		},
		request: &sheets.Request{RefreshDataSource: request},
		report: func(reply *sheets.Response) (map[string]any, string) {
			statuses := collectSheetsDataSourceRefreshStatuses(reply)
			return map[string]any{
				"spreadsheetId": spreadsheetID,
				"dataSourceId":  dataSourceID,
				"all":           c.All,
				"statuses":      statuses,
			}, describeSheetsDataSourceRefresh(statuses)
		},
	})
}

func (c *SheetsDataSourceCancelRefreshCmd) Run(ctx context.Context, flags *RootFlags) error {
	spreadsheetID, dataSourceID, err := validateSheetsDataSourceTarget(c.SpreadsheetID, c.DataSourceID, c.All)
	if err != nil {
		return err
	}

	request := &sheets.CancelDataSourceRefreshRequest{
		DataSourceId: dataSourceID,
		IsAll:        c.All,
	}

	return runConnectedSheetsMutation(ctx, flags, connectedSheetsMutation{
		op: "sheets.datasource-cancel-refresh",
		dryRunPayload: map[string]any{
			"spreadsheet_id": spreadsheetID,
			"data_source_id": dataSourceID,
			"all":            c.All,
		},
		request: &sheets.Request{CancelDataSourceRefresh: request},
		report: func(reply *sheets.Response) (map[string]any, string) {
			statuses := collectSheetsDataSourceCancelStatuses(reply)
			return map[string]any{
				"spreadsheetId": spreadsheetID,
				"dataSourceId":  dataSourceID,
				"all":           c.All,
				"statuses":      statuses,
			}, describeSheetsDataSourceCancel(statuses)
		},
	})
}

func (c *SheetsDataSourceAddCmd) Run(ctx context.Context, flags *RootFlags) error {
	spreadsheetID := normalizeGoogleID(strings.TrimSpace(c.SpreadsheetID))
	if spreadsheetID == "" {
		return usage("empty spreadsheetId")
	}

	query, err := resolveSheetsDataSourceQuery(ctx, c.Query, c.QueryFile)
	if err != nil {
		return err
	}
	spec, err := buildBigQueryDataSourceSpec(bigQuerySpecInput{
		project:      c.BillingProject,
		query:        query,
		tableProject: c.TableProject,
		dataset:      c.Dataset,
		table:        c.Table,
	})
	if err != nil {
		return err
	}

	return runConnectedSheetsMutation(ctx, flags, connectedSheetsMutation{
		op: "sheets.datasource-add",
		dryRunPayload: map[string]any{
			"spreadsheet_id": spreadsheetID,
			"spec":           spec,
		},
		request: &sheets.Request{AddDataSource: &sheets.AddDataSourceRequest{
			DataSource: &sheets.DataSource{Spec: &sheets.DataSourceSpec{BigQuery: spec}},
		}},
		report: func(reply *sheets.Response) (map[string]any, string) {
			var added *sheets.AddDataSourceResponse
			if reply != nil {
				added = reply.AddDataSource
			}
			source, status := addedDataSource(added)

			return sheetsDataSourceMutationPayload(spreadsheetID, source, status)
		},
	})
}

func (c *SheetsDataSourceUpdateCmd) Run(ctx context.Context, flags *RootFlags, kctx *kong.Context) error {
	spreadsheetID := normalizeGoogleID(strings.TrimSpace(c.SpreadsheetID))
	dataSourceID := strings.TrimSpace(c.DataSourceID)
	if spreadsheetID == "" {
		return usage("empty spreadsheetId")
	}
	if dataSourceID == "" {
		return usage("empty dataSourceId")
	}

	query, err := resolveSheetsDataSourceQuery(ctx, c.Query, c.QueryFile)
	if err != nil {
		return err
	}
	spec, mask, err := buildBigQueryDataSourceUpdate(kctx, bigQuerySpecInput{
		project:      c.BillingProject,
		query:        query,
		tableProject: c.TableProject,
		dataset:      c.Dataset,
		table:        c.Table,
	})
	if err != nil {
		return err
	}

	return runConnectedSheetsMutation(ctx, flags, connectedSheetsMutation{
		op: "sheets.datasource-update",
		dryRunPayload: map[string]any{
			"spreadsheet_id": spreadsheetID,
			"data_source_id": dataSourceID,
			"fields":         mask,
			"spec":           spec,
		},
		request: &sheets.Request{UpdateDataSource: &sheets.UpdateDataSourceRequest{
			DataSource: &sheets.DataSource{
				DataSourceId: dataSourceID,
				Spec:         &sheets.DataSourceSpec{BigQuery: spec},
			},
			Fields: mask,
		}},
		report: func(reply *sheets.Response) (map[string]any, string) {
			var updated *sheets.UpdateDataSourceResponse
			if reply != nil {
				updated = reply.UpdateDataSource
			}
			source, status := updatedDataSource(updated)

			return sheetsDataSourceMutationPayload(spreadsheetID, source, status)
		},
	})
}

func (c *SheetsDataSourceDeleteCmd) Run(ctx context.Context, flags *RootFlags) error {
	spreadsheetID := normalizeGoogleID(strings.TrimSpace(c.SpreadsheetID))
	dataSourceID := strings.TrimSpace(c.DataSourceID)
	if spreadsheetID == "" {
		return usage("empty spreadsheetId")
	}
	if dataSourceID == "" {
		return usage("empty dataSourceId")
	}

	return runConnectedSheetsMutation(ctx, flags, connectedSheetsMutation{
		op: "sheets.datasource-delete",
		dryRunPayload: map[string]any{
			"spreadsheet_id": spreadsheetID,
			"data_source_id": dataSourceID,
		},
		// Deleting a data source takes its DATA_SOURCE sheet with it, along with
		// every extract, chart, and pivot table bound to it anywhere in the
		// spreadsheet. Name that, rather than only the id being removed.
		confirmAction: fmt.Sprintf(
			"delete data source %s and its linked DATA_SOURCE sheet, plus every extract, chart, and pivot table bound to it",
			dataSourceID,
		),
		request: &sheets.Request{DeleteDataSource: &sheets.DeleteDataSourceRequest{DataSourceId: dataSourceID}},
		report: func(*sheets.Response) (map[string]any, string) {
			return map[string]any{
				"spreadsheetId": spreadsheetID,
				"dataSourceId":  dataSourceID,
				"deleted":       true,
			}, fmt.Sprintf("Deleted data source %s from spreadsheet %s", dataSourceID, spreadsheetID)
		},
	})
}

type connectedSheetsMutation struct {
	op            string
	dryRunPayload map[string]any
	confirmAction string
	request       *sheets.Request
	report        func(*sheets.Response) (map[string]any, string)
}

// runConnectedSheetsMutation is the write-path sibling of runSheetsMutation.
// That helper cannot be reused: it builds the ordinary Sheets client, which has
// no BigQuery scope, and a batchUpdate touching a BigQuery data source is
// rejected without it.
func runConnectedSheetsMutation(ctx context.Context, flags *RootFlags, mutation connectedSheetsMutation) error {
	spreadsheetID, _ := mutation.dryRunPayload["spreadsheet_id"].(string)

	if mutation.confirmAction != "" {
		if err := dryRunAndConfirmDestructive(ctx, flags, mutation.op, mutation.dryRunPayload, mutation.confirmAction); err != nil {
			return err
		}
	} else if err := dryRunExit(ctx, flags, mutation.op, mutation.dryRunPayload); err != nil {
		return err
	}

	account, svc, err := requireConnectedSheetsWriterService(ctx, flags)
	if err != nil {
		return err
	}

	resp, err := svc.Spreadsheets.BatchUpdate(spreadsheetID, &sheets.BatchUpdateSpreadsheetRequest{
		Requests: []*sheets.Request{mutation.request},
	}).Context(ctx).Do()
	if err != nil {
		return wrapConnectedSheetsWriteError(err, account)
	}

	var reply *sheets.Response
	if resp != nil && len(resp.Replies) > 0 {
		reply = resp.Replies[0]
	}
	payload, text := mutation.report(reply)

	if outfmt.IsJSON(ctx) {
		return outfmt.WriteJSON(ctx, stdoutWriter(ctx), payload)
	}
	ui.FromContext(ctx).Out().Linef("%s", text)

	return nil
}

// validateSheetsDataSourceTarget enforces exactly one target. The API would
// otherwise accept a request naming both and silently pick one.
func validateSheetsDataSourceTarget(rawSpreadsheetID, rawDataSourceID string, all bool) (string, string, error) {
	spreadsheetID := normalizeGoogleID(strings.TrimSpace(rawSpreadsheetID))
	dataSourceID := strings.TrimSpace(rawDataSourceID)
	if spreadsheetID == "" {
		return "", "", usage("empty spreadsheetId")
	}
	if all && dataSourceID != "" {
		return "", "", usage("pass either a dataSourceId or --all, not both")
	}
	if !all && dataSourceID == "" {
		return "", "", usage("pass a dataSourceId, or --all for every data source")
	}

	return spreadsheetID, dataSourceID, nil
}

type bigQuerySpecInput struct {
	project      string
	query        string
	tableProject string
	dataset      string
	table        string
}

func (in bigQuerySpecInput) trimmed() bigQuerySpecInput {
	return bigQuerySpecInput{
		project:      strings.TrimSpace(in.project),
		query:        strings.TrimSpace(in.query),
		tableProject: strings.TrimSpace(in.tableProject),
		dataset:      strings.TrimSpace(in.dataset),
		table:        strings.TrimSpace(in.table),
	}
}

func (in bigQuerySpecInput) tableMode() bool {
	return in.tableProject != "" || in.dataset != "" || in.table != ""
}

// buildBigQueryDataSourceSpec builds a complete spec for add, where the API
// needs the whole thing and there is nothing to merge with.
func buildBigQueryDataSourceSpec(raw bigQuerySpecInput) (*sheets.BigQueryDataSourceSpec, error) {
	in := raw.trimmed()
	if in.project == "" {
		return nil, usage("--billing-project is required")
	}
	if in.query == "" && !in.tableMode() {
		return nil, usage("pass --query/--query-file for custom SQL, or --dataset and --table for a table")
	}
	if in.query != "" && in.tableMode() {
		return nil, usage("--query/--query-file and --dataset/--table are mutually exclusive")
	}

	spec := &sheets.BigQueryDataSourceSpec{ProjectId: in.project}
	if in.query != "" {
		spec.QuerySpec = &sheets.BigQueryQuerySpec{RawQuery: in.query}

		return spec, nil
	}
	if in.dataset == "" || in.table == "" {
		return nil, usage("--dataset and --table are both required for a table data source")
	}
	spec.TableSpec = &sheets.BigQueryTableSpec{
		// Left empty the API assumes projectId, which is also how the read path
		// renders a table source; sending it explicitly keeps the two agreeing.
		TableProjectId: firstNonEmpty(in.tableProject, in.project),
		DatasetId:      in.dataset,
		TableId:        in.table,
	}

	return spec, nil
}

// buildBigQueryDataSourceUpdate builds a partial spec plus the field mask that
// scopes it. The mask is derived from which flags the caller actually typed, so
// an unmentioned field is left alone by the API rather than being overwritten
// with a zero value — no read-modify-write round trip needed.
func buildBigQueryDataSourceUpdate(kctx *kong.Context, raw bigQuerySpecInput) (*sheets.BigQueryDataSourceSpec, string, error) {
	in := raw.trimmed()
	queryProvided := flagProvided(kctx, "query") || flagProvided(kctx, "query-file")
	tableProvided := flagProvided(kctx, "table-project") || flagProvided(kctx, "dataset") || flagProvided(kctx, "table")

	if queryProvided && tableProvided {
		return nil, "", usage("--query/--query-file and --dataset/--table are mutually exclusive")
	}

	spec := &sheets.BigQueryDataSourceSpec{}
	mask := make([]string, 0, 5)

	if flagProvided(kctx, "billing-project") {
		if in.project == "" {
			return nil, "", usage("--billing-project cannot be empty")
		}
		spec.ProjectId = in.project
		mask = append(mask, "spec.bigQuery.projectId")
	}
	if queryProvided {
		if in.query == "" {
			return nil, "", usage("--query cannot be empty")
		}
		spec.QuerySpec = &sheets.BigQueryQuerySpec{RawQuery: in.query}
		mask = append(mask, "spec.bigQuery.querySpec.rawQuery")
	}
	if tableProvided {
		tableSpec, tableMask, err := buildBigQueryTableSpecUpdate(kctx, in)
		if err != nil {
			return nil, "", err
		}
		spec.TableSpec = tableSpec
		mask = append(mask, tableMask...)
	}
	if len(mask) == 0 {
		return nil, "", usage("nothing to update: pass --billing-project, --query/--query-file, or --dataset/--table")
	}

	return spec, strings.Join(mask, ","), nil
}

func buildBigQueryTableSpecUpdate(kctx *kong.Context, in bigQuerySpecInput) (*sheets.BigQueryTableSpec, []string, error) {
	spec := &sheets.BigQueryTableSpec{}
	mask := make([]string, 0, 3)

	for _, field := range []struct {
		flag  string
		value string
		path  string
		set   func(string)
	}{
		{flag: "table-project", value: in.tableProject, path: "spec.bigQuery.tableSpec.tableProjectId", set: func(v string) { spec.TableProjectId = v }},
		{flag: "dataset", value: in.dataset, path: "spec.bigQuery.tableSpec.datasetId", set: func(v string) { spec.DatasetId = v }},
		{flag: "table", value: in.table, path: "spec.bigQuery.tableSpec.tableId", set: func(v string) { spec.TableId = v }},
	} {
		if !flagProvided(kctx, field.flag) {
			continue
		}
		if field.value == "" {
			return nil, nil, usagef("--%s cannot be empty", field.flag)
		}
		field.set(field.value)
		mask = append(mask, field.path)
	}

	return spec, mask, nil
}

// resolveSheetsDataSourceQuery deliberately does not fall back to reading stdin
// when neither flag is given: a table-mode invocation inside a pipeline would
// otherwise silently pick up unrelated input as SQL.
func resolveSheetsDataSourceQuery(ctx context.Context, query, queryFile string) (string, error) {
	query = strings.TrimSpace(query)
	queryFile = strings.TrimSpace(queryFile)

	if query != "" && queryFile != "" {
		return "", usage("--query and --query-file are mutually exclusive")
	}
	if queryFile == "" {
		return query, nil
	}
	if queryFile == "-" {
		data, err := io.ReadAll(stdinReader(ctx))
		if err != nil {
			return "", fmt.Errorf("reading stdin: %w", err)
		}

		return strings.TrimSpace(string(data)), nil
	}

	data, err := os.ReadFile(queryFile) //nolint:gosec // user-provided path
	if err != nil {
		return "", fmt.Errorf("reading query file: %w", err)
	}

	return strings.TrimSpace(string(data)), nil
}

func addedDataSource(resp *sheets.AddDataSourceResponse) (*sheets.DataSource, *sheets.DataExecutionStatus) {
	if resp == nil {
		return nil, nil
	}

	return resp.DataSource, resp.DataExecutionStatus
}

func updatedDataSource(resp *sheets.UpdateDataSourceResponse) (*sheets.DataSource, *sheets.DataExecutionStatus) {
	if resp == nil {
		return nil, nil
	}

	return resp.DataSource, resp.DataExecutionStatus
}

func sheetsDataSourceMutationPayload(spreadsheetID string, source *sheets.DataSource, status *sheets.DataExecutionStatus) (map[string]any, string) {
	payload := map[string]any{"spreadsheetId": spreadsheetID}
	dataSourceID := ""
	if source != nil {
		dataSourceID = source.DataSourceId
		payload["dataSource"] = source
		// Live spreadsheets.get omits dataSources[].sheetId (see #1001); whether
		// the mutation replies populate it is worth reporting when they do rather
		// than assuming either way.
		if source.SheetId != 0 {
			payload["sheetId"] = source.SheetId
		}
	}
	payload["dataSourceId"] = dataSourceID

	state := ""
	if status != nil {
		payload["dataExecutionStatus"] = status
		state = status.State
	}

	return payload, strings.TrimSpace(fmt.Sprintf("Data source %s %s", dataSourceID, state))
}

func collectSheetsDataSourceRefreshStatuses(reply *sheets.Response) []sheetsDataSourceRefreshStatus {
	statuses := make([]sheetsDataSourceRefreshStatus, 0)
	if reply == nil || reply.RefreshDataSource == nil {
		return statuses
	}

	for _, entry := range reply.RefreshDataSource.Statuses {
		if entry == nil {
			continue
		}
		status := sheetsDataSourceRefreshStatus{Reference: formatSheetsDataSourceObjectReference(entry.Reference)}
		setSheetsDataExecutionStatus(&status.State, &status.LastRefreshTime, &status.ErrorCode, &status.ErrorMessage, entry.DataExecutionStatus)
		statuses = append(statuses, status)
	}

	return statuses
}

func collectSheetsDataSourceCancelStatuses(reply *sheets.Response) []sheetsDataSourceCancelStatus {
	statuses := make([]sheetsDataSourceCancelStatus, 0)
	if reply == nil || reply.CancelDataSourceRefresh == nil {
		return statuses
	}

	for _, entry := range reply.CancelDataSourceRefresh.Statuses {
		if entry == nil {
			continue
		}
		status := sheetsDataSourceCancelStatus{Reference: formatSheetsDataSourceObjectReference(entry.Reference)}
		if entry.RefreshCancellationStatus != nil {
			status.State = entry.RefreshCancellationStatus.State
			status.ErrorCode = entry.RefreshCancellationStatus.ErrorCode
		}
		statuses = append(statuses, status)
	}

	return statuses
}

// formatSheetsDataSourceObjectReference renders what a refresh reply points at.
// Replies reference data source *objects* — a DATA_SOURCE sheet, an extract
// anchor, a pivot table, a chart — not the source itself, and an anchor arrives
// as a sheet id with no title, so the sheet id has to stand in for a name.
func formatSheetsDataSourceObjectReference(reference *sheets.DataSourceObjectReference) string {
	if reference == nil {
		return ""
	}
	switch {
	case reference.SheetId != "":
		return "sheet " + reference.SheetId
	case reference.DataSourceTableAnchorCell != nil:
		return "table " + formatSheetsGridCoordinate(reference.DataSourceTableAnchorCell)
	case reference.DataSourcePivotTableAnchorCell != nil:
		return "pivot " + formatSheetsGridCoordinate(reference.DataSourcePivotTableAnchorCell)
	case reference.DataSourceFormulaCell != nil:
		return "formula " + formatSheetsGridCoordinate(reference.DataSourceFormulaCell)
	case reference.ChartId != 0:
		return "chart " + strconv.FormatInt(reference.ChartId, 10)
	default:
		return ""
	}
}

func formatSheetsGridCoordinate(coordinate *sheets.GridCoordinate) string {
	if coordinate == nil {
		return ""
	}
	// GridCoordinate indexes are zero-based; A1 notation is not.
	letters, err := sheetsa1.ColumnLetters(int(coordinate.ColumnIndex) + 1)
	if err != nil {
		return fmt.Sprintf("sheetId=%d", coordinate.SheetId)
	}

	return fmt.Sprintf("sheetId=%d %s%d", coordinate.SheetId, letters, coordinate.RowIndex+1)
}

func describeSheetsDataSourceRefresh(statuses []sheetsDataSourceRefreshStatus) string {
	if len(statuses) == 0 {
		// isAll only reports failures, so an empty reply means nothing failed.
		return "Refresh queued"
	}
	parts := make([]string, 0, len(statuses))
	for _, status := range statuses {
		parts = append(parts, strings.TrimSpace(status.Reference+" "+status.State))
	}

	return "Refresh queued: " + strings.Join(parts, ", ")
}

func describeSheetsDataSourceCancel(statuses []sheetsDataSourceCancelStatus) string {
	if len(statuses) == 0 {
		return "Cancellation requested"
	}
	parts := make([]string, 0, len(statuses))
	for _, status := range statuses {
		parts = append(parts, strings.TrimSpace(status.Reference+" "+status.State))
	}

	return "Cancellation requested: " + strings.Join(parts, ", ")
}

func wrapConnectedSheetsWriteError(err error, account string) error {
	if err == nil || !isInsufficientScopeError(err) {
		return err
	}

	return errfmt.NewUserFacingError(
		fmt.Sprintf("Connected Sheets mutations require OAuth scopes %s and %s; re-authenticate while preserving this account's existing --services selection and append --extra-scopes %s --force-consent (for a Sheets-only token: gog auth add %s --services sheets --extra-scopes %s --force-consent). A --readonly token cannot mutate data sources at all.",
			connectedSheetsWriteScope, connectedSheetsBigQueryScope, connectedSheetsBigQueryScope, account, connectedSheetsBigQueryScope),
		err,
	)
}
