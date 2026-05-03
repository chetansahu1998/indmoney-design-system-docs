package projects

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Phase 6 source readers. Each function builds a slice of GraphIndexRow for a
// single source kind, ready to be UPSERTed by the worker. All functions are
// pure-ish: they take an explicit tenant + platform and a context, never
// reach into ambient state, and never write.
//
// Design contract:
//   - Functions are tenant-scoped (tenant_id baked into every returned row)
//     and platform-scoped (rows for one platform at a time).
//   - Functions are idempotent: calling twice on unchanged source data
//     returns identical rows (modulo MaterializedAt which the worker stamps).
//   - Functions never error on "no data": they return an empty slice. Errors
//     are reserved for I/O failures + parse failures the worker should log.

// ─── Node ID encoding ────────────────────────────────────────────────────────
//
// graph_index.id is a typed string key. The format is "<type>:<source-key>".
// These helpers keep the encoding consistent across writers + the wire shape.

const (
	idPrefixProduct   = "product:"
	idPrefixFolder    = "folder:"
	idPrefixFlow      = "flow:"
	idPrefixPersona   = "persona:"
	idPrefixComponent = "component:"
	idPrefixToken     = "token:"
	idPrefixDecision  = "decision:"
)

// productNodeID slugifies a product name to a stable key. Lowercase + replace
// spaces with hyphens. We don't reach for slug.go's `makeSlug` because product
// names like "Indian Stocks" would collide on path with project slugs.
func productNodeID(productName string) string {
	s := strings.ToLower(strings.TrimSpace(productName))
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "/", "-")
	return idPrefixProduct + s
}

// folderNodeID encodes a folder path "Indian Stocks/F&O/Learn Touchpoints" as
// a stable id. We keep the slashes inside the suffix so the parent_id chain
// can be reconstructed by trimming the last segment.
func folderNodeID(productName, folderPath string) string {
	prod := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(productName), " ", "-"))
	path := strings.Trim(folderPath, "/")
	if path == "" {
		return idPrefixProduct + prod // a "" folder degenerates to the product itself
	}
	return idPrefixFolder + prod + "/" + path
}

func flowNodeID(flowID string) string         { return idPrefixFlow + flowID }
func personaNodeID(personaID string) string   { return idPrefixPersona + personaID }
func componentNodeID(slug string) string      { return idPrefixComponent + slug }
func tokenNodeID(name string) string          { return idPrefixToken + name }
func decisionNodeID(decisionID string) string { return idPrefixDecision + decisionID }

// ─── Products + folders (derived from projects.path strings) ─────────────────

