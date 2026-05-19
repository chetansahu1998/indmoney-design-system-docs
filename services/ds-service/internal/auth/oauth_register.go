package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

// oauth_register.go — RFC 7591 Dynamic Client Registration.
//
// POST /v1/oauth/register
//   Request body (JSON):
//     {
//       "redirect_uris": ["https://claude.ai/api/mcp/auth_callback"],
//       "client_name": "Claude — Acme Workspace",
//       "scope": "",
//       "grant_types": ["authorization_code", "refresh_token"],
//       "token_endpoint_auth_method": "none"
//     }
//   Response (201):
//     {
//       "client_id": "<uuid>",
//       "client_id_issued_at": 1700000000,
//       "redirect_uris": [...],
//       "client_name": "...",
//       "grant_types": [...],
//       "response_types": ["code"],
//       "token_endpoint_auth_method": "none"
//     }
//
// We only support PUBLIC clients (PKCE-bound, no client_secret). Confidential
// clients would need a client_secret mint + storage path; not implemented
// because every MCP client we know about is public per RFC 8252.

// RegisterClientRequest mirrors the RFC 7591 JSON body fields we accept.
// Unknown fields are ignored (RFC 7591 §3.1.1 — server MAY ignore unsupported
// metadata).
type RegisterClientRequest struct {
	RedirectURIs            []string `json:"redirect_uris"`
	ClientName              string   `json:"client_name,omitempty"`
	GrantTypes              []string `json:"grant_types,omitempty"`
	ResponseTypes           []string `json:"response_types,omitempty"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method,omitempty"`
	Scope                   string   `json:"scope,omitempty"`
	Contacts                []string `json:"contacts,omitempty"`
	ClientURI               string   `json:"client_uri,omitempty"`
	LogoURI                 string   `json:"logo_uri,omitempty"`
	TosURI                  string   `json:"tos_uri,omitempty"`
	PolicyURI               string   `json:"policy_uri,omitempty"`
	SoftwareID              string   `json:"software_id,omitempty"`
	SoftwareVersion         string   `json:"software_version,omitempty"`
}

