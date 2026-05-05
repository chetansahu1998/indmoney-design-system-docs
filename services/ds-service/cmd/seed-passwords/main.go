// Command seed-passwords — provisions bcrypt password hashes for the designer
// roster so the prod /login flow has accounts to authenticate against.
//
// The mint-tokens command (sibling under cmd/mint-tokens) issues long-lived
// JWTs without ever touching the users table. That worked when designers
// pasted tokens into localStorage, but the user-facing /login page expects
// rows in `users` with a real `password_hash`. seed-passwords fills the gap:
//
//  1. UPSERT a `users` row per roster entry (id derived deterministically
//     from email so re-runs are idempotent — same id as mint-tokens).
//  2. Generate a cryptographically random 16-character password per user.
//  3. bcrypt-hash with cost 12 (matches internal/auth.HashPassword) and store.
//  4. UPSERT the tenant_users link so the user resolves to the INDmoney tenant.
//  5. Print a paste-ready table of email + plaintext password to stdout
//     ONCE — the operator hands each row to the matching designer via Slack /
//     1Password and does not save the table.
//
// Re-running rotates passwords (each invocation picks fresh randoms). Use
// --emails=alice@…,bob@… to scope to a subset, or --keep-existing to skip
// users who already have a non-empty password_hash.
//
// Usage:
//
//	source .env.local
//	go run ./cmd/seed-passwords --tenant=<uuid>
//	go run ./cmd/seed-passwords --emails=ashish@indmoney.com
//	go run ./cmd/seed-passwords --keep-existing
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"flag"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/auth"
)

const (
	defaultTenantID = "e090530f-2698-489d-934a-c821cb925c8a"
	defaultDBPath   = "services/ds-service/data/ds.db"
	pwLength        = 16
	pwAlphabet      = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789!@#$%^&*"
)

type designer struct {
	Name  string
	Email string
}

// Roster mirrors cmd/mint-tokens. Keep alphabetical by first name.
var roster = []designer{
	{"Ashish Kashyap", "ashish@indmoney.com"},
	{"Dhairya Shah", "dhairya@indmoney.com"},
	{"Garvdeep Singh", "garvdeep@indmoney.com"},
	{"Laxmi Mishra", "laxmi@indmoney.com"},
	{"Omeshwari Dharpure", "omeshwari@indmoney.com"},
	{"Sahaj Tyagi", "sahaj@indmoney.com"},
	{"Saksham Jamwal", "saksham@indmoney.com"},
	{"Shivam Kumar", "shivam@indmoney.com"},
	{"Vikash Thakur", "vikash@indmoney.com"},
	{"Chetan Sahu", "chetan@indmoney.com"},
}

func main() {
	dbPath := flag.String("db", getenv("DS_DB_PATH", defaultDBPath), "Path to ds.db (env DS_DB_PATH)")
	tenantID := flag.String("tenant", defaultTenantID, "tenant_id to UPSERT tenant_users link against")
	emails := flag.String("emails", "", "Comma-separated subset of roster emails to seed (default: all)")
	keepExisting := flag.Bool("keep-existing", false, "Skip users who already have a non-empty password_hash")
	flag.Parse()

	subset := map[string]bool{}
	if *emails != "" {
		for _, e := range strings.Split(*emails, ",") {
			subset[strings.TrimSpace(strings.ToLower(e))] = true
		}
	}

	db, err := sql.Open("sqlite3", *dbPath)
	if err != nil {
		die("open db: %v", err)
	}
	defer db.Close()
	if err := db.PingContext(context.Background()); err != nil {
		die("ping db: %v", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	type row struct {
		Name, Email, Password string
		Skipped, Reason       string
	}
	out := make([]row, 0, len(roster))

	for _, d := range roster {
		if len(subset) > 0 && !subset[strings.ToLower(d.Email)] {
			continue
		}

		userID := deterministicUUID(d.Email)
		var existingHash sql.NullString
		_ = db.QueryRowContext(context.Background(),
			`SELECT password_hash FROM users WHERE id = ?`, userID,
		).Scan(&existingHash)

		if *keepExisting && existingHash.Valid && existingHash.String != "" && existingHash.String != "noop" {
			out = append(out, row{Name: d.Name, Email: d.Email, Skipped: "yes", Reason: "password already set"})
			continue
		}

		pw, err := generatePassword(pwLength)
		if err != nil {
			die("rand: %v", err)
		}
		hash, err := auth.HashPassword(pw)
		if err != nil {
			die("hash: %v", err)
		}

		// UPSERT user. Schema: id PK, email UNIQUE, password_hash, role, created_at, last_login_at.
		_, err = db.ExecContext(context.Background(), `
			INSERT INTO users (id, email, password_hash, role, created_at)
			VALUES (?, ?, ?, 'user', ?)
			ON CONFLICT(id) DO UPDATE SET password_hash = excluded.password_hash
		`, userID, d.Email, hash, now)
		if err != nil {
			die("upsert user %s: %v", d.Email, err)
		}

		// UPSERT tenant_users link.
		_, err = db.ExecContext(context.Background(), `
			INSERT INTO tenant_users (tenant_id, user_id, role, status, created_at)
			VALUES (?, ?, 'designer', 'active', ?)
			ON CONFLICT(tenant_id, user_id) DO NOTHING
		`, *tenantID, userID, now)
		if err != nil {
			die("upsert tenant_users %s: %v", d.Email, err)
		}

		out = append(out, row{Name: d.Name, Email: d.Email, Password: pw})
	}

	// Print the credentials table ONCE. Operator hands each row out via Slack DM /
	// 1Password share. Stdout — never written to disk.
	fmt.Println("# Designer credentials — share each row with the matching designer over a private channel.")
	fmt.Println("# DO NOT commit this output. Re-run --keep-existing to skip users you've already onboarded.")
	fmt.Println()
	fmt.Printf("%-25s %-30s %s\n", "Name", "Email", "Password")
	fmt.Println(strings.Repeat("-", 80))
	for _, r := range out {
		if r.Skipped != "" {
			fmt.Printf("%-25s %-30s %s (skipped: %s)\n", r.Name, r.Email, "—", r.Reason)
			continue
		}
		fmt.Printf("%-25s %-30s %s\n", r.Name, r.Email, r.Password)
	}
}

// deterministicUUID derives a stable UUIDv5-ish hex string from the email so a
// re-run for the same designer produces the same `users.id`. Mirrors
// cmd/mint-tokens so JWT subjects line up with rows in the users table.
func deterministicUUID(email string) string {
	h := sha256.Sum256([]byte("indmoney-ds-service:user:" + email))
	hexStr := hex.EncodeToString(h[:16])
	return fmt.Sprintf("%s-%s-%s-%s-%s", hexStr[0:8], hexStr[8:12], hexStr[12:16], hexStr[16:20], hexStr[20:32])
}

// generatePassword draws `n` characters from `pwAlphabet` using crypto/rand.
// Alphabet excludes ambiguous chars (0/O, 1/l/I).
func generatePassword(n int) (string, error) {
	out := make([]byte, n)
	max := big.NewInt(int64(len(pwAlphabet)))
	for i := 0; i < n; i++ {
		j, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		out[i] = pwAlphabet[j.Int64()]
	}
	return string(out), nil
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func die(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(1)
}
