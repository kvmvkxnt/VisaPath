# agent

Runs on each monitored host (systemd, not a container, ADR 0004), reads
logs, turns lines into `Event`s and ships them to `ingest`. Today it has
the parsers and the file tailer; the shipper comes next.

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

## Test

```sh
go test -race ./...
```
