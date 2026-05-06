// One-shot recovery tool: re-seed the INDmoney tenant + designer roster
// + per-tenant Figma PAT into a freshly-migrated ds.db. Idempotent.
//
// Usage:
//
//	source .env.local
//	go run ./cmd/bootstrap-tenant --db services/ds-service/data/ds.db
//
// What it inserts (all INSERT OR IGNORE, safe to re-run):
//   - tenants(id=e090530f-…, slug=indmoney, created_by=system user)
//   - users(id=<sha256-derived>, email=<roster>) for the 9 designers
//     baked into cmd/mint-tokens (kept in sync — same UUID derivation)
//   - tenant_users(tenant_id, user_id, role=tenant_admin) for each
//   - figma_tokens(tenant_id, encrypted_token=AES(FIGMA_PAT))
package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/auth"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/db"
)

const (
	tenantID   = "e090530f-2698-489d-934a-c821cb925c8a"
	tenantSlug = "indmoney"
	tenantName = "INDmoney"
)

type designer struct{ name, email string }

// Mirror of cmd/mint-tokens roster — keep in sync.
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
}

func deterministicUUID(email string) string {
	h := sha256.Sum256([]byte("indmoney-ds-service:user:" + email))
	hx := hex.EncodeToString(h[:16])
	return fmt.Sprintf("%s-%s-%s-%s-%s", hx[0:8], hx[8:12], hx[12:16], hx[16:20], hx[20:32])
}

func main() {
	dbPath := flag.String("db", "services/ds-service/data/ds.db", "Path to ds.db")
	flag.Parse()

	pat := os.Getenv("FIGMA_PAT")
	if pat == "" {
		fmt.Fprintln(os.Stderr, "FIGMA_PAT not set")
		os.Exit(1)
	}
	encB64 := os.Getenv("ENCRYPTION_KEY")
	if encB64 == "" {
		fmt.Fprintln(os.Stderr, "ENCRYPTION_KEY not set")
		os.Exit(1)
	}
	encKey, err := auth.LoadEncryptionKey(encB64)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load encryption key: %v\n", err)
		os.Exit(1)
	}

	conn, err := sql.Open("sqlite3", *dbPath+"?_foreign_keys=on")
	if err != nil {
		fmt.Fprintf(os.Stderr, "open db: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339)

	// Resolve system user ID for tenant.created_by FK.
	var systemUserID string
	if err := conn.QueryRowContext(ctx,
		`SELECT id FROM users WHERE email = 'system@indmoney.local'`).Scan(&systemUserID); err != nil {
		fmt.Fprintf(os.Stderr, "lookup system user: %v\n", err)
		os.Exit(1)
	}

	// 1. Tenant.
	if _, err := conn.ExecContext(ctx,
		`INSERT OR IGNORE INTO tenants (id, slug, name, status, plan_type, created_at, created_by)
		 VALUES (?, ?, ?, 'active', 'free', ?, ?)`,
		tenantID, tenantSlug, tenantName, now, systemUserID); err != nil {
		fmt.Fprintf(os.Stderr, "insert tenant: %v\n", err)
		os.Exit(1)
	}

	// 2. Users + tenant_users membership.
	for _, d := range roster {
		uid := deterministicUUID(d.email)
		if _, err := conn.ExecContext(ctx,
			`INSERT OR IGNORE INTO users (id, email, password_hash, role, created_at)
			 VALUES (?, ?, '', 'user', ?)`,
			uid, d.email, now); err != nil {
			fmt.Fprintf(os.Stderr, "insert user %s: %v\n", d.email, err)
			os.Exit(1)
		}
		if _, err := conn.ExecContext(ctx,
			`INSERT OR IGNORE INTO tenant_users (tenant_id, user_id, role, status, created_at)
			 VALUES (?, ?, 'tenant_admin', 'active', ?)`,
			tenantID, uid, now); err != nil {
			fmt.Fprintf(os.Stderr, "insert tenant_users %s: %v\n", d.email, err)
			os.Exit(1)
		}
		fmt.Printf("user %s = %s\n", d.email, uid)
	}

	// 3. Figma PAT (encrypted at rest).
	enc, err := encKey.Encrypt([]byte(pat))
	if err != nil {
		fmt.Fprintf(os.Stderr, "encrypt PAT: %v\n", err)
		os.Exit(1)
	}
	dbHandle := &db.DB{DB: conn}
	if err := dbHandle.UpsertFigmaToken(ctx, db.FigmaTokenRecord{
		TenantID:        tenantID,
		EncryptedToken:  enc,
		KeyVersion:      1,
		FigmaUserEmail:  "garvdeep@indmoney.com",
		FigmaUserHandle: "garvdeep",
		CreatedAt:       time.Now().UTC(),
	}); err != nil {
		fmt.Fprintf(os.Stderr, "upsert figma_token: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\nbootstrap complete: tenant=%s users=%d figma_token=encrypted\n",
		tenantID, len(roster))
}
