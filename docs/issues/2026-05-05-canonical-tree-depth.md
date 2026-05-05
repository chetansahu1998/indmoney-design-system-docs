---
title: Re-export existing projects to pick up canonical_tree depth bump
status: open
created: 2026-05-05
priority: high
area: pipeline / canvas-v2
---

## What

`services/ds-service/internal/projects/pipeline.go` previously hard-coded `depth=3` on every Figma `/v1/files/<key>/nodes` call. Real INDmoney screens nest 6–8 levels (screen → header/body → card → row → label → icon), so frames at depth ≥ 4 came back as empty bounding boxes. The canvas-v2 LeafFrameRenderer rendered them as grey columns.

Commit `<TBD>` raised the constant to `CanonicalTreeFetchDepth = 10`. New exports use the deeper fetch automatically.

## Why this is a tracked issue, not a one-shot fix

Existing `canonical_tree` blobs stored at depth=3 stay shallow — the depth bump only takes effect on **re-export**. Every project already in SQLite needs to be re-imported once for its trees to be replaced.

## Operator runbook

Local dev:
```bash
# For one project:
curl -X POST http://localhost:8080/v1/projects/<slug>/export \
  -H "Authorization: Bearer $JWT"

# For everything in the sheet-sync set:
go run ./services/ds-service/cmd/sheets-sync --once
```

Prod (Fly):
```bash
fly ssh console -a indmoney-ds-service
# inside:
/usr/local/bin/server-export-all  # if shipped, otherwise loop curl per slug
```

## Verification

After re-export, sample a canonical_tree and check that previously-empty
frames have children:

```bash
SCREEN_ID=...
curl -sS http://localhost:8080/v1/projects/<slug>/screens/$SCREEN_ID/canonical-tree |
  python3 -c "
import json, sys
d = json.load(sys.stdin)
ct = d['canonical_tree']
def walk(n, depth=0, max_d=12):
    if depth > max_d: return
    if isinstance(n, dict):
        kids = n.get('children') or []
        bbox = n.get('absoluteBoundingBox') or {}
        if bbox.get('width', 0) > 100 and bbox.get('height', 0) > 100 and len(kids) == 0 and n.get('type') in ('FRAME','INSTANCE','COMPONENT'):
            print('STILL EMPTY:', n.get('type'), n.get('name'))
        for k in kids:
            walk(k, depth+1, max_d)
walk(ct.get('document'))
"
```

If "STILL EMPTY" lines still appear and the frames are nested deeper than 10 levels, bump the constant further. Real-world INDmoney screens have not been observed beyond 8 levels.

## Risks of going deeper

- Per-screen JSON payload from Figma grows ~3-5x at depth=10 vs depth=3.
- canonical_tree gzipped DB column grows proportionally; T8 migration handles this.
- Render-side memory scales linearly (~1 KB per node).

## When to close

After every project in the sheet-sync set has been re-exported and verified.
