# 0001. Stack and versions

Date: 2026-11-08. Status: accepted.

## Context

Sentinel is new, so there is no upgrade cost to starting on current releases,
and every old version picked now is a migration later.

## Decision

Latest stable of everything, checked against the registries on the date above:

| Component      | Version            | Notes |
|----------------|--------------------|-------|
| Go             | 1.27.4             | |
| Python         | 3.15.2             | via uv 0.13.7 |
| Node.js        | 26.13.0 LTS        | |
| pnpm           | 12.18.0            | |
| TypeScript     | 7.0.2              | |
| Nuxt           | 4.6.4              | not Nuxt 3, which reached EOL on 2026-07-31 |
| PostgreSQL     | 18.6               | image `pgvector/pgvector:0.8.7-pg18` |
| Redis          | 8.10.3             | Streams as the event bus |
| Caddy          | 2.11.9             | reverse proxy, automatic HTTPS |
| buf            | 1.75.0             | protobuf lint, breaking-change check, codegen |
| golangci-lint  | 2.15.0             | |
| FastAPI        | 0.162.2            | |
| LangGraph      | 1.2.20             | |
| scikit-learn   | 1.9.1              | |
| ruff / pytest / pyright | 0.17.4 / 9.2.0 / 1.1.414 | |
| eslint / vitest | 10.14.0 / 5.1.0   | |
| MCP TS SDK     | 1.32.2             | |

## Consequences

- Manifests and lockfiles are the source of truth after this; the table is
  the starting point, not something to keep in sync by hand.
- Bleeding-edge Python can lack wheels for ML packages (torch for the
  reranker). If a dependency has no 3.15 wheel, that service drops to the
  newest Python it supports and says so in its README.
