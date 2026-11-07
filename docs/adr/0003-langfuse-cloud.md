# 0003. Langfuse Cloud for LLM tracing

Date: 2026-11-08. Status: accepted.

## Context

Every LLM call must be traced with cost and latency. Self-hosted Langfuse v3
needs ClickHouse, Redis and S3-compatible storage next to its own web and
worker containers, which is a lot to run on a single VPS.

## Decision

Use Langfuse Cloud (EU region, free tier). Services read `LANGFUSE_HOST`,
`LANGFUSE_PUBLIC_KEY` and `LANGFUSE_SECRET_KEY` from `.env`.

## Consequences

- Nothing extra in Compose.
- Prompts and log excerpts sent to the model leave the server and are stored
  by Langfuse. Acceptable for a demo; revisit before monitoring anyone
  else's servers.
- Switching to self-hosted later only changes `LANGFUSE_HOST`.
