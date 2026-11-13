# agent

Runs on each monitored host (systemd, not a container, ADR 0004), reads
logs, turns lines into `Event`s and ships them to `ingest` over gRPC with
mTLS. Executing approved actions (nftables) comes later.

## Parsers

`internal/parse` holds one function per log format. Each returns false for
lines that are not an event.

| Parser  | Reads | Notes |
|---------|-------|-------|
| `Nginx` | access log, `combined` format | Request lines that are not `METHOD PATH PROTOCOL` are kept whole in `path`. |
| `SSH`   | sshd lines in `/var/log/auth.log` | Classic and RFC 3339 syslog timestamps, `sshd` and `sshd-session` tags. |

Log text is attacker-controlled. The SSH parser takes the address from the
end of the line, because the user name in the middle is chosen by the
client and can contain a fake `from 1.2.3.4 port 5`.

Hosts that only log to journald (no `auth.log`) are not covered yet.

## Tailer

`internal/tail.Follow` is `tail -F` in stdlib Go: it polls, starts at the
end of the file, waits for a missing file, and follows rotation (rename
and recreate) and truncation (`copytruncate`). Lines longer than 64 KiB
are dropped. There is no saved offset yet, so lines written while the
agent is down are skipped.

## Shipper

`internal/ship` sends a batch when it reaches 500 events or once a second.
If ingest is unreachable it retries the same batch with backoff (1s
doubling to 30s) and stops reading new lines, so the backlog waits in the
log files, not in memory. A batch ingest rejects as `InvalidArgument` is
dropped and logged. On SIGTERM it tries once more to flush.

## Configuration

Read from the environment (a systemd `EnvironmentFile` on a real host):

| Key                 | Meaning |
|---------------------|---------|
| `AGENT_INGEST_ADDR` | `host:port` of ingest. The host must match the server certificate. |
| `AGENT_TLS_CERT`    | This host's certificate, PEM. Its common name is the host name ingest records. |
| `AGENT_TLS_KEY`     | Its private key, PEM. |
| `AGENT_TLS_CA`      | CA that signed the ingest certificate, PEM. |
| `AGENT_NGINX_LOG`   | Nginx access log to follow. Optional. |
| `AGENT_SSH_LOG`     | sshd auth log to follow, e.g. `/var/log/auth.log`. Optional. |

At least one of the two logs must be set.

## Run and test

```sh
go run ./cmd/sentinel-agent
```

```sh
go test -race ./...
```
