package main

import (
	"context"
	"fmt"
	"time"

	driveapi "google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

// drive.go — Drive API v3 modifiedTime probe.
//
// Single-purpose: cheap "did the sheet change since last sync?" check.
// One GET per cycle, costs ~0 against the 1000-requests/100-seconds quota.
// When the timestamp matches the last persisted run's drive_modified_time,
// the orchestrator short-circuits without doing the heavy values-batchGet.
//
// IMPORTANT: Drive API must be enabled in the SA's GCP project. Probed
// on 2026-05-05 — currently 403s with 'API has not been used in project'.
// Operator must enable it once before the cron starts producing accurate
// modifiedTime gating. Until then, ProbeModifiedTime returns a zero time
// and the orchestrator falls back to "always fetch", which still works
// (just costs an extra batchGet per cycle).

// ProbeModifiedTime returns the spreadsheet's modifiedTime as RFC3339.
// On any error (Drive API not enabled, network blip, etc.) returns zero
// time + the error so the orchestrator can decide between hard-fail
// (development) and graceful-degrade (production).
func ProbeModifiedTime(ctx context.Context, credsPath, fileID string) (time.Time, error) {
	svc, err := driveapi.NewService(ctx,
		option.WithCredentialsFile(credsPath),
		option.WithScopes(driveapi.DriveMetadataReadonlyScope),
	)
	if err != nil {
		return time.Time{}, fmt.Errorf("drive: new service: %w", err)
	}

	f, err := svc.Files.Get(fileID).Fields("id,name,modifiedTime").Context(ctx).Do()
	if err != nil {
		return time.Time{}, fmt.Errorf("drive: get file: %w", err)
	}
	if f.ModifiedTime == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, f.ModifiedTime)
	if err != nil {
		return time.Time{}, fmt.Errorf("drive: parse modifiedTime %q: %w", f.ModifiedTime, err)
	}
	return t, nil
}
