package googleapi

import (
	"context"
	"log/slog"

	"google.golang.org/api/sheets/v4"

	"github.com/openclaw/gogcli/internal/googleauth"
)

const (
	scopeSpreadsheets         = "https://www.googleapis.com/auth/spreadsheets"
	scopeSpreadsheetsReadOnly = "https://www.googleapis.com/auth/spreadsheets.readonly"
	scopeBigQueryReadOnly     = "https://www.googleapis.com/auth/bigquery.readonly"
)

func NewSheets(ctx context.Context, email string) (*sheets.Service, error) {
	slog.Debug("creating sheets service", "email", email)

	svc, err := newGoogleServiceForAccount(ctx, email, googleauth.ServiceSheets, "sheets", sheets.NewService)
	if err != nil {
		slog.Error("failed to create sheets service", "email", email, "error", err)
		return nil, err
	}

	slog.Debug("sheets service created successfully", "email", email)

	return svc, nil
}

// NewConnectedSheets creates a read-only Sheets client whose token also has
// the BigQuery scope required when a response contains Connected Sheets data.
// Keeping this separate from NewSheets avoids broadening ordinary Sheets auth.
func NewConnectedSheets(ctx context.Context, email string) (*sheets.Service, error) {
	slog.Debug("creating Connected Sheets service", "email", email)

	svc, err := newGoogleServiceForScopes(
		ctx,
		email,
		string(googleauth.ServiceSheets),
		"Connected Sheets",
		[]string{scopeSpreadsheetsReadOnly, scopeBigQueryReadOnly},
		sheets.NewService,
	)
	if err != nil {
		slog.Error("failed to create Connected Sheets service", "email", email, "error", err)
		return nil, err
	}

	slog.Debug("Connected Sheets service created successfully", "email", email)

	return svc, nil
}

// NewConnectedSheetsWriter creates the Sheets client used for Connected Sheets
// mutations: data source add, update, refresh, cancel-refresh, and delete all go
// through spreadsheets.batchUpdate, which the read-only client cannot issue.
//
// It asks for spreadsheets write access plus the same BigQuery scope, and
// deliberately not Drive, so it stays narrower than ordinary Sheets auth on
// every axis except the one it needs.
func NewConnectedSheetsWriter(ctx context.Context, email string) (*sheets.Service, error) {
	slog.Debug("creating Connected Sheets writer service", "email", email)

	svc, err := newGoogleServiceForScopes(
		ctx,
		email,
		string(googleauth.ServiceSheets),
		"Connected Sheets writer",
		[]string{scopeSpreadsheets, scopeBigQueryReadOnly},
		sheets.NewService,
	)
	if err != nil {
		slog.Error("failed to create Connected Sheets writer service", "email", email, "error", err)
		return nil, err
	}

	slog.Debug("Connected Sheets writer service created successfully", "email", email)

	return svc, nil
}
