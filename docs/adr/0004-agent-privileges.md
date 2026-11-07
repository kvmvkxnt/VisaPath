# 0004. Agent runs on the host with minimal privileges

Date: 2026-11-08. Status: accepted.

## Context

The agent reads host log files and Docker events and applies nftables rules.
That rules out running it as a normal Compose service, and running it as
root would make the security tool the juiciest target on the box.

## Decision

Ship the agent as a static binary with a systemd unit that runs as a
dedicated `sentinel` user with only `CAP_NET_ADMIN` (for nftables). Log
access comes from group membership (`adm`, `systemd-journal`) and the Docker
socket from the `docker` group, not from root.

## Consequences

- The agent can block traffic but cannot read arbitrary files or spawn
  privileged processes.
- The `docker` group is close to root on its own. Docker events are an
  opt-in collector; leave it off on hosts where that trade is not worth it.
