// Package parse turns raw log lines into events. Everything it reads is
// attacker-controlled: parsers copy text into fields and never act on it.
package parse

import (
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"

	sentinelv1 "github.com/kvmvkxnt/sentinel/proto/gen/go/sentinel/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Func parses one log line. It returns false for lines that are not an
// event: other formats, noise, truncated writes. now is only used by
// formats whose timestamps lack a year or zone.
type Func func(line string, now time.Time) (*sentinelv1.Event, bool)

// Nginx escapes '"' inside variables as \x22, so a quote always ends a field.
var nginxRE = regexp.MustCompile(`^(\S+) \S+ \S+ \[([^\]]+)\] "([^"]*)" (\d{3}) (\d+) "([^"]*)" "([^"]*)"$`)

// Nginx parses a line of the Nginx "combined" access log format.
func Nginx(line string, _ time.Time) (*sentinelv1.Event, bool) {
	m := nginxRE.FindStringSubmatch(line)
	if m == nil {
		return nil, false
	}
	ip, ok := srcIP(m[1])
	if !ok {
		return nil, false
	}
	at, err := time.Parse("02/Jan/2006:15:04:05 -0700", m[2])
	if err != nil {
		return nil, false
	}
	status, _ := strconv.ParseUint(m[4], 10, 32)
	sent, err := strconv.ParseUint(m[5], 10, 64)
	if err != nil {
		return nil, false
	}
	n := &sentinelv1.NginxAccess{
		Status:    uint32(status),
		BytesSent: sent,
		Referer:   dash(m[6]),
		UserAgent: dash(m[7]),
	}
	// Scanners send request lines that are not "METHOD PATH PROTOCOL".
	// Those are kept whole in Path, because they are the interesting ones.
	if parts := strings.Split(m[3], " "); len(parts) == 3 {
		n.Method, n.Path, n.Protocol = parts[0], parts[1], parts[2]
	} else {
		n.Path = m[3]
	}
	return &sentinelv1.Event{
		Time:    timestamppb.New(at),
		SrcIp:   ip.String(),
		Payload: &sentinelv1.Event_Nginx{Nginx: n},
	}, true
}

// srcIP parses a client address. IPv4-mapped IPv6 (::ffff:1.2.3.4) becomes
// plain IPv4, so one client is one address whatever socket it came in on.
func srcIP(s string) (netip.Addr, bool) {
	ip, err := netip.ParseAddr(s)
	return ip.Unmap(), err == nil
}

// dash maps Nginx's placeholder for an empty value to an empty string.
func dash(s string) string {
	if s == "-" {
		return ""
	}
	return s
}

var (
	// Timestamp, host, then the sshd tag. Requiring the tag keeps other
	// programs that log client-supplied text from forging sshd events.
	syslogRE = regexp.MustCompile(`^(\w{3} [ \d]\d \d\d:\d\d:\d\d|\d{4}-\S+) \S+ sshd(?:-session)?\[\d+\]: (.*)$`)

	// WARNING: the user name is chosen by the client and may contain
	// " from 1.2.3.4 port 5". The greedy (.*) makes the last "from" win,
	// which is the one sshd itself wrote. Do not make it lazy.
	sshFailedRE   = regexp.MustCompile(`^Failed (\S+) for (?:invalid user )?(.*) from (\S+) port (\d+) ssh2(?:: .*)?$`)
	sshAcceptedRE = regexp.MustCompile(`^Accepted (\S+) for (.*) from (\S+) port (\d+) ssh2(?:: .*)?$`)
	sshInvalidRE  = regexp.MustCompile(`^Invalid user (.*) from (\S+) port (\d+)$`)
)

// SSH parses an sshd authentication line in syslog format, with either a
// classic ("Nov 13 10:00:01") or an RFC 3339 timestamp.
//
// "Invalid user" and "Failed password for invalid user" are separate
// events: the first is logged once per connection, the second once per
// guess.
func SSH(line string, now time.Time) (*sentinelv1.Event, bool) {
	m := syslogRE.FindStringSubmatch(line)
	if m == nil {
		return nil, false
	}
	at, ok := syslogTime(m[1], now)
	if !ok {
		return nil, false
	}
	msg := m[2]

	auth := &sentinelv1.SshAuth{}
	var ip, port string
	if f := sshFailedRE.FindStringSubmatch(msg); f != nil {
		auth.Outcome, auth.Method, auth.User, ip, port = sentinelv1.SshAuth_OUTCOME_FAILED, f[1], f[2], f[3], f[4]
	} else if f := sshAcceptedRE.FindStringSubmatch(msg); f != nil {
		auth.Outcome, auth.Method, auth.User, ip, port = sentinelv1.SshAuth_OUTCOME_ACCEPTED, f[1], f[2], f[3], f[4]
	} else if f := sshInvalidRE.FindStringSubmatch(msg); f != nil {
		auth.Outcome, auth.User, ip, port = sentinelv1.SshAuth_OUTCOME_INVALID_USER, f[1], f[2], f[3]
	} else {
		return nil, false
	}

	addr, ok := srcIP(ip)
	if !ok {
		return nil, false
	}
	p, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return nil, false
	}
	auth.SrcPort = uint32(p)
	return &sentinelv1.Event{
		Time:    timestamppb.New(at),
		SrcIp:   addr.String(),
		Payload: &sentinelv1.Event_Ssh{Ssh: auth},
	}, true
}

func syslogTime(s string, now time.Time) (time.Time, bool) {
	if at, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return at, true
	}
	// Classic syslog: no year, no zone. Assume the host's zone and the
	// current year, unless that lands in the future.
	at, err := time.ParseInLocation("Jan _2 15:04:05", s, now.Location())
	if err != nil {
		return time.Time{}, false
	}
	at = at.AddDate(now.Year(), 0, 0)
	if at.After(now.Add(24 * time.Hour)) {
		at = at.AddDate(-1, 0, 0)
	}
	return at, true
}
