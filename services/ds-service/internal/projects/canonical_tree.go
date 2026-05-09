package projects

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/klauspost/compress/zstd"
)

// canonical_tree.go — dual-+-now-triple-column canonical_tree storage.
//
// Compression evolution:
//
//   • Pre-T8: TEXT in screen_canonical_trees.canonical_tree.
//     95+ MB on Fly per tenant for content the frontend never reads.
//
//   • T8 (migration 0016): added canonical_tree_gz BLOB. gzip
//     BestCompression on JSON measured at 6× ratio (1.17 GB on the
//     5,647-tree corpus). cmd/compress-trees backfills + nulls the
//     legacy column row-by-row.
//
//   • Phase 1 (migration 0022): added canonical_tree_zstd BLOB. zstd
//     L19 measured at 9× ratio (765.8 MB on the same corpus) — 36.1%
//     smaller than gzip with 3.2× faster decompress (1.57 ms p50 →
//     0.49 ms p50). cmd/zstd-bench captured the numbers; cmd/compress-
//     trees --to=zstd backfills.
//
// Read path: ResolveCanonicalTree(legacy, gz, zstd) picks the first
// non-empty representation in priority order zstd > gz > legacy and
// decompresses if needed. Every read site SELECTs all three columns
// and passes them through; the helper hides the compression choice.
//
// Safety: the decompressors cap output at decompressMaxRead bytes so a
// malformed or malicious payload can't stream gigabytes into RAM.
// 64 MB is well above the largest realistic Figma node tree (the
// production max is ~106 MB raw / 8.3 MB gzipped; the cap covers a
// generous future buffer without becoming a DoS vector).

const decompressMaxRead = 64 * 1024 * 1024

// gzipMaxRead is preserved as an alias for the historical name so any
// out-of-tree caller (or future refactor) referring to it still compiles.
const gzipMaxRead = decompressMaxRead

// CompressTree gzips a UTF-8 JSON canonical tree for storage in
// screen_canonical_trees.canonical_tree_gz. Kept for backward compat with
// callers that still write to the gz column (e.g., the compress-trees
// CLI's legacy mode); new code should prefer CompressTreeZstd.
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
// input so callers can treat "no compressed value" as "fall back to other
// columns".
func DecompressTree(blob []byte) (string, error) {
	if len(blob) == 0 {
		return "", nil
	}
	r, err := gzip.NewReader(bytes.NewReader(blob))
	if err != nil {
		return "", fmt.Errorf("gzip reader: %w", err)
	}
	defer r.Close()
	out, err := io.ReadAll(io.LimitReader(r, decompressMaxRead+1))
	if err != nil {
		return "", fmt.Errorf("gzip read: %w", err)
	}
	if len(out) > decompressMaxRead {
		return "", errors.New("gzip: payload exceeds 64 MB cap")
	}
	return string(out), nil
}

// ─── zstd path ──────────────────────────────────────────────────────────────

// zstdEncoder + zstdDecoder are package-level singletons with stateless
// EncodeAll / DecodeAll APIs. The klauspost/compress/zstd encoder is safe
// for concurrent EncodeAll calls and reuses internal buffers across
// invocations, so a single instance avoids the goroutine-allocation churn
// of building per-call writers.
//
// Lazily initialised behind a sync.Once so cmd/* binaries that don't read
// canonical_trees at all (e.g., admin / drive) don't pay for the encoder.
var (
	zstdInitOnce sync.Once
	zstdEncoder  *zstd.Encoder
	zstdDecoder  *zstd.Decoder
	zstdInitErr  error
)

func zstdInit() {
	zstdInitOnce.Do(func() {
		// Level 19 (SpeedBestCompression). Measured at 36.1% smaller than
		// gzip BestCompression on the 5,647-tree corpus while staying under
		// 100 ms p95 compress on the same M-series host. Higher levels
		// (SpeedFastest etc) trade size for speed; we have plenty of
		// compress headroom because writes happen at pipeline time, not
		// in the canvas hot path.
		enc, err := zstd.NewWriter(nil,
			zstd.WithEncoderLevel(zstd.SpeedBestCompression),
			zstd.WithEncoderConcurrency(1),
		)
		if err != nil {
			zstdInitErr = fmt.Errorf("zstd encoder: %w", err)
			return
		}
		dec, err := zstd.NewReader(nil,
			zstd.WithDecoderConcurrency(1),
			// Ceiling matches the legacy gzip path. EncodeAll/DecodeAll
			// don't honor io.LimitReader, so we enforce the bound after
			// DecodeAll returns and reject oversized payloads.
			zstd.WithDecoderMaxMemory(decompressMaxRead),
		)
		if err != nil {
			_ = enc.Close()
			zstdInitErr = fmt.Errorf("zstd decoder: %w", err)
			return
		}
		zstdEncoder = enc
		zstdDecoder = dec
	})
}

// CompressTreeZstd encodes a UTF-8 JSON canonical tree to a zstd blob for
// storage in screen_canonical_trees.canonical_tree_zstd. Returns nil for an
// empty input so callers can write a SQL NULL for "no tree".
func CompressTreeZstd(raw string) ([]byte, error) {
	if raw == "" {
		return nil, nil
	}
	zstdInit()
	if zstdInitErr != nil {
		return nil, zstdInitErr
	}
	return zstdEncoder.EncodeAll([]byte(raw), nil), nil
}

// DecompressTreeZstd is the inverse of CompressTreeZstd. Empty/nil input →
// "" with nil error so the caller can fall back to the gz column.
func DecompressTreeZstd(blob []byte) (string, error) {
	if len(blob) == 0 {
		return "", nil
	}
	zstdInit()
	if zstdInitErr != nil {
		return "", zstdInitErr
	}
	out, err := zstdDecoder.DecodeAll(blob, nil)
	if err != nil {
		return "", fmt.Errorf("zstd decode: %w", err)
	}
	if len(out) > decompressMaxRead {
		return "", errors.New("zstd: payload exceeds 64 MB cap")
	}
	return string(out), nil
}

// ─── Resolve ────────────────────────────────────────────────────────────────

// ResolveCanonicalTree picks the populated representation in priority
// order zstd > gz > legacy and decompresses if needed.
//
// During the migration window rows are in three states:
//
//   • backfilled to zstd (Phase 1 target): zstd populated, others may
//     be NULL or stale — zstd wins.
//   • backfilled to gzip only (T8 era): gz populated, legacy NULL.
//   • pre-T8: legacy populated, gz + zstd both NULL.
//
// Every call site SELECTs all three columns and passes them straight
// through. Once cmd/compress-trees --to=zstd reports zero un-converted
// rows, a follow-up migration drops both legacy columns.
func ResolveCanonicalTree(legacy string, gz, zstdBlob []byte) (string, error) {
	if len(zstdBlob) > 0 {
		return DecompressTreeZstd(zstdBlob)
	}
	if len(gz) > 0 {
		return DecompressTree(gz)
	}
	return legacy, nil
}
