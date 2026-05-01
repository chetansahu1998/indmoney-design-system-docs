# DRD Hocuspocus sidecar (Phase 5 U1)

Bridges Yjs-backed BlockNote editing in the docs site to ds-service via a
shared-secret loopback API. One process per ds-service instance; routes
all per-flow Y.Doc state through ds-service's `/internal/drd/*` endpoints.

## Run

```sh
cd services/hocuspocus
npm install
DS_HOCUSPOCUS_SHARED_SECRET=<long-random> \
DS_SERVICE_URL=http://127.0.0.1:7475 \
HOCUSPOCUS_PORT=7676 \
npm run dev
```

## Environment

| Var | Default | Purpose |
|-----|---------|---------|
| `HOCUSPOCUS_PORT` | `7676` | WebSocket listener |
| `DS_SERVICE_URL` | `http://127.0.0.1:7475` | Where to call `/internal/drd/*` |
| `DS_HOCUSPOCUS_SHARED_SECRET` | _(required)_ | Sent as `X-DS-Hocuspocus-Secret` |
| `HOCUSPOCUS_SNAPSHOT_DEBOUNCE_MS` | `30000` | Debounce window before persist |

Both this sidecar and ds-service must read the same `DS_HOCUSPOCUS_SHARED_SECRET`.

## Client handshake

The docs site mints a single-use ticket via
`POST /v1/projects/:slug/flows/:flow_id/drd/ticket`, then opens a WebSocket
to this sidecar with `documentName=<flow_id>` and `token=<ticket>`. The
sidecar's `onAuthenticate` posts to `/internal/drd/auth`; ds-service
redeems the ticket and returns the user/tenant/flow context that the
sidecar attaches to the connection.

## Document persistence

* `onLoadDocument` GETs `/internal/drd/load?flow_id=…&tenant_id=…`. A
  binary body is applied via `Y.applyUpdate`; an empty body bootstraps
  a fresh Y.Doc.
* `onChange` debounces a 30s timer (configurable). On fire, encodes the
  Y.Doc state via `Y.encodeStateAsUpdate` and POSTs to
  `/internal/drd/snapshot` with the binary body + flow / tenant / user
  headers.
* `onDisconnect` flushes immediately when the last peer leaves so the
  next refresh doesn't lose unsaved work.

ds-service's `PersistYDocSnapshot` enforces the 5MB Y.Doc cap and bumps
`flow_drd.revision` on every snapshot. Audit-log rows under
`drd.snapshot` surface in the project Activity rail (Phase 5 U12).

## Production deployment

Run alongside ds-service in the same docker-compose / k8s pod so
`/internal/drd/*` traffic stays on a private interface. The
`X-DS-Hocuspocus-Secret` header is the only authentication on those
endpoints; treat the secret like any database credential.
