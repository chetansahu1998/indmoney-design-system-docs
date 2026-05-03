package projects

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
)

// canonical_tree.go — T8 (audit follow-up plan 2026-05-03-001).
//
// Helpers for the dual-column canonical_tree storage. Pre-T8 the
// dereferenced Figma node JSON was stored as plain TEXT in
// screen_canonical_trees.canonical_tree — 95+ MB on Fly's volume
// for content nobody reads at runtime via the frontend. T8 adds
// canonical_tree_gz BLOB; new rows write gzipped + empty legacy
// column, the read-side helper ResolveCanonicalTree picks whichever
// is populated.
//
// gzipMaxRead bounds the decompressor so a malformed/malicious gz
// payload can't stream gigabytes into memory. 64 MB is well above any
// realistic Figma node tree (the local-DB max is ~600 KB pre-compress).

const gzipMaxRead = 64 * 1024 * 1024

// CompressTree gzips a UTF-8 JSON canonical tree for storage in
// screen_canonical_trees.canonical_tree_gz.
func CompressTree(raw string) ([]byte, error) {
	if raw == "" {
		return nil, nil
	}
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write([]byte(raw)); err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("gzip write: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("gzip close: %w", err)
	}
	return buf.Bytes(), nil
}

// DecompressTree is the inverse of CompressTree. Returns "" on a nil/empty
// input so callers can treat "no compressed value" as "fall back to legacy
// plain-text column".
func DecompressTree(blob []byte) (string, error) {
	if len(blob) == 0 {
		return "", nil
	}
	r, err := gzip.NewReader(bytes.NewReader(blob))
	if err != nil {
		return "", fmt.Errorf("gzip reader: %w", err)
	}
	defer r.Close()
	out, err := io.ReadAll(io.LimitReader(r, gzipMaxRead+1))
	if err != nil {
		return "", fmt.Errorf("gzip read: %w", err)
	}
	if len(out) > gzipMaxRead {
		return "", errors.New("gzip: payload exceeds 64 MB cap")
	}
	return string(out), nil
}

// ResolveCanonicalTree picks the populated representation. SELECT both
// columns from screen_canonical_trees and pass them through this helper.
// During the T8 transition window, rows from before the backfill have
// `legacy` populated and `gz` nil; backfilled + new rows have `gz`
// populated and `legacy` empty.
func ResolveCanonicalTree(legacy string, gz []byte) (string, error) {
	if len(gz) > 0 {
		return DecompressTree(gz)
	}
	return legacy, nil
}
