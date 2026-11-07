# Sentinel

Self-hosted security copilot: collects your servers' logs, flags suspicious
activity with rules and an anomaly model, and lets an AI agent investigate an
incident and propose a fix that a human approves before it runs.

> Status: pre-alpha. Nothing runs yet. The build plan is in
> [docs/PLAN.md](docs/PLAN.md).

## How it works

```
 server ── agent (Go) ──gRPC/mTLS──> ingest (Go) ──> Postgres + Redis Streams
                ^                                          │
                │ approved actions                         v
                │                          detector (Python): rules + Isolation Forest
                │                                          │ alerts
                │                                          v
                └──── web (Nuxt) <── gateway (WebSocket) <── ai (Python): RAG + investigator agent
                         ^                                   ^
                       human                     mcp (TypeScript) <── Claude / Cursor
```

1. The agent tails Nginx, SSH, Xray and Docker logs and ships normalized events.
2. The detector raises alerts with a score and a reason.
3. The investigator agent looks up the IP, related events, CVEs and runbooks,
   writes a summary and proposes an action, for example an nftables block.
4. A human approves or rejects it in the dashboard. Only then does the agent act.

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
| `gateway/`  | TypeScript | WebSocket fan-out of live alerts |
| `web/`      | Vue (Nuxt) | Dashboard: alerts, timelines, approvals |
| `evals/`    |            | Golden questions and injection tests |
| `deploy/`   |            | Docker Compose and reverse proxy |
| `docs/`     |            | Plan and architecture decision records |

## Contributing

Branching, commit style, tests-first and documentation rules live in
[CLAUDE.md](CLAUDE.md). In short: branch from `dev`, write the test first,
open a PR, merge when CI is green.