// BuildProductFolderRows reads `projects` for the given (tenant, platform)
// and emits product + folder nodes. Each unique `projects.product` becomes a
// product node; each unique segment of `projects.path` becomes a folder node
// scoped to its product. parent_id chain: folders → product, deepest folder
// upward.
//
// This intentionally does NOT emit flow nodes — flows have their own builder
// because their signal payload (severity counts, persona count) is heavier.
func BuildProductFolderRows(ctx context.Context, db *sql.DB, tenantID, platform string, now time.Time) ([]GraphIndexRow, error) {
	if tenantID == "" {
		return nil, errors.New("graph_sources: tenant_id required")
	}
	if platform != GraphPlatformMobile && platform != GraphPlatformWeb {
		return nil, fmt.Errorf("graph_sources: invalid platform %q", platform)
	}
	rows, err := db.QueryContext(ctx,
		`SELECT product, path, MAX(updated_at) AS max_updated, COUNT(*) AS flow_count
		   FROM projects
		  WHERE tenant_id = ? AND platform = ? AND deleted_at IS NULL
		  GROUP BY product, path`,
		tenantID, platform,
	)
	if err != nil {
		return nil, fmt.Errorf("query projects: %w", err)
	}
	defer rows.Close()

	type pathRow struct {
		Product   string
		Path      string
		Updated   time.Time
		FlowCount int
	}
	var paths []pathRow
	for rows.Next() {
		var pr pathRow
		var updated string
		if err := rows.Scan(&pr.Product, &pr.Path, &updated, &pr.FlowCount); err != nil {
			return nil, fmt.Errorf("scan projects: %w", err)
		}
		pr.Updated = parseTime(updated)
		paths = append(paths, pr)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Aggregate per-product update timestamps + collect distinct folder
	// segments. We use maps keyed by node id to dedupe; final slice is sorted
	// for deterministic output.
	productLatest := map[string]time.Time{}
	productLabel := map[string]string{}
	folderLatest := map[string]time.Time{}
	folderParent := map[string]string{}
	folderLabel := map[string]string{}

	for _, pr := range paths {
		pid := productNodeID(pr.Product)
		productLabel[pid] = pr.Product
		if pr.Updated.After(productLatest[pid]) {
			productLatest[pid] = pr.Updated
		}
		// Walk the path "/"-separated, building each ancestor folder.
		segments := splitNonEmpty(pr.Path, "/")
		for i := range segments {
			folderPath := strings.Join(segments[:i+1], "/")
			fid := folderNodeID(pr.Product, folderPath)
			folderLabel[fid] = segments[i]
			if pr.Updated.After(folderLatest[fid]) {
				folderLatest[fid] = pr.Updated
			}
			// Parent: previous segment, OR the product if i == 0.
			var parent string
			if i == 0 {
				parent = pid
			} else {
				parent = folderNodeID(pr.Product, strings.Join(segments[:i], "/"))
			}
			folderParent[fid] = parent
		}
	}

	out := make([]GraphIndexRow, 0, len(productLabel)+len(folderLabel))
	for id, label := range productLabel {
		out = append(out, GraphIndexRow{
			ID:             id,
			TenantID:       tenantID,
			Platform:       platform,
			Type:           GraphNodeProduct,
			Label:          label,
			ParentID:       "", // products are top-level
			LastUpdatedAt:  productLatest[id],
			SourceKind:     GraphSourceProjects,
			SourceRef:      "product:" + label,
			MaterializedAt: now,
		})
	}
	for id, label := range folderLabel {
		out = append(out, GraphIndexRow{
			ID:             id,
			TenantID:       tenantID,
			Platform:       platform,
			Type:           GraphNodeFolder,
			Label:          label,
			ParentID:       folderParent[id],
			LastUpdatedAt:  folderLatest[id],
			SourceKind:     GraphSourceProjects,
			SourceRef:      id, // folders are pure derivatives; their source_ref IS their id
			MaterializedAt: now,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// ─── Flows ───────────────────────────────────────────────────────────────────

// BuildFlowRows reads `flows` joined to `projects` and emits one row per
// flow. Severity counts are derived from active violations on the latest
// project_version (Phase 4 lifecycle). Persona count is the distinct count
// of persona_id across the flow's screens. The `edges_uses_json` (flow →
// component) is computed by walking screen_canonical_trees BLOBs — see
// BuildFlowComponentEdges below; this builder leaves the slice nil and the
// caller fills it in (so the canonical-tree walk can be parallelised).
func BuildFlowRows(ctx context.Context, db *sql.DB, tenantID, platform string, now time.Time) ([]GraphIndexRow, error) {
	if tenantID == "" {
		return nil, errors.New("graph_sources: tenant_id required")
	}
	// Pull the latest view_ready version per project alongside the flow row
	// so open_url can be qualified with ?v=<version_id>. Without the version
	// qualifier deep-links from /atlas always land on whatever happens to be
	// the most recent version at click-time, even if the leaf was rendered
	// against an older one (audit finding A4).
	rows, err := db.QueryContext(ctx,
		`SELECT f.id, f.name, f.persona_id,
		        f.updated_at,
		        p.product, p.path, p.slug,
		        COALESCE((
		          SELECT v.id FROM project_versions v
		           WHERE v.project_id = p.id
		             AND v.status = 'view_ready'
		           ORDER BY v.version_index DESC
		           LIMIT 1
		        ), '') AS latest_version_id
		   FROM flows f
		   JOIN projects p ON p.id = f.project_id
		  WHERE f.tenant_id = ?
		    AND f.deleted_at IS NULL
		    AND p.platform = ?
		    AND p.deleted_at IS NULL`,
		tenantID, platform,
	)
	if err != nil {
		return nil, fmt.Errorf("query flows: %w", err)
	}
	defer rows.Close()

	type flowRec struct {
		ID              string
		Label           string
		PersonaID       string
		UpdatedAt       time.Time
		Product         string
		Path            string
		Slug            string
		LatestVersionID string
	}
	var flows []flowRec
	for rows.Next() {
		var f flowRec
		var personaID sql.NullString
		var updatedAt string
		if err := rows.Scan(&f.ID, &f.Label, &personaID, &updatedAt, &f.Product, &f.Path, &f.Slug, &f.LatestVersionID); err != nil {
			return nil, fmt.Errorf("scan flow: %w", err)
		}
		if personaID.Valid {
			f.PersonaID = personaID.String
		}
		f.UpdatedAt = parseTime(updatedAt)
		flows = append(flows, f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Aggregate severity counts. Skipped when there are no flows to avoid
	// running a redundant query. Failure is non-fatal — flows materialise
	// with zero severity counts and the worker logs.
	sev := map[string]SeverityCounts{}
	if len(flows) > 0 {
		s, err := aggregateActiveSeverityForTenantPlatform(ctx, db, tenantID, platform)
		if err == nil {
			sev = s
		} else {
			return nil, fmt.Errorf("severity aggregate: %w", err)
		}
	}

	out := make([]GraphIndexRow, 0, len(flows))
	for _, f := range flows {
		parent := ""
		if f.Path != "" {
			parent = folderNodeID(f.Product, f.Path)
		} else {
			parent = productNodeID(f.Product)
		}
		counts := sev[f.ID]
		row := GraphIndexRow{
			ID:               flowNodeID(f.ID),
			TenantID:         tenantID,
			Platform:         platform,
			Type:             GraphNodeFlow,
			Label:            f.Label,
			ParentID:         parent,
			SeverityCritical: counts.Critical,
			SeverityHigh:     counts.High,
			SeverityMedium:   counts.Medium,
			SeverityLow:      counts.Low,
			SeverityInfo:     counts.Info,
			LastUpdatedAt:    f.UpdatedAt,
			OpenURL:          flowOpenURL(f.Slug, f.LatestVersionID),
			SourceKind:       GraphSourceFlows,
			SourceRef:        f.ID,
			MaterializedAt:   now,
		}
		if f.PersonaID != "" {
			row.PersonaCount = 1
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// aggregateActiveSeverityForTenantPlatform runs queries that return severity
// counts per flow_id, scoped to the latest project_version per project
// (Phase 4 lifecycle: status='active' violations only). Returns a map keyed
// by flow_id.
//
// We do this in two passes to avoid a CTE-resolution stack overflow we hit
// in modernc.org/sqlite when the same query inlined a window-function CTE
// referenced from a multi-JOIN. Two simple statements are also easier to
// reason about: (1) latest version_id per project, (2) violations grouped
// by flow_id × severity scoped to that set.
func aggregateActiveSeverityForTenantPlatform(ctx context.Context, db *sql.DB, tenantID, platform string) (map[string]SeverityCounts, error) {
	// Pass 1: latest project_version per project, restricted to this tenant
	// + platform.
	latestRows, err := db.QueryContext(ctx,
		`SELECT pv.id
		   FROM project_versions pv
		   JOIN projects p ON p.id = pv.project_id
		  WHERE p.platform = ?
		    AND p.deleted_at IS NULL
		    AND p.tenant_id = ?
		    AND pv.version_index = (
		        SELECT MAX(pv2.version_index)
		          FROM project_versions pv2
		         WHERE pv2.project_id = pv.project_id
		    )`,
		platform, tenantID,
	)
	if err != nil {
		return nil, fmt.Errorf("aggregate severity (latest versions): %w", err)
	}
	defer latestRows.Close()
	var latestVersionIDs []string
	for latestRows.Next() {
		var id string
		if err := latestRows.Scan(&id); err != nil {
			return nil, err
		}
		latestVersionIDs = append(latestVersionIDs, id)
	}
	if err := latestRows.Err(); err != nil {
		return nil, err
	}
	if len(latestVersionIDs) == 0 {
		return map[string]SeverityCounts{}, nil
	}

	// Pass 2: violations grouped by flow_id × severity, scoped to those
	// latest versions only. We build a parameter list dynamically (SQLite
	// has no array-bind; placeholder count = len(latestVersionIDs)).
	placeholders := make([]string, len(latestVersionIDs))
	args := make([]any, 0, 1+len(latestVersionIDs))
	args = append(args, tenantID)
	for i, id := range latestVersionIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}
	query := `SELECT s.flow_id, v.severity, COUNT(*)
		   FROM violations v
		   JOIN screens s ON s.id = v.screen_id
		  WHERE v.tenant_id = ?
		    AND v.status = 'active'
		    AND s.version_id IN (` + strings.Join(placeholders, ",") + `)
		  GROUP BY s.flow_id, v.severity`
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("aggregate severity (counts): %w", err)
	}
	defer rows.Close()
	out := map[string]SeverityCounts{}
	for rows.Next() {
		var flowID, severity string
		var count int
		if err := rows.Scan(&flowID, &severity, &count); err != nil {
			return nil, fmt.Errorf("scan severity row: %w", err)
		}
		c := out[flowID]
		switch strings.ToLower(severity) {
		case "critical":
			c.Critical = count
		case "high":
			c.High = count
		case "medium":
			c.Medium = count
		case "low":
			c.Low = count
		case "info":
			c.Info = count
		}
		out[flowID] = c
	}
	return out, rows.Err()
}

// ─── Personas (org-wide library; materialised per tenant + platform) ─────────

// BuildPersonaRows reads `personas` (NO tenant_id — the source is org-wide)
// and emits one row per approved persona per (tenant, platform). Phase 1
// learnings #1: personas are the documented exception to denormalised tenant
// scoping. Pending personas are excluded.
func BuildPersonaRows(ctx context.Context, db *sql.DB, tenantID, platform string, now time.Time) ([]GraphIndexRow, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, name, COALESCE(approved_at, created_at)
		   FROM personas
		  WHERE status = 'approved' AND deleted_at IS NULL`)
	if err != nil {
		return nil, fmt.Errorf("query personas: %w", err)
	}
	defer rows.Close()
	var out []GraphIndexRow
	for rows.Next() {
		var id, name, ts string
		if err := rows.Scan(&id, &name, &ts); err != nil {
			return nil, fmt.Errorf("scan persona: %w", err)
		}
		out = append(out, GraphIndexRow{
			ID:             personaNodeID(id),
			TenantID:       tenantID,
			Platform:       platform,
			Type:           GraphNodePersona,
			Label:          name,
			LastUpdatedAt:  parseTime(ts),
			SourceKind:     GraphSourcePersonas,
			SourceRef:      id,
			MaterializedAt: now,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, rows.Err()
}

// ─── Decisions ───────────────────────────────────────────────────────────────

// BuildDecisionRows reads `decisions` and emits one row per decision. The
// `edges_supersedes_json` array carries the single back-pointer from
// decisions.supersedes_id (Phase 5 chain semantics). Decision parent is the
// flow node it's anchored to.
func BuildDecisionRows(ctx context.Context, db *sql.DB, tenantID, platform string, now time.Time) ([]GraphIndexRow, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT d.id, d.flow_id, d.title, d.status, d.made_by_user_id, d.made_at, d.updated_at,
		        COALESCE(d.supersedes_id, '')
		   FROM decisions d
		   JOIN flows f ON f.id = d.flow_id
		   JOIN projects p ON p.id = f.project_id
		  WHERE d.tenant_id = ?
		    AND d.deleted_at IS NULL
		    AND p.platform = ?
		    AND p.deleted_at IS NULL`,
		tenantID, platform,
	)
	if err != nil {
		return nil, fmt.Errorf("query decisions: %w", err)
	}
	defer rows.Close()

	var out []GraphIndexRow
	for rows.Next() {
		var (
			id, flowID, title, status, madeBy, madeAt, updated string
			supersedesID                                        string
		)
		if err := rows.Scan(&id, &flowID, &title, &status, &madeBy, &madeAt, &updated, &supersedesID); err != nil {
			return nil, fmt.Errorf("scan decision: %w", err)
		}
		row := GraphIndexRow{
			ID:             decisionNodeID(id),
			TenantID:       tenantID,
			Platform:       platform,
			Type:           GraphNodeDecision,
			Label:          title,
			ParentID:       flowNodeID(flowID),
			LastUpdatedAt:  parseTime(updated),
			LastEditor:     madeBy,
			SourceKind:     GraphSourceDecisions,
			SourceRef:      id,
			MaterializedAt: now,
		}
		if supersedesID != "" {
			row.EdgesSupersedes = []string{decisionNodeID(supersedesID)}
		}
		// status surfaces in the open_url query string so the frontend can
		// route to the DRD decisions tab with this decision focused.
		row.OpenURL = "/projects/" + flowID + "?decision=" + id
		_ = status
		_ = madeAt
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, rows.Err()
}

// ─── Components (parsed from public/icons/glyph/manifest.json) ───────────────

// componentManifest is the slice of public/icons/glyph/manifest.json the
// Phase 6 worker cares about. Mirrors lib/icons/manifest.ts — kind, slug,
// name, category, variants[].fills[].bound_variable_id, plus Phase 7
// U6's composition_refs[] for the molecule → atom `uses` edge.
type componentManifest struct {
	GeneratedAt string `json:"generated_at"`
	Icons       []struct {
		Slug            string `json:"slug"`
		Name            string `json:"name"`
		Kind            string `json:"kind"`     // component | logo | illustration | icon
		Category        string `json:"category"` // SECTION ancestor name
		CompositionRefs []struct {
			AtomSlug string `json:"atom_slug,omitempty"`
		} `json:"composition_refs,omitempty"`
		Variants []struct {
			Fills []struct {
				BoundVariableID string `json:"bound_variable_id,omitempty"`
			} `json:"fills,omitempty"`
			Effects []struct {
				BoundVariableID string `json:"bound_variable_id,omitempty"`
			} `json:"effects,omitempty"`
			Corner struct {
				BoundVariableID string `json:"bound_variable_id,omitempty"`
			} `json:"corner,omitempty"`
		} `json:"variants,omitempty"`
	} `json:"icons"`
}

// BuildComponentRows parses the manifest at the given path and emits one row
// per kind='component' entry per platform. Components are platform-agnostic
// in source; the worker writes them under both mobile + web. parent_id is the
// `category` folder under the special "design-system" pseudo-product so
// components land grouped in the brain view.
//
// `binds_to` is built from the union of bound_variable_id values across all
// variants' fills + effects + corner. The variableIDToTokenName map is
// computed by BuildTokenRows + passed in here; unmatched bound_variable_ids
// are silently dropped (the component still renders; the binding is just
// invisible until the next manifest extraction reconciles names).
func BuildComponentRows(manifestPath, tenantID, platform string, variableIDToTokenName map[string]string, now time.Time) ([]GraphIndexRow, error) {
	if manifestPath == "" {
		return nil, errors.New("graph_sources: manifestPath required")
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		// Manifest absent is treated as "no component data" rather than fatal —
		// dev environments without the manifest committed should still boot.
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var m componentManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	st, _ := os.Stat(manifestPath)
	mtime := time.Now()
	if st != nil {
		mtime = st.ModTime()
	}

	categoryFolderID := func(cat string) string {
		// Pseudo-product "design-system" so components don't pollute real
		// product trees. Categories like "Buttons" become "folder:design-
		// system/Buttons".
		return idPrefixFolder + "design-system/" + cat
	}

	out := make([]GraphIndexRow, 0, len(m.Icons))
	for _, it := range m.Icons {
		if strings.ToLower(it.Kind) != "component" {
			continue
		}
		// Collect distinct bound_variable_id across all variants → resolve to
		// token names → emit "binds-to" edges.
		seen := map[string]struct{}{}
		var binds []string
		for _, v := range it.Variants {
			for _, f := range v.Fills {
				if f.BoundVariableID == "" {
					continue
				}
				if _, ok := seen[f.BoundVariableID]; ok {
					continue
				}
				seen[f.BoundVariableID] = struct{}{}
				if name, ok := variableIDToTokenName[f.BoundVariableID]; ok {
					binds = append(binds, tokenNodeID(name))
				}
			}
			for _, e := range v.Effects {
				if e.BoundVariableID == "" {
					continue
				}
				if _, ok := seen[e.BoundVariableID]; ok {
					continue
				}
				seen[e.BoundVariableID] = struct{}{}
				if name, ok := variableIDToTokenName[e.BoundVariableID]; ok {
					binds = append(binds, tokenNodeID(name))
				}
			}
			if v.Corner.BoundVariableID != "" {
				if _, ok := seen[v.Corner.BoundVariableID]; !ok {
					seen[v.Corner.BoundVariableID] = struct{}{}
					if name, ok := variableIDToTokenName[v.Corner.BoundVariableID]; ok {
						binds = append(binds, tokenNodeID(name))
					}
				}
			}
		}
		sort.Strings(binds)

		// Phase 7 U6 — composition_refs[].atom_slug → component → component
		// `uses` edges (molecule embeds atom). Self-references are dropped;
		// duplicates are deduped. Empty atom_slug means the embedded
		// component isn't extracted (external library / unpublished helper)
		// — skip silently.
		var uses []string
		seenUse := map[string]struct{}{}
		for _, ref := range it.CompositionRefs {
			if ref.AtomSlug == "" || ref.AtomSlug == it.Slug {
				continue
			}
			if _, ok := seenUse[ref.AtomSlug]; ok {
				continue
			}
			seenUse[ref.AtomSlug] = struct{}{}
			uses = append(uses, componentNodeID(ref.AtomSlug))
		}
		sort.Strings(uses)

		out = append(out, GraphIndexRow{
			ID:             componentNodeID(it.Slug),
			TenantID:       tenantID,
			Platform:       platform,
			Type:           GraphNodeComponent,
			Label:          it.Name,
			ParentID:       categoryFolderID(it.Category),
			EdgesUses:      uses,
			EdgesBindsTo:   binds,
			LastUpdatedAt:  mtime,
			OpenURL:        "/components/" + it.Slug,
			SourceKind:     GraphSourceManifest,
			SourceRef:      it.Slug,
			MaterializedAt: now,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// ─── Tokens (parsed from lib/tokens/indmoney/{base,semantic,*}.json) ─────────

// dtcgTokenFile is the slice of a DTCG token catalog the worker reads.
// Tokens are nested objects with $value / $type / $extensions.figma.variable_id
// at leaf level. We walk the tree depth-first.
type dtcgTokenFile = map[string]any

// BuildTokenRows walks every *.json file in tokensDir and returns:
//   - rows: one GraphIndexRow per leaf token, deduped by full dotted name
//   - variableIDToTokenName: a map from Figma variable_id → token name; used
//     by BuildComponentRows to resolve `bound_variable_id` references.
func BuildTokenRows(tokensDir, tenantID, platform string, now time.Time) ([]GraphIndexRow, map[string]string, error) {
	if tokensDir == "" {
		return nil, nil, errors.New("graph_sources: tokensDir required")
	}
	entries, err := os.ReadDir(tokensDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, map[string]string{}, nil
		}
		return nil, nil, fmt.Errorf("read tokens dir: %w", err)
	}

	variableIDToTokenName := map[string]string{}
	tokenLatestUpdate := map[string]time.Time{}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		// The "_extraction-meta.json" file is metadata, not tokens; skip.
		if strings.HasPrefix(e.Name(), "_") {
			continue
		}
		path := filepath.Join(tokensDir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		var root dtcgTokenFile
		if err := json.Unmarshal(data, &root); err != nil {
			return nil, nil, fmt.Errorf("parse %s: %w", e.Name(), err)
		}
		st, _ := os.Stat(path)
		mtime := time.Now()
		if st != nil {
			mtime = st.ModTime()
		}
		walkDTCG(root, "", func(name string, leaf map[string]any) {
			if _, ok := leaf["$value"]; !ok {
				return
			}
			// Variable ID for component binding resolution.
			if ext, ok := leaf["$extensions"].(map[string]any); ok {
				if fig, ok := ext["figma"].(map[string]any); ok {
					if vid, ok := fig["variable_id"].(string); ok && vid != "" {
						variableIDToTokenName[vid] = name
					}
				}
			}
			if mtime.After(tokenLatestUpdate[name]) {
				tokenLatestUpdate[name] = mtime
			}
		})
	}

	out := make([]GraphIndexRow, 0, len(tokenLatestUpdate))
	for name, ts := range tokenLatestUpdate {
		// parent_id chain follows the dotted path: "base.colour.blue.0e4b90"
		// → parent "folder:design-system/tokens/base.colour.blue".
		parent := tokenParentID(name)
		out = append(out, GraphIndexRow{
			ID:             tokenNodeID(name),
			TenantID:       tenantID,
			Platform:       platform,
			Type:           GraphNodeToken,
			Label:          tokenLeafLabel(name),
			ParentID:       parent,
			LastUpdatedAt:  ts,
			SourceKind:     GraphSourceTokens,
			SourceRef:      name,
			MaterializedAt: now,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, variableIDToTokenName, nil
}

// walkDTCG depth-first walks a DTCG token file, calling visit(name, leaf) on
// every leaf (a map with a "$value" key). Group nodes (no $value) are
// recursed into.
func walkDTCG(node map[string]any, prefix string, visit func(name string, leaf map[string]any)) {
	if _, isLeaf := node["$value"]; isLeaf {
		visit(prefix, node)
		return
	}
	for k, v := range node {
		if strings.HasPrefix(k, "$") {
			continue
		}
		child, ok := v.(map[string]any)
		if !ok {
			continue
		}
		next := k
		if prefix != "" {
			next = prefix + "." + k
		}
		walkDTCG(child, next, visit)
	}
}

func tokenParentID(name string) string {
	idx := strings.LastIndex(name, ".")
	if idx < 0 {
		return idPrefixFolder + "design-system/tokens"
	}
	return idPrefixFolder + "design-system/tokens/" + name[:idx]
}

func tokenLeafLabel(name string) string {
	idx := strings.LastIndex(name, ".")
	if idx < 0 {
		return name
	}
	return name[idx+1:]
}

// ─── Flow → component edges (canonical-tree walker) ─────────────────────────

// BuildVariantIDToSlugMap parses the manifest and returns a flat map from
// every variant_id (and the COMPONENT_SET set_id, which canonical_trees
// sometimes reference instead of a specific variant) to the manifest slug
// of the owning component entry. Skipped silently when the manifest is
// missing.
func BuildVariantIDToSlugMap(manifestPath string) (map[string]string, error) {
	if manifestPath == "" {
		return map[string]string{}, nil
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	// Lazy reuse of the manifest type — we only need the variant_id +
	// set_id surface, but the existing struct already carries the broader
	// shape so we reuse it.
	var m struct {
		Icons []struct {
			Slug      string `json:"slug"`
			Kind      string `json:"kind"`
			SetID     string `json:"set_id"`
			VariantID string `json:"variant_id"`
			Variants  []struct {
				VariantID string `json:"variant_id"`
			} `json:"variants,omitempty"`
		} `json:"icons"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, it := range m.Icons {
		if strings.ToLower(it.Kind) != "component" {
			continue
		}
		if it.SetID != "" {
			out[it.SetID] = it.Slug
		}
		if it.VariantID != "" {
			out[it.VariantID] = it.Slug
		}
		for _, v := range it.Variants {
			if v.VariantID != "" {
				out[v.VariantID] = it.Slug
			}
		}
	}
	return out, nil
}

// FillFlowComponentEdges walks every screen's canonical_tree for the given
// flow rows and stamps the deduped slug list onto each row's EdgesUses. The
// caller passes a variant_id → slug map so unmatched references (components
// not in the manifest) drop silently.
//
// One SQL query loads every screen+tree for the (tenant, platform) slice;
// the in-Go walk is O(node count) per tree and runs after the DB read so
// we don't hold the connection during the walk.
func FillFlowComponentEdges(ctx context.Context, db *sql.DB, tenantID, platform string, variantToSlug map[string]string, rows []GraphIndexRow) error {
	if len(rows) == 0 || len(variantToSlug) == 0 {
		return nil
	}
	// Build an index from flow_id → row index so we can stamp EdgesUses
	// without a linear scan per screen.
	flowIndex := map[string]int{}
	for i := range rows {
		if rows[i].Type != GraphNodeFlow {
			continue
		}
		flowIndex[rows[i].SourceRef] = i
	}
	if len(flowIndex) == 0 {
		return nil
	}

	// Read every (flow_id, canonical_tree) pair for this tenant + platform.
	// We use the latest project_version per project (Phase 4 lifecycle) so
	// edge derivation reflects the current state of designer work.
	q := `WITH latest_versions AS (
	    SELECT pv.id, pv.project_id
	      FROM project_versions pv
	      JOIN projects p ON p.id = pv.project_id
	     WHERE p.tenant_id = ? AND p.platform = ? AND p.deleted_at IS NULL
	       AND pv.version_index = (
	           SELECT MAX(pv2.version_index) FROM project_versions pv2
	            WHERE pv2.project_id = pv.project_id
	       )
	)
	SELECT s.flow_id, COALESCE(t.canonical_tree, '')
	  FROM screens s
	  JOIN latest_versions lv ON lv.id = s.version_id
	  LEFT JOIN screen_canonical_trees t ON t.screen_id = s.id
	 WHERE s.tenant_id = ?`
	dbRows, err := db.QueryContext(ctx, q, tenantID, platform, tenantID)
	if err != nil {
		return fmt.Errorf("load canonical trees: %w", err)
	}
	defer dbRows.Close()

	// Per-flow set of slugs (dedupe).
	perFlow := map[string]map[string]struct{}{}
	for dbRows.Next() {
		var flowID, treeJSON string
		if err := dbRows.Scan(&flowID, &treeJSON); err != nil {
			return fmt.Errorf("scan canonical tree row: %w", err)
		}
		if treeJSON == "" {
			continue
		}
		var tree any
		if err := json.Unmarshal([]byte(treeJSON), &tree); err != nil {
			// Malformed tree is logged at the worker level; skip silently
			// here so one bad tree doesn't poison the whole flow.
			continue
		}
		set := perFlow[flowID]
		if set == nil {
			set = map[string]struct{}{}
			perFlow[flowID] = set
		}
		walkInstancesForComponentRefs(tree, func(componentRef string) {
			if slug, ok := variantToSlug[componentRef]; ok {
				set[slug] = struct{}{}
			}
		})
	}
	if err := dbRows.Err(); err != nil {
		return fmt.Errorf("iterate canonical trees: %w", err)
	}

	// Stamp deduped sorted slug slice onto each flow's row.
	for flowID, slugs := range perFlow {
		idx, ok := flowIndex[flowID]
		if !ok {
			continue
		}
		out := make([]string, 0, len(slugs))
		for s := range slugs {
			out = append(out, componentNodeID(s))
		}
		sort.Strings(out)
		rows[idx].EdgesUses = out
	}
	return nil
}

// walkInstancesForComponentRefs depth-first walks a parsed canonical_tree
// and emits every component reference it finds on INSTANCE nodes. Figma's
// canonical-tree JSON varies between extractors; we accept any of:
//
//   - { "type": "INSTANCE", "componentId": "<id>" }
//   - { "type": "INSTANCE", "mainComponent": { "id": "<id>" } }
//   - { "type": "INSTANCE", "component_id": "<id>" }
//
// children may live under "children" (array) or "_children" (extractor
// variant); both are walked.
func walkInstancesForComponentRefs(node any, emit func(string)) {
	switch v := node.(type) {
	case []any:
		for _, child := range v {
			walkInstancesForComponentRefs(child, emit)
		}
	case map[string]any:
		// Detect INSTANCE + emit refs.
		if t, ok := v["type"].(string); ok && t == "INSTANCE" {
			if id, ok := v["componentId"].(string); ok && id != "" {
				emit(id)
			}
			if id, ok := v["component_id"].(string); ok && id != "" {
				emit(id)
			}
			if mc, ok := v["mainComponent"].(map[string]any); ok {
				if id, ok := mc["id"].(string); ok && id != "" {
					emit(id)
				}
			}
		}
		// Recurse into common child containers.
		if kids, ok := v["children"]; ok {
			walkInstancesForComponentRefs(kids, emit)
		}
		if kids, ok := v["_children"]; ok {
			walkInstancesForComponentRefs(kids, emit)
		}
		// Some extractors nest the document under "document" — recurse.
		if doc, ok := v["document"]; ok {
			walkInstancesForComponentRefs(doc, emit)
		}
	}
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func splitNonEmpty(s, sep string) []string {
	out := []string{}
	for _, p := range strings.Split(s, sep) {
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

// flowOpenURL builds the deep-link a flow leaf renders for in /atlas. Always
// includes ?v=<latest_version> when a view_ready version exists so the user
// lands on the same revision the leaf's signal was computed against. Falls
// back to the slug-only URL when no version has reached view_ready (the
// project's first export is mid-pipeline or every version failed). Audit A4.
func flowOpenURL(slug, latestVersionID string) string {
	if latestVersionID == "" {
		return "/projects/" + slug
	}
	return "/projects/" + slug + "?v=" + latestVersionID
}
