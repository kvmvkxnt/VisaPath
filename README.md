# Sentinel

Self-hosted security copilot: collects your servers' logs, flags suspicious
activity with rules and an anomaly model, and lets an AI agent investigate an
incident and propose a fix that a human approves before it runs.

> Status: pre-alpha. The event pipeline runs (agent, ingest, Postgres,
> Redis); detection, the AI investigator and the dashboard do not exist yet.
> The build plan is in [docs/PLAN.md](docs/PLAN.md).

## How it works

```
 server ── agent (Go) ──gRPC/mTLS──> ingest (Go) ──> Postgres + Redis Streams
                ^                                          │
                │ approved actions                         v
                │                          detector (Python): rules + Isolation Forest
                │                                          │ alerts
                │                                          v
                └──── web (Nuxt, live WebSocket) <── ai (Python): RAG + investigator agent
                         ^                            ^
                       human              mcp (TypeScript) <── Claude / Cursor
```

1. The agent tails Nginx, SSH, Xray and Docker logs and ships normalized events.
2. The detector raises alerts with a score and a reason.
3. The investigator agent looks up the IP, related events, CVEs and runbooks,
   writes a summary and proposes an action, for example an nftables block.
4. A human approves or rejects it in the dashboard. Only then does the agent act.

## Quick start

Needs Docker with Compose, and Go to build the agent.

```sh
cp .env.example .env          # then set POSTGRES_PASSWORD
docker compose up -d --wait   # Postgres, Redis, ingest; creates the mTLS CA
```

The first start writes a private CA and the ingest certificate to
`deploy/certs/` (git-ignored). Give each monitored host its own
certificate; its common name is the host name Sentinel records:

```sh
docker compose run --rm certs agent web-1
```

Then run the agent on that host with `deploy/certs/agents/web-1.{pem,key}`
and `deploy/certs/ca.pem`, see [agent/README.md](agent/README.md). If
agents reach ingest by anything other than `localhost`, put that name or
address in `INGEST_SANS` before the first start.

Events land in the `events` table:

```sh
docker compose exec postgres psql -U sentinel -c 'select * from events'
```

## Security model

Log lines are attacker-controlled. Sentinel treats them as data, never as
instructions:

- log content reaches the model only as quoted data;
- every agent tool is read-only except `propose_block`, which only proposes;
- the agent cannot approve its own proposals;
- a test suite of prompt-injected log lines runs in CI.

## Repository layout

| Path        | Language   | What it is |
|-------------|------------|------------|
| `agent/`    | Go         | Per-server log collector and action executor |
| `ingest/`   | Go         | gRPC receiver, writes Postgres and Redis Streams |
| `proto/`    | Protobuf   | Shared event and action schemas |
| `detector/` | Python     | Rules engine and Isolation Forest |
| `ai/`       | Python     | FastAPI, RAG pipeline, LangGraph investigator |
| `mcp/`      | TypeScript | MCP server exposing Sentinel tools to Claude |
| `web/`      | Vue (Nuxt) | Dashboard: live alerts, timelines, approvals, settings |
| `evals/`    |            | Golden questions and injection tests |
| `deploy/`   |            | Docker Compose and reverse proxy |
| `docs/`     |            | Plan and architecture decision records (`docs/adr/`) |

## Contributing

Branching, commit style, tests-first and documentation rules live in
[CLAUDE.md](CLAUDE.md). In short: branch from `dev-fable`, write the test first,
open a PR, merge when CI is green.

## License

[MIT](LICENSE)
