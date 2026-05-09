// zstd-bench — one-shot measurement tool for canonical_tree compression.
//
// Samples N rows from screen_canonical_trees, decompresses each (whichever
// column is populated), then re-compresses with three strategies:
//   1. gzip (baseline; matches today's storage)
//   2. zstd level 19 (no dict; expected 15-25% better than gzip)
//   3. zstd level 19 + trained dict (expected 40-60% better than gzip)
//
// Output: per-strategy total bytes, ratio vs raw, ratio vs gzip baseline,
// and per-row compress + decompress wall-clock so we can confirm decompress
// stays under the canvas-render budget.
//
// This is a benchmark / decision aid, NOT a production tool. Drop after
// the migration ships.

package main

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	mrand "math/rand"
	"os"
	"sort"
	"time"

	"github.com/klauspost/compress/zstd"
	_ "github.com/mattn/go-sqlite3"
)

func main() {
	dbPath := flag.String("db", "data/ds.db", "path to ds.db")
	sampleN := flag.Int("n", 100, "number of trees to sample")
	dictSize := flag.Int("dict-size", 64*1024, "zstd dictionary target size in bytes")
	flag.Parse()

	db, err := sql.Open("sqlite3", *dbPath)
	if err != nil {
		log.Fatalf("open: %v", err)
	}
	defer db.Close()

	trees, err := sampleTrees(db, *sampleN)
	if err != nil {
		log.Fatalf("sample: %v", err)
	}
	if len(trees) == 0 {
		log.Fatal("no trees in screen_canonical_trees — run sheets-sync first")
	}
	fmt.Printf("Sampled %d trees, total raw bytes: %s\n",
		len(trees), human(sumRaw(trees)))

	gzipR := benchGzip(trees)
	zstdR := benchZstd(trees, nil)

	// Train a dict on the FIRST half of the sample so the SECOND half
	// measures apples-to-apples (dict shouldn't include test data).
	half := len(trees) / 2
	if half < 10 {
		half = len(trees)
	}
	trainSet := make([][]byte, half)
	for i, t := range trees[:half] {
		trainSet[i] = t.raw
	}
	dict, derr := zstd.BuildDict(zstd.BuildDictOptions{
		ID:       1,
		Contents: trainSet,
		Level:    zstd.SpeedBestCompression,
	})
	if derr != nil {
		fmt.Printf("dict training failed: %v (continuing without)\n", derr)
		printResults(len(trees), sumRaw(trees), gzipR, zstdR, nil)
		return
	}
	if len(dict) > *dictSize {
		fmt.Printf("dict trained at %s, capping to %s\n",
			human(int64(len(dict))), human(int64(*dictSize)))
		dict = dict[:*dictSize]
	} else {
		fmt.Printf("dict trained at %s (cap %s)\n",
			human(int64(len(dict))), human(int64(*dictSize)))
	}

	zstdDictR := benchZstd(trees, dict)
	printResults(len(trees), sumRaw(trees), gzipR, zstdR, &zstdDictR)
}

type sample struct {
	id  string
	raw []byte
}

func sumRaw(ss []sample) int64 {
	var s int64
	for _, x := range ss {
		s += int64(len(x.raw))
	}
	return s
}

