// Command mint-tokens issues long-lived ds-service JWTs for a hard-coded
// roster of designers. Output is a paste-ready table the operator hands to
// each designer; they drop the token into the Figma plugin's Settings →
// Bearer JWT field and into the browser's localStorage entry
// `indmoney-ds-auth`.
//
// JWTs validate purely on signature — no user/tenant_user rows required.
// The signing key must be the same one configured on the Fly ds-service
// + audit-server apps. Pull from .env.local before running:
//
//	set -a && source .env.local && set +a && \
//	go run ./services/ds-service/cmd/mint-tokens
//
// Tokens default to 365-day lifetime; override with --days. Tenant defaults
// to the indmoney tenant; override with --tenant.

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/auth"
)

const defaultTenantID = "e090530f-2698-489d-934a-c821cb925c8a"

type designer struct {
	name  string
	email string
}

// Roster — keep alphabetical by first name. Add or remove rows here and
// re-run; the script is idempotent (deterministic user_id from email so a
// re-run for the same name produces the same JWT subject).
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

func main() {
	days := flag.Int("days", 365, "token lifetime in days")
	tenant := flag.String("tenant", defaultTenantID, "tenant_id to embed in claims.tenants[0]")
	role := flag.String("role", "user", "user role claim — user | super_admin")
	flag.Parse()

	priv := os.Getenv("JWT_SIGNING_KEY")
	pub := os.Getenv("JWT_PUBLIC_KEY")
	if priv == "" {
		fmt.Fprintln(os.Stderr, "JWT_SIGNING_KEY not set in env; source .env.local first")
		os.Exit(1)
	}

	key, err := auth.LoadSigningKey(priv, pub)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load signing key: %v\n", err)
		os.Exit(1)
	}

	lifetime := time.Duration(*days) * 24 * time.Hour
	tenants := []string{*tenant}

	fmt.Printf("# %d tokens · lifetime %dd · tenant %s · role %s\n\n",
		len(roster), *days, *tenant, *role)

	for _, d := range roster {
		userID := deterministicUUID(d.email)
		tok, err := key.MintAccessToken(userID, d.email, *role, tenants, lifetime)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: mint failed: %v\n", d.name, err)
			continue
		}
		fmt.Printf("## %s  <%s>\n%s\n\n", d.name, d.email, tok)
	}
}

// deterministicUUID derives a stable UUIDv5-ish hex string from the email
// so a re-run for the same designer produces the same `sub` claim. Useful
// for revoking later (we'd add the sub to a denylist; recurring runs don't
// rotate the identifier).
func deterministicUUID(email string) string {
	h := sha256.Sum256([]byte("indmoney-ds-service:user:" + email))
	hex := hex.EncodeToString(h[:16])
	// 8-4-4-4-12 UUID layout
	return fmt.Sprintf("%s-%s-%s-%s-%s", hex[0:8], hex[8:12], hex[12:16], hex[16:20], hex[20:32])
}
