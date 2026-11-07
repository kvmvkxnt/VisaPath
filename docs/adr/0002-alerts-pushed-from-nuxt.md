# 0002. Push live alerts from the Nuxt server, no gateway service

Date: 2026-11-08. Status: accepted.

## Context

The plan had a separate Node `gateway` whose only job was to read the alert
stream from Redis and fan it out over WebSockets to the dashboard.

## Decision

Drop `gateway`. Nuxt's server (Nitro) supports WebSockets, so `web` reads the
Redis stream and pushes alerts to connected browsers itself.

## Consequences

- One service, one container and one deploy target fewer.
- The browser talks to one origin, which keeps auth and CORS simple.
- If fan-out ever needs to scale apart from the UI, splitting it back out is
  a move of one server route, not a rewrite.