// RegisterClientResponse is the RFC 7591 §3.2.1 success body. We include
// every field of the stored client so the caller can confirm what was
// registered (no separate read endpoint per RFC 7592 — Claude doesn't
// need to update its own registration in this MCP flow).
type RegisterClientResponse struct {
	ClientID                string   `json:"client_id"`
	ClientIDIssuedAt        int64    `json:"client_id_issued_at"`
	RedirectURIs            []string `json:"redirect_uris"`
	ClientName              string   `json:"client_name,omitempty"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	Scope                   string   `json:"scope,omitempty"`
}

// handleOAuthRegister is the RFC 7591 registration endpoint. Open — no
// auth required, no client allowlist. The security control is the
// redirect_uris field, which is exact-match-validated at authorize time;
// an attacker registering a client_id can only ride it with the
// redirect_uri they registered (which they already control).
func handleOAuthRegister(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// RFC 7591 §3.1 — request is application/json.
		ct := r.Header.Get("Content-Type")
		if !strings.HasPrefix(ct, "application/json") {
			writeOAuthError(w, http.StatusBadRequest, errInvalidRequest,
				"Content-Type must be application/json")
			return
		}
		var req RegisterClientRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeOAuthError(w, http.StatusBadRequest, errInvalidRequest,
				"invalid JSON: "+err.Error())
			return
		}

		// Validate: at least one redirect_uri, all exact-match-safe.
		if len(req.RedirectURIs) == 0 {
			writeOAuthError(w, http.StatusBadRequest, errInvalidRequest,
				"redirect_uris required")
			return
		}
		for _, u := range req.RedirectURIs {
			if err := validateRegisteredRedirectURI(u); err != nil {
				writeOAuthError(w, http.StatusBadRequest, errInvalidRequest,
					"invalid redirect_uri "+u+": "+err.Error())
				return
			}
		}

		// PKCE-only: only token_endpoint_auth_method=none is supported.
		// Defaults to "none" if absent. Anything else is rejected — we
		// don't mint or store client_secrets.
		method := req.TokenEndpointAuthMethod
		if method == "" {
			method = "none"
		}
		if method != "none" {
			writeOAuthError(w, http.StatusBadRequest, errInvalidRequest,
				"only token_endpoint_auth_method=none is supported (PKCE)")
			return
		}

		// Default grant_types + response_types if the client didn't specify.
		grantTypes := req.GrantTypes
		if len(grantTypes) == 0 {
			grantTypes = []string{"authorization_code", "refresh_token"}
		}
		responseTypes := req.ResponseTypes
		if len(responseTypes) == 0 {
			responseTypes = []string{"code"}
		}

		// Mint a fresh client_id. UUIDv4 — 122 bits of entropy, indistinguishable
		// from random for the redirect-uri-bound attack model.
		clientID := uuid.NewString()
		now := time.Now()

		// Persist.
		redirectsJSON, _ := json.Marshal(req.RedirectURIs)
		grantsJSON, _ := json.Marshal(grantTypes)
		respsJSON, _ := json.Marshal(responseTypes)
		contactsJSON, _ := json.Marshal(req.Contacts)
		_, err := db.ExecContext(r.Context(), `
			INSERT INTO oauth_clients (
				id, client_name, redirect_uris, grant_types, response_types,
				token_endpoint_auth_method, scope, contacts, client_uri,
				logo_uri, tos_uri, policy_uri, software_id, software_version,
				created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			clientID, req.ClientName, string(redirectsJSON), string(grantsJSON), string(respsJSON),
			method, req.Scope, string(contactsJSON), req.ClientURI,
			req.LogoURI, req.TosURI, req.PolicyURI, req.SoftwareID, req.SoftwareVersion,
			now.Unix(),
		)
		if err != nil {
			writeOAuthError(w, http.StatusInternalServerError, errServerError,
				"persist client: "+err.Error())
			return
		}

		// RFC 7591 §3.2.1 — return 201 Created with the registered metadata.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(RegisterClientResponse{
			ClientID:                clientID,
			ClientIDIssuedAt:        now.Unix(),
			RedirectURIs:            req.RedirectURIs,
			ClientName:              req.ClientName,
			GrantTypes:              grantTypes,
			ResponseTypes:           responseTypes,
			TokenEndpointAuthMethod: method,
			Scope:                   req.Scope,
		})
	}
}

// validateRegisteredRedirectURI applies the same exact-match-friendly
// shape rules the OAuth authorize handler enforces, but at registration
// time. Mirrors RFC 6749 §3.1.2 — https:// only (or http://localhost
// for development), no fragment, syntactically a URL.
func validateRegisteredRedirectURI(raw string) error {
	if raw == "" {
		return errors.New("empty")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	if u.Fragment != "" {
		return errors.New("must not contain a fragment")
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		host := u.Hostname()
		if host == "localhost" || host == "127.0.0.1" || host == "::1" {
			return nil
		}
		return errors.New("http:// only allowed for localhost (dev)")
	default:
		return fmt.Errorf("scheme %q not allowed", u.Scheme)
	}
}

// LookupDynamicClient reads a row from oauth_clients by client_id and
// returns it as an OAuthClient (the same shape the authorize handler
// uses for static config). Caller falls back to the static slice when
// this returns ErrNotFound.
//
// Updates last_used_at as a side effect so a future reaper can prune
// dormant clients. Best-effort — failure here doesn't block the auth
// flow.
func LookupDynamicClient(ctx context.Context, db *sql.DB, clientID string) (OAuthClient, bool) {
	if clientID == "" {
		return OAuthClient{}, false
	}
	row := db.QueryRowContext(ctx,
		`SELECT id, redirect_uris FROM oauth_clients WHERE id = ?`,
		clientID)
	var (
		id            string
		redirectsJSON string
	)
	if err := row.Scan(&id, &redirectsJSON); err != nil {
		return OAuthClient{}, false
	}
	var uris []string
	if err := json.Unmarshal([]byte(redirectsJSON), &uris); err != nil {
		return OAuthClient{}, false
	}
	// Touch last_used_at — best-effort, ignore errors.
	_, _ = db.ExecContext(ctx,
		`UPDATE oauth_clients SET last_used_at = ? WHERE id = ?`,
		time.Now().Unix(), clientID)
	return OAuthClient{
		ID:                  id,
		AllowedRedirectURIs: uris,
	}, true
}
