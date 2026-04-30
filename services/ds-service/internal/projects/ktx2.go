// Package projects — Phase 3.5 U2 — KTX2 / Basis Universal transcoding.
//
// After the pipeline writes a PNG sidecar at
// data/screens/<tenant>/<version>/<screen>@2x.png, this module forks
// `basisu` to emit a parallel `.ktx2` file at the same path with the
// `.ktx2` suffix. The frontend's atlas loader picks `.ktx2` first; on
// 404 it falls back to `.png` (matching Phase 1's PNG path).
//
// Failure mode: when the basisu CLI isn't on PATH, the transcode logs a
// warning and returns nil (no error bubbled to the pipeline). The PNG
// is intact; the atlas continues serving it. Set `DS_BASIS_CLI_PATH`
// env to override the binary location (default: `basisu`).
//
// Performance: typical 1024×800 PNG transcodes in 50-150ms on M1.
// We run it inline inside persistPNG; this adds ~150ms to the per-
// screen pipeline budget. Phase 1's overall pipeline budget (≤15s p95
// for fast preview) absorbs this — even at 30 screens the transcode
// total is ≤4.5s. If profiling shows it dominating, future work can
// move the transcode to a goroutine that runs after SetScreenPNG and
// updates a separate ktx2_storage_key column.

package projects

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// KTX2BinaryEnvVar is the env var that overrides the basisu CLI path.
const KTX2BinaryEnvVar = "DS_BASIS_CLI_PATH"

// DefaultKTX2Binary is the lookup name when DS_BASIS_CLI_PATH isn't set.
// Resolves via $PATH like any other CLI.
const DefaultKTX2Binary = "basisu"

// KTX2Transcoder forks basisu to produce a .ktx2 sidecar next to a PNG.
// All methods are safe to call when basisu isn't installed — they
// short-circuit gracefully + log once at boot.
type KTX2Transcoder struct {
	// BinaryPath resolves to the basisu CLI. Configured at New().
	BinaryPath string
	// Available is true when the binary was found on PATH at New().
	// Subsequent Transcode() calls short-circuit when false.
	Available bool
	// Log surfaces a one-time warning at boot when basisu is missing
	// + per-call errors during transcode.
	Log *slog.Logger
	// Timeout caps each transcode call. Default 10s.
	Timeout time.Duration
}

// NewKTX2Transcoder probes the PATH for basisu (or DS_BASIS_CLI_PATH
// override) and returns a configured transcoder. Always returns a
// non-nil transcoder — callers don't need to nil-check; they just see
// `Available=false` and treat each Transcode() call as a no-op.
func NewKTX2Transcoder(log *slog.Logger) *KTX2Transcoder {
	bin := os.Getenv(KTX2BinaryEnvVar)
	if bin == "" {
		bin = DefaultKTX2Binary
	}
	resolved, err := exec.LookPath(bin)
	if err != nil {
		if log != nil {
			log.Warn("ktx2: basisu CLI not found; KTX2 transcoding disabled",
				"binary", bin, "err", err.Error(),
				"hint", "install basisu (brew install basis_universal on macOS)")
		}
		return &KTX2Transcoder{
			BinaryPath: bin,
			Available:  false,
			Log:        log,
			Timeout:    10 * time.Second,
		}
	}
	if log != nil {
		log.Info("ktx2: basisu CLI ready", "binary", resolved)
	}
	return &KTX2Transcoder{
		BinaryPath: resolved,
		Available:  true,
		Log:        log,
		Timeout:    10 * time.Second,
	}
}

// Transcode reads pngPath, forks basisu to emit a sibling .ktx2 file,
// and returns nil. When basisu is unavailable, returns nil immediately
// (the PNG is the source of truth — KTX2 is an optimization).
//
// Failures during the transcode itself are logged + returned. The
// caller in persistPNG ignores the error and lets the PNG flow through;
// the .ktx2 sidecar simply doesn't materialize and the frontend falls
// back to the PNG URL.
func (t *KTX2Transcoder) Transcode(ctx context.Context, pngPath string) error {
	if !t.Available {
		return nil
	}
	if pngPath == "" {
		return errors.New("ktx2: empty png_path")
	}
	// Output sibling: <name>@2x.png → <name>@2x.ktx2. We let basisu
	// decide the actual output name via -output_path so we don't have
	// to script the suffix swap.
	outDir := filepath.Dir(pngPath)
	stem := filepath.Base(pngPath)
	stem = stem[:len(stem)-len(filepath.Ext(stem))] // strip .png
	outPath := filepath.Join(outDir, stem+".ktx2")

	cctx, cancel := context.WithTimeout(ctx, t.Timeout)
	defer cancel()

	// basisu flags:
	//   -ktx2          → KTX2 container (default is .basis)
	//   -no_multifile  → single-file output
	//   -mipmap        → generate mipmap chain (drei KTX2Loader picks
	//                    the right LOD based on world-space size).
	//   -y_flip        → match TextureLoader's UV space — three.js
	//                    expects flipped Y when loading a PNG; KTX2
	//                    doesn't apply that flip by default.
	//   -file <png>    → input
	//   -output_path <dir> → output dir (basisu names the output by
	//                       replacing the input extension, so we end
	//                       up with stem.ktx2 in outDir).
	cmd := exec.CommandContext(cctx,
		t.BinaryPath,
		"-ktx2",
		"-no_multifile",
		"-mipmap",
		"-y_flip",
		"-file", pngPath,
		"-output_path", outDir,
	)
	stderr, err := cmd.CombinedOutput()
	if err != nil {
		if t.Log != nil {
			t.Log.Warn("ktx2: transcode failed",
				"png", pngPath,
				"err", err.Error(),
				"stderr", string(stderr))
		}
		return fmt.Errorf("ktx2 transcode: %w", err)
	}
	// basisu writes to <stem>.ktx2 by default — sanity-check the output.
	if _, err := os.Stat(outPath); err != nil {
		return fmt.Errorf("ktx2 expected output missing at %s: %w", outPath, err)
	}
	return nil
}
