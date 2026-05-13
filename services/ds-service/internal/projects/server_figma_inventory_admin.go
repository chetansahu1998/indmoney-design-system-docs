package projects

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/auth"
)

// server_figma_inventory_admin.go — admin HTTP surface for the FIGMA DB
// (migration 0025 + internal/figma/inventory). Six endpoints, all behind
// the existing JWT auth + super_admin gate:
//
//   GET    /v1/admin/figma-inventory/teams
//   POST   /v1/admin/figma-inventory/teams         { team_id, team_name }
//   DELETE /v1/admin/figma-inventory/teams/{team_id}
//   GET    /v1/admin/figma-inventory/tree?team_id=&include_deleted=
//   POST   /v1/admin/figma-inventory/sync
//   GET    /v1/admin/figma-inventory/runs?limit=
//
// The poller goroutine itself lives in internal/figma/inventory and is
// reached via a small interface (InventoryPoller) so the Server doesn't
// need to import the inventory package directly.

// InventoryPoller is the slice of *inventory.Poller this admin surface
// needs. Defined locally to avoid a circular import (inventory already
// depends on projects).
type InventoryPoller interface {
	TriggerSync()
}

// HandleFigmaInventoryListTeams serves GET /v1/admin/figma-inventory/teams.
// Returns every team seed for the tenant with its last-crawl status.
func (s *Server) HandleFigmaInventoryListTeams(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET only")
		return
	}
	tenantID, ok := s.requireFigmaInventoryAdminTenant(w, r)
	if !ok {
		return
	}
	repo := NewTenantRepo(s.deps.DB.DB, tenantID)
	seeds, err := repo.ListFigmaTeamSeeds(r.Context())
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "list_teams", err.Error())
		return
	}
	out := make([]figmaTeamSeedDTO, 0, len(seeds))
	for _, s := range seeds {
		out = append(out, figmaTeamSeedToDTO(s))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"teams": out,
		"count": len(out),
	})
}

