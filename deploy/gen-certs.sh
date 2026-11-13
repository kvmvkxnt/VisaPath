#!/bin/sh
# Creates the private CA and certificates for mTLS between agents and ingest.
#
#   gen-certs.sh init           CA and ingest certificate, if missing
#   gen-certs.sh agent <host>   certificate for the agent on <host>
#
# Runs inside the `certs` compose service; files land in deploy/certs.
# INGEST_SANS adds names agents use to reach ingest, e.g.
# "DNS:ingest.example.com,IP:203.0.113.10".
set -eu

dir=${CERTS_DIR:-/certs}
days=825
mkdir -p "$dir/agents"
cd "$dir"

sign() { # sign <name> <common name> <extensions>
    openssl req -new -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes \
        -keyout "$1.key" -out "$1.csr" -subj "/CN=$2"
    printf '%s\n' "$3" > "$1.ext"
    openssl x509 -req -in "$1.csr" -CA ca.pem -CAkey ca.key -CAcreateserial \
        -days "$days" -extfile "$1.ext" -out "$1.pem"
    rm "$1.csr" "$1.ext"
    chmod 600 "$1.key"
}

case ${1:-} in
init)
    if [ ! -f ca.pem ]; then
        openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes \
            -keyout ca.key -out ca.pem -days 3650 -subj "/CN=sentinel ca"
        chmod 600 ca.key
        echo "created CA"
    fi
    if [ ! -f ingest.pem ]; then
        sans="DNS:ingest,DNS:localhost,IP:127.0.0.1${INGEST_SANS:+,$INGEST_SANS}"
        sign ingest ingest "subjectAltName=$sans
extendedKeyUsage=serverAuth"
        # The ingest container runs as this unprivileged user.
        chown 65532:65532 ingest.key
        echo "created ingest certificate for $sans"
    fi
    ;;
agent)
    host=${2:?usage: gen-certs.sh agent <host>}
    case $host in *[!A-Za-z0-9._-]*) echo "host may only contain letters, digits, '.', '_' and '-'" >&2; exit 1 ;; esac
    [ -f ca.pem ] || { echo "no CA yet, run init first" >&2; exit 1; }
    sign "agents/$host" "$host" "extendedKeyUsage=clientAuth"
    echo "created agents/$host.pem and agents/$host.key"
    ;;
*)
    echo "usage: gen-certs.sh init | agent <host>" >&2
    exit 1
    ;;
esac
