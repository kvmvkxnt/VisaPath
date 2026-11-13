// Package docker collects container lifecycle events from the Docker daemon.
package docker

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	sentinelv1 "github.com/kvmvkxnt/sentinel/proto/gen/go/sentinel/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// NOTE: exec_* is left out on purpose. Every container healthcheck is an
// exec, so it would be most of the stream. Worth adding once events carry
// enough to tell a healthcheck from a person.
var actions = map[string]bool{
	"create": true, "start": true, "die": true, "oom": true, "kill": true, "destroy": true,
}

type message struct {
	Type   string
	Action string
	Actor  struct {
		ID         string
		Attributes map[string]string
	}
	TimeNano int64 `json:"timeNano"`
}

// Follow streams container events from the daemon behind the unix socket
// and calls emit for each until ctx is done. When the stream breaks (Docker
// restarts, or is not up yet) it reconnects after retry and asks only for
// events newer than the last one it saw.
func Follow(ctx context.Context, socket string, retry time.Duration, emit func(*sentinelv1.Event)) {
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
	}}
	names := make([]string, 0, len(actions))
	for a := range actions {
		names = append(names, a)
	}
	filters, _ := json.Marshal(map[string][]string{"type": {"container"}, "event": names})

	since := time.Now().UnixNano()
	for ctx.Err() == nil {
		err := stream(ctx, client, string(filters), &since, emit)
		if ctx.Err() != nil {
			return
		}
		slog.Warn("docker event stream ended, reconnecting", "in", retry, "err", err)
		select {
		case <-ctx.Done():
		case <-time.After(retry):
		}
	}
}

// stream reads one connection until it ends. since is moved past every
// event handed to emit.
func stream(ctx context.Context, client *http.Client, filters string, since *int64, emit func(*sentinelv1.Event)) error {
	q := url.Values{
		"filters": {filters},
		// Docker takes seconds with a nanosecond fraction.
		"since": {fmt.Sprintf("%d.%09d", *since/1e9, *since%1e9)},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/events?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("docker answered %s", resp.Status)
	}

	lines := bufio.NewScanner(resp.Body)
	for lines.Scan() {
		var m message
		// The filter is a request, not a promise: check again.
		if json.Unmarshal(lines.Bytes(), &m) != nil || m.Type != "container" || !actions[m.Action] {
			continue
		}
		code, _ := strconv.ParseInt(m.Actor.Attributes["exitCode"], 10, 32)
		emit(&sentinelv1.Event{
			Time: timestamppb.New(time.Unix(0, m.TimeNano)),
			Payload: &sentinelv1.Event_Docker{Docker: &sentinelv1.DockerEvent{
				Action:        m.Action,
				ContainerId:   m.Actor.ID,
				ContainerName: m.Actor.Attributes["name"],
				Image:         m.Actor.Attributes["image"],
				ExitCode:      int32(code),
			}},
		})
		*since = m.TimeNano + 1
	}
	return lines.Err()
}