// HandleFigmaInventoryAddTeam serves POST /v1/admin/figma-inventory/teams.
// Body: { "team_id": "...", "team_name": "..." }. Idempotent on team_id.
func (s *Server) HandleFigmaInventoryAddTeam(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST only")
		return
	}
	tenantID, ok := s.requireFigmaInventoryAdminTenant(w, r)
	if !ok {
		return
	}
	claims, _ := r.Context().Value(ctxKeyClaims).(*auth.Claims)
	var req struct {
		TeamID   string `json:"team_id"`
		TeamName string `json:"team_name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSONErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	req.TeamID = strings.TrimSpace(req.TeamID)
	req.TeamName = strings.TrimSpace(req.TeamName)
	if req.TeamID == "" || req.TeamName == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_fields", "team_id and team_name required")
		return
	}
	userID := ""
	if claims != nil {
		userID = claims.Sub
	}
	repo := NewTenantRepo(s.deps.DB.DB, tenantID)
	if err := repo.UpsertFigmaTeamSeed(r.Context(), FigmaTeamSeed{
		TeamID:        req.TeamID,
		TeamName:      req.TeamName,
		AddedByUserID: userID,
		Enabled:       true,
	}); err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "upsert_team", err.Error())
		return
	}
	// Trigger an immediate crawl so the admin sees results without waiting
	// for the 5-min tick.
	if s.deps.InventoryPoller != nil {
		s.deps.InventoryPoller.TriggerSync()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"team_id":   req.TeamID,
		"team_name": req.TeamName,
		"enabled":   true,
	})
}

// HandleFigmaInventoryRemoveTeam serves DELETE
// /v1/admin/figma-inventory/teams/{team_id}. Soft-disable — preserves
// the seed row and all crawled data so re-enable works without re-crawl.
func (s *Server) HandleFigmaInventoryRemoveTeam(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeJSONErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "DELETE only")
		return
	}
	tenantID, ok := s.requireFigmaInventoryAdminTenant(w, r)
	if !ok {
		return
	}
	teamID := r.PathValue("team_id")
	if teamID == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_team_id", "team_id required")
		return
	}
	repo := NewTenantRepo(s.deps.DB.DB, tenantID)
	if err := repo.SetFigmaTeamSeedEnabled(r.Context(), teamID, false); err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "disable_team", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"team_id": teamID,
		"enabled": false,
	})
}

// HandleFigmaInventoryTree serves GET /v1/admin/figma-inventory/tree.
//
// Required query: team_id. Optional: include_deleted=1.
func (s *Server) HandleFigmaInventoryTree(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET only")
		return
	}
	tenantID, ok := s.requireFigmaInventoryAdminTenant(w, r)
	if !ok {
		return
	}
	teamID := r.URL.Query().Get("team_id")
	if teamID == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_team_id", "team_id query param required")
		return
	}
	includeDeleted := r.URL.Query().Get("include_deleted") == "1"
	repo := NewTenantRepo(s.deps.DB.DB, tenantID)
	tree, err := repo.GetFigmaInventoryTree(r.Context(), teamID, includeDeleted)
	if errors.Is(err, ErrNotFound) {
		writeJSONErr(w, http.StatusNotFound, "team_not_found", "team not crawled yet")
		return
	}
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "tree_load", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, tree)
}

// HandleFigmaInventorySync serves POST /v1/admin/figma-inventory/sync.
// Non-blocking — triggers the poller's next cycle. Returns 503 when no
// poller is wired (e.g. dev configs that didn't start it).
func (s *Server) HandleFigmaInventorySync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST only")
		return
	}
	if _, ok := s.requireFigmaInventoryAdminTenant(w, r); !ok {
		return
	}
	if s.deps.InventoryPoller == nil {
		writeJSONErr(w, http.StatusServiceUnavailable, "no_poller", "inventory poller not configured")
		return
	}
	s.deps.InventoryPoller.TriggerSync()
	writeJSON(w, http.StatusAccepted, map[string]any{
		"triggered": true,
		"at":        time.Now().UTC().Format(time.RFC3339),
	})
}

// HandleFigmaInventoryFileNodes serves GET
// /v1/admin/figma-inventory/files/{file_key}/nodes?limit=
//
// Returns the deep node tree (Phase 2C) for one file as a flat list,
// ordered depth-first. Caller stitches the tree client-side using
// parent_id. Capped at limit (default 2000) so huge files don't blow
// up the admin payload — the UI offers a "fetch all" override.
func (s *Server) HandleFigmaInventoryFileNodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET only")
		return
	}
	tenantID, ok := s.requireFigmaInventoryAdminTenant(w, r)
	if !ok {
		return
	}
	fileKey := r.PathValue("file_key")
	if fileKey == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_file_key", "file_key required")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	repo := NewTenantRepo(s.deps.DB.DB, tenantID)
	nodes, err := repo.ListFigmaNodesForFile(r.Context(), fileKey, limit)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "list_nodes", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"file_key": fileKey,
		"nodes":    nodes,
		"count":    len(nodes),
	})
}

// HandleFigmaInventoryRuns serves GET /v1/admin/figma-inventory/runs.
func (s *Server) HandleFigmaInventoryRuns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET only")
		return
	}
	tenantID, ok := s.requireFigmaInventoryAdminTenant(w, r)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	repo := NewTenantRepo(s.deps.DB.DB, tenantID)
	runs, err := repo.ListFigmaInventoryRuns(r.Context(), limit)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "list_runs", err.Error())
		return
	}
	out := make([]figmaInventoryRunDTO, 0, len(runs))
	for _, r := range runs {
		out = append(out, figmaInventoryRunToDTO(r))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"runs":  out,
		"count": len(out),
	})
}

// requireFigmaInventoryAdminTenant is the auth+tenant resolution helper
// shared by every inventory admin handler. Mirrors
// requireOrganismAdminTenant exactly so the gate behavior stays consistent.
func (s *Server) requireFigmaInventoryAdminTenant(w http.ResponseWriter, r *http.Request) (string, bool) {
	claims, _ := r.Context().Value(ctxKeyClaims).(*auth.Claims)
	if claims == nil {
		writeJSONErr(w, http.StatusUnauthorized, "unauthorized", "missing claims")
		return "", false
	}
	tenantID := s.resolveTenantID(claims)
	if tenantID == "" {
		writeJSONErr(w, http.StatusForbidden, "no_tenant", "")
		return "", false
	}
	return tenantID, true
}

// ─── DTOs ────────────────────────────────────────────────────────────────────

type figmaTeamSeedDTO struct {
	TeamID          string `json:"team_id"`
	TeamName        string `json:"team_name"`
	AddedByUserID   string `json:"added_by_user_id,omitempty"`
	AddedAt         string `json:"added_at"`
	Enabled         bool   `json:"enabled"`
	LastCrawlAt     string `json:"last_crawl_at,omitempty"`
	LastCrawlStatus string `json:"last_crawl_status,omitempty"`
	LastCrawlError  string `json:"last_crawl_error,omitempty"`
}

func figmaTeamSeedToDTO(s FigmaTeamSeed) figmaTeamSeedDTO {
	dto := figmaTeamSeedDTO{
		TeamID:          s.TeamID,
		TeamName:        s.TeamName,
		AddedByUserID:   s.AddedByUserID,
		Enabled:         s.Enabled,
		LastCrawlStatus: s.LastCrawlStatus,
		LastCrawlError:  s.LastCrawlError,
	}
	if !s.AddedAt.IsZero() {
		dto.AddedAt = s.AddedAt.Format(time.RFC3339)
	}
	if !s.LastCrawlAt.IsZero() {
		dto.LastCrawlAt = s.LastCrawlAt.Format(time.RFC3339)
	}
	return dto
}

type figmaInventoryRunDTO struct {
	ID               int64    `json:"id"`
	StartedAt        string   `json:"started_at"`
	FinishedAt       string   `json:"finished_at,omitempty"`
	DurationMs       int64    `json:"duration_ms,omitempty"`
	TeamsCrawled     int      `json:"teams_crawled"`
	ProjectsSeen     int      `json:"projects_seen"`
	FilesSeen        int      `json:"files_seen"`
	FilesRefetched   int      `json:"files_refetched"`
	PagesUpserted    int      `json:"pages_upserted"`
	SectionsUpserted int      `json:"sections_upserted"`
	ErrorCount       int      `json:"error_count"`
	ErrorSample      []string `json:"error_sample,omitempty"`
}

func figmaInventoryRunToDTO(r FigmaInventoryRunRow) figmaInventoryRunDTO {
	dto := figmaInventoryRunDTO{
		ID:               r.ID,
		TeamsCrawled:     r.TeamsCrawled,
		ProjectsSeen:     r.ProjectsSeen,
		FilesSeen:        r.FilesSeen,
		FilesRefetched:   r.FilesRefetched,
		PagesUpserted:    r.PagesUpserted,
		SectionsUpserted: r.SectionsUpserted,
		ErrorCount:       r.ErrorCount,
	}
	if !r.StartedAt.IsZero() {
		dto.StartedAt = r.StartedAt.Format(time.RFC3339)
	}
	if !r.FinishedAt.IsZero() {
		dto.FinishedAt = r.FinishedAt.Format(time.RFC3339)
		if !r.StartedAt.IsZero() {
			dto.DurationMs = r.FinishedAt.Sub(r.StartedAt).Milliseconds()
		}
	}
	if r.ErrorSampleJSON != "" {
		var sample []string
		if err := json.Unmarshal([]byte(r.ErrorSampleJSON), &sample); err == nil {
			dto.ErrorSample = sample
		}
	}
	return dto
}
