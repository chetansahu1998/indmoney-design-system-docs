// Command revoke-token adds a JWT identifier to the revoked_jtis table so
// the next requireAuth check (within 60 s once the in-memory cache expires)
// returns 401 token_revoked. Idempotent on jti.
//
// Usage from Fly:
//
//	fly ssh console -C "/usr/local/bin/revoke-token --jti <id> --reason 'leaked'"
//	fly ssh console -C "/usr/local/bin/revoke-token --token <jwt> --reason 'rotation'"
//
// Two input modes:
//
//  1. --jti <id>     — direct revoke; you got the jti from a JWT decoder
//                      (e.g. jwt.io's payload viewer) or from an audit_log entry.
//  2. --token <jwt>  — paste the whole JWT; the CLI parses out the jti claim.
//                      Skips signature verification (revoking an expired or
//                      tampered JWT is fine — we just want the jti string).
//
// Local development:
//
//	cd services/ds-service && go run ./cmd/revoke-token --jti <id>
//
// SQLITE_PATH defaults to services/ds-service/data/ds.db; override via env
// to point at a different DB (e.g. a copy pulled from Fly for testing).

package main

import (
	"context"
	"encoding/json"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/db"
)

func main() {
	jti := flag.String("jti", "", "the JWT ID (RegisteredClaims.ID) to revoke")
	token := flag.String("token", "", "a full JWT — CLI extracts jti for you")
	reason := flag.String("reason", "", "free-form note for incident review")
	revokedBy := flag.String("by", "ops", "operator identifier (defaults to 'ops')")
	flag.Parse()

	if *jti == "" && *token == "" {
		fmt.Fprintln(os.Stderr, "error: provide --jti or --token")
		os.Exit(2)
	}
	if *jti != "" && *token != "" {
		fmt.Fprintln(os.Stderr, "error: --jti and --token are mutually exclusive")
		os.Exit(2)
	}

	resolvedJTI := *jti
	if *token != "" {
		extracted, err := jtiFromToken(*token)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		resolvedJTI = extracted
	}

	dbPath := os.Getenv("SQLITE_PATH")
	if dbPath == "" {
		dbPath = filepath.Join("services", "ds-service", "data", "ds.db")
	}
	conn, err := db.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open db %s: %v\n", dbPath, err)
		os.Exit(1)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := conn.RevokeJTI(ctx, resolvedJTI, *revokedBy, *reason); err != nil {
		fmt.Fprintf(os.Stderr, "revoke: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("revoked jti=%s by=%s reason=%q\n", resolvedJTI, *revokedBy, *reason)
}

// jtiFromToken decodes the JWT payload (no signature check) and returns its
// `jti` claim. Revoking a token doesn't require validating it — we just need
// the identifier — so this skips crypto for a forgiving operator UX.
func jtiFromToken(tok string) (string, error) {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("not a JWT (expected 3 parts, got %d)", len(parts))
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("decode payload: %w", err)
	}
	var claims struct {
		JTI string `json:"jti"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", fmt.Errorf("parse claims: %w", err)
	}
	if claims.JTI == "" {
		return "", fmt.Errorf("token has no jti claim")
	}
	return claims.JTI, nil
}
