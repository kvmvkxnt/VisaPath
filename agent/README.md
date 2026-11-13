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
| `Xray`  | Xray access log | Accepted and rejected connections, with or without the `from` and `tcp:` prefixes. Timestamps are taken in the host's zone. |

Log text is attacker-controlled. The SSH parser takes the address from the
end of the line, because the user name in the middle is chosen by the
client and can contain a fake `from 1.2.3.4 port 5`.

Hosts that only log to journald (no `auth.log`) are not covered yet.

## Docker events

`internal/docker` streams container lifecycle events (`create`, `start`,
`die`, `oom`, `kill`, `destroy`) from the Docker socket and reconnects
when the daemon restarts. `exec` events are skipped: every healthcheck is
one.

It is off unless `AGENT_DOCKER_SOCKET` is set, because reading the socket
needs the `docker` group, which is close to root (ADR 0004). The systemd
unit does not allow it by default; enable it with a drop-in
(`systemctl edit sentinel-agent`):

```ini
[Service]
SupplementaryGroups=adm docker
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
```

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
| `AGENT_XRAY_LOG`    | Xray access log to follow, e.g. `/var/log/xray/access.log`. Optional. |
| `AGENT_DOCKER_SOCKET` | Docker socket for container events, e.g. `/var/run/docker.sock`. Optional, see above. |

At least one source must be set.

## Install on a host

On the Sentinel server, build the binary and issue the host's certificate:

```sh
make agent                                  # dist/sentinel-agent-linux-amd64
docker compose run --rm certs agent web-1   # deploy/certs/agents/web-1.{pem,key}
```

Copy the binary, `web-1.pem`, `web-1.key`, `deploy/certs/ca.pem` and the
two files in `deploy/agent/` to the host, then as root there:

```sh
useradd --system --no-create-home --shell /usr/sbin/nologin sentinel
install -m 0755 sentinel-agent-linux-amd64 /usr/local/bin/sentinel-agent
install -d -m 0750 -g sentinel /etc/sentinel
install -m 0640 -g sentinel web-1.pem /etc/sentinel/agent.pem
install -m 0640 -g sentinel web-1.key /etc/sentinel/agent.key
install -m 0640 -g sentinel ca.pem /etc/sentinel/ca.pem
install -m 0640 -g sentinel agent.env.example /etc/sentinel/agent.env   # then edit
install -m 0644 sentinel-agent.service /etc/systemd/system/
systemctl daemon-reload && systemctl enable --now sentinel-agent
```

The unit runs as the `sentinel` user with no capabilities and a read-only
view of the system (ADR 0004). It reads logs through the `adm` group,
which owns `/var/log/auth.log` and the Nginx logs on Debian and Ubuntu.

## Run and test

```sh
go run ./cmd/sentinel-agent
```

```sh
go test -race ./...
```