func sampleTrees(db *sql.DB, n int) ([]sample, error) {
	rows, err := db.Query(
		`SELECT screen_id, canonical_tree, canonical_tree_gz
		   FROM screen_canonical_trees
		  WHERE canonical_tree_gz IS NOT NULL OR canonical_tree IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var all []sample
	for rows.Next() {
		var id string
		var legacy sql.NullString
		var gz []byte
		if err := rows.Scan(&id, &legacy, &gz); err != nil {
			return nil, err
		}
		raw, derr := decode(legacy.String, gz)
		if derr != nil || len(raw) == 0 {
			continue
		}
		all = append(all, sample{id: id, raw: raw})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if n >= len(all) {
		return all, nil
	}
	// Reservoir sample for stable randomness.
	mrand.Seed(seed())
	mrand.Shuffle(len(all), func(i, j int) { all[i], all[j] = all[j], all[i] })
	return all[:n], nil
}

func seed() int64 {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return int64(b[0])<<56 | int64(b[1])<<48 | int64(b[2])<<40 | int64(b[3])<<32 |
		int64(b[4])<<24 | int64(b[5])<<16 | int64(b[6])<<8 | int64(b[7])
}

func decode(legacy string, gz []byte) ([]byte, error) {
	if len(gz) > 0 {
		return gunzip(gz)
	}
	if legacy != "" {
		return []byte(legacy), nil
	}
	return nil, nil
}

func gunzip(gz []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

type result struct {
	strategy        string
	totalCompressed int64
	compressP50Ms   float64
	compressP95Ms   float64
	decompressP50Ms float64
	decompressP95Ms float64
}

func benchGzip(trees []sample) result {
	var total int64
	var compMs, decompMs []float64
	for _, t := range trees {
		var buf bytes.Buffer
		t0 := time.Now()
		w, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		_, _ = w.Write(t.raw)
		_ = w.Close()
		compMs = append(compMs, msSince(t0))
		total += int64(buf.Len())

		t1 := time.Now()
		_, _ = gunzip(buf.Bytes())
		decompMs = append(decompMs, msSince(t1))
	}
	sort.Float64s(compMs)
	sort.Float64s(decompMs)
	return result{
		strategy:        "gzip BestCompression",
		totalCompressed: total,
		compressP50Ms:   pct(compMs, 50),
		compressP95Ms:   pct(compMs, 95),
		decompressP50Ms: pct(decompMs, 50),
		decompressP95Ms: pct(decompMs, 95),
	}
}

func benchZstd(trees []sample, dict []byte) result {
	encOpts := []zstd.EOption{zstd.WithEncoderLevel(zstd.SpeedBestCompression)}
	if len(dict) > 0 {
		encOpts = append(encOpts, zstd.WithEncoderDict(dict))
	}
	enc, err := zstd.NewWriter(nil, encOpts...)
	if err != nil {
		log.Fatalf("zstd writer: %v", err)
	}
	defer enc.Close()
	decOpts := []zstd.DOption{}
	if len(dict) > 0 {
		decOpts = append(decOpts, zstd.WithDecoderDicts(dict))
	}
	dec, err := zstd.NewReader(nil, decOpts...)
	if err != nil {
		log.Fatalf("zstd reader: %v", err)
	}
	defer dec.Close()

	var total int64
	var compMs, decompMs []float64
	for _, t := range trees {
		t0 := time.Now()
		out := enc.EncodeAll(t.raw, nil)
		compMs = append(compMs, msSince(t0))
		total += int64(len(out))

		t1 := time.Now()
		_, derr := dec.DecodeAll(out, nil)
		if derr != nil {
			log.Fatalf("decode mismatch on %s: %v", t.id, derr)
		}
		decompMs = append(decompMs, msSince(t1))
	}
	label := "zstd L19 (no dict)"
	if len(dict) > 0 {
		label = fmt.Sprintf("zstd L19 + %s dict", human(int64(len(dict))))
	}
	sort.Float64s(compMs)
	sort.Float64s(decompMs)
	return result{
		strategy:        label,
		totalCompressed: total,
		compressP50Ms:   pct(compMs, 50),
		compressP95Ms:   pct(compMs, 95),
		decompressP50Ms: pct(decompMs, 50),
		decompressP95Ms: pct(decompMs, 95),
	}
}

func printResults(n int, raw int64, gzipR, zstdR result, zstdDictR *result) {
	fmt.Println()
	fmt.Println("Compression results")
	fmt.Println("───────────────────")
	fmt.Printf("%-32s %12s %8s %8s %12s %12s\n",
		"strategy", "total", "vs raw", "vs gzip", "comp p50/p95", "decomp p50/p95")

	row := func(r result) {
		fmt.Printf("%-32s %12s %7.2f%% %7.2f%% %5.1f/%5.1fms %5.2f/%5.2fms\n",
			r.strategy,
			human(r.totalCompressed),
			float64(r.totalCompressed)*100/float64(raw),
			float64(r.totalCompressed)*100/float64(gzipR.totalCompressed),
			r.compressP50Ms, r.compressP95Ms,
			r.decompressP50Ms, r.decompressP95Ms,
		)
	}
	row(gzipR)
	row(zstdR)
	if zstdDictR != nil {
		row(*zstdDictR)
	}
	fmt.Println()
	fmt.Printf("Sample: %d trees, raw total %s\n", n, human(raw))
	fmt.Printf("Projected DB savings vs current gzip:\n")
	saveZ := gzipR.totalCompressed - zstdR.totalCompressed
	fmt.Printf("  zstd alone:       %s saved (%.1f%%)\n",
		human(saveZ), float64(saveZ)*100/float64(gzipR.totalCompressed))
	if zstdDictR != nil {
		saveD := gzipR.totalCompressed - zstdDictR.totalCompressed
		fmt.Printf("  zstd + dict:      %s saved (%.1f%%)\n",
			human(saveD), float64(saveD)*100/float64(gzipR.totalCompressed))
	}
}

// ─── tiny utils ─────────────────────────────────────────────────────────────

func human(b int64) string {
	if b < 1024 {
		return fmt.Sprintf("%d B", b)
	}
	if b < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(b)/1024)
	}
	if b < 1024*1024*1024 {
		return fmt.Sprintf("%.1f MB", float64(b)/(1024*1024))
	}
	return fmt.Sprintf("%.2f GB", float64(b)/(1024*1024*1024))
}

func pct(sorted []float64, p int) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Round(float64(p)*float64(len(sorted)-1)/100.0))
	return sorted[idx]
}

func msSince(t time.Time) float64 {
	return float64(time.Since(t).Microseconds()) / 1000.0
}

func init() {
	log.SetOutput(os.Stderr)
}
