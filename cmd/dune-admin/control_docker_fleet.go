package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
)

// ── Red-Blink docker fleet discovery ───────────────────────────────────────────
//
// Red-Blink's dune-awakening-selfhost-docker stack runs each map as its own
// `dune-server-*` container (survival-1, overmap, social hubs, deep desert),
// spawned/killed by a dune-autoscaler, with a dune-director coordinating the
// battlegroup. GetStatus discovers those containers and decorates each with the
// live player/queue/dimension data the director exposes, so the Battlegroup tab
// shows per-map rows instead of a single gameserver's status.

var (
	// dockerServerNameRe matches a safe dune-server-* container name to
	// interpolate into a docker command (no shell metacharacters).
	dockerServerNameRe = regexp.MustCompile(`^dune-server-[a-zA-Z0-9._-]+$`)
	// dockerOrdinalRe strips a trailing autoscaler ordinal ("-3") from a map
	// token so sh-arrakeen-1 and sh-arrakeen-2 collapse to the same display map.
	dockerOrdinalRe = regexp.MustCompile(`-\d+$`)
)

// isValidDockerServerName reports whether name is a dune-server-* container name
// safe to use in a lifecycle command. Used by both the fleet listing and the
// per-server exec endpoint.
func isValidDockerServerName(name string) bool {
	return len(name) > 0 && len(name) <= 128 && dockerServerNameRe.MatchString(name)
}

// alwaysOnMapTokens are the protected, non-autoscaled map families. Everything
// else is managed by the dune-autoscaler (a Stop will be undone).
var alwaysOnMapTokens = []string{"survival", "overmap", "gateway"}

// mapNameFromContainer derives a stable display map name from a dune-server-*
// container name. The autoscaler ordinal is stripped so dynamic instances of the
// same map share a display name; the unique row key remains the container name.
func mapNameFromContainer(container string) string {
	token := strings.TrimPrefix(container, "dune-server-")
	token = dockerOrdinalRe.ReplaceAllString(token, "")
	switch strings.ToLower(token) {
	case "survival":
		return "Survival"
	case "overmap":
		return "Overmap"
	case "gateway":
		return "Gateway"
	case "sh-arrakeen", "arrakeen":
		return "SH_Arrakeen"
	case "deepdesert", "deep-desert":
		return "DeepDesert"
	default:
		return prettifyMapToken(token)
	}
}

// prettifyMapToken upper-cases each "-"/"_"-separated segment of an unknown map
// token (e.g. "new-map" → "New_Map") as a best-effort display fallback.
func prettifyMapToken(token string) string {
	if token == "" {
		return "unknown"
	}
	parts := strings.FieldsFunc(token, func(r rune) bool { return r == '-' || r == '_' })
	if len(parts) == 0 {
		return token
	}
	for i, p := range parts {
		if p != "" {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, "_")
}

// isAutoscaledMap reports whether a container is managed by the dune-autoscaler
// (true for anything outside the always-on map families).
func isAutoscaledMap(container string) bool {
	token := strings.ToLower(strings.TrimPrefix(container, "dune-server-"))
	for _, on := range alwaysOnMapTokens {
		if token == on || strings.HasPrefix(token, on+"-") {
			return false
		}
	}
	return true
}

// dockerServerContainer is a discovered dune-server-* container.
type dockerServerContainer struct {
	name    string
	running bool
	status  string // raw `docker ps` status text, e.g. "Up 2 hours" / "Exited (0) ..."
}

// dockerGameProc holds the parsed in-container game process for a container.
type dockerGameProc struct {
	proc ampGameProcess
	age  int
	ok   bool
}

// listDockerServerContainers runs `docker ps -a` and returns every dune-server-*
// container except the gateway (a router, not a map). Stopped containers are
// included so a crashed/halted map still appears and can be started.
func (c *dockerControl) listDockerServerContainers(exec Executor) ([]dockerServerContainer, error) {
	out, err := exec.Exec("docker ps -a --format '{{.Names}}\\t{{.Status}}' 2>&1")
	if err != nil {
		return nil, fmt.Errorf("docker ps: %w", err)
	}
	var cs []dockerServerContainer
	for _, line := range splitLines(out) {
		parts := strings.SplitN(line, "\t", 2)
		name := strings.TrimSpace(parts[0])
		if !strings.HasPrefix(name, "dune-server-") || name == "dune-server-gateway" {
			continue
		}
		status := ""
		if len(parts) == 2 {
			status = strings.TrimSpace(parts[1])
		}
		cs = append(cs, dockerServerContainer{
			name:    name,
			running: strings.HasPrefix(status, "Up"),
			status:  status,
		})
	}
	return cs, nil
}

// parseDockerGameLine parses one `ps -eo pid,etimes,args` line into the game
// process and its uptime seconds, reusing parseAMPGameProcess for the args.
func parseDockerGameLine(line string) (ampGameProcess, int, bool) {
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return ampGameProcess{}, 0, false
	}
	age, _ := strconv.Atoi(fields[1])
	// Reconstruct a "pid args..." line (dropping etimes) for parseAMPGameProcess.
	reconstructed := fields[0] + " " + strings.Join(fields[2:], " ")
	proc, ok := parseAMPGameProcess(reconstructed)
	return proc, age, ok
}

// dockerGameProcFor returns the DuneSandboxServer process inside a container,
// best-effort. A stopped container or missing process yields ok=false.
func dockerGameProcFor(exec Executor, container string) dockerGameProc {
	out, err := exec.Exec(fmt.Sprintf(
		"docker exec %s ps -eo pid,etimes,args --no-headers 2>/dev/null | grep DuneSandboxServer-Linux-Shipping | grep -v grep",
		shellQuote(container)))
	if err != nil && strings.TrimSpace(out) == "" {
		return dockerGameProc{}
	}
	for _, line := range splitLines(out) {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if proc, age, ok := parseDockerGameLine(line); ok {
			return dockerGameProc{proc: proc, age: age, ok: true}
		}
	}
	return dockerGameProc{}
}

// fetchDockerDirectorPartitions returns per-partition director metadata. It uses
// the configured director_url over the executor tunnel when set, otherwise curls
// the director from inside its container (default dune-director). Best-effort:
// nil with no error means "no director data".
func (c *dockerControl) fetchDockerDirectorPartitions(ctx context.Context, exec Executor) (map[int]partitionMeta, error) {
	if c.directorURL != "" {
		return fetchDirectorPartitionsVia(ctx, exec, c.directorURL)
	}
	container := c.directorContainer
	if container == "" {
		container = "dune-director"
	}
	out, err := exec.Exec(fmt.Sprintf(
		"docker exec %s curl -s --max-time 3 http://127.0.0.1:11717/v0/battlegroup 2>/dev/null",
		shellQuote(container)))
	if err != nil {
		return nil, fmt.Errorf("query director container %s: %w", container, err)
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return nil, nil
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, fmt.Errorf("decode director response: %w", err)
	}
	meta := map[int]partitionMeta{}
	collectPartitions(raw, meta)
	return meta, nil
}

// dockerServerPhase maps a container's running state + process presence to a
// display phase.
func dockerServerPhase(running, hasProc bool, status string) string {
	if running {
		if hasProc {
			return "Running"
		}
		return "Starting"
	}
	if fields := strings.Fields(status); len(fields) > 0 {
		return fields[0] // "Exited", "Created", "Restarting", …
	}
	return "Stopped"
}

// buildServerRows assembles the per-map rows from the discovered containers and
// the enrichment sources. It is pure (no I/O) so the merge logic — container row
// keys, director enrichment, and the partition-keyed DB fallback — is testable.
// Director metadata wins; the DB count is only used when the director gave none.
func buildServerRows(containers []dockerServerContainer, procs map[string]dockerGameProc, dirMeta map[int]partitionMeta, dbCounts map[int]int) []ServerRow {
	rows := make([]ServerRow, 0, len(containers))
	for _, ct := range containers {
		gp := procs[ct.name]
		row := ServerRow{
			Map:        mapNameFromContainer(ct.name),
			Container:  ct.name,
			Autoscaled: isAutoscaledMap(ct.name),
			Ready:      ct.running && gp.ok,
			Phase:      dockerServerPhase(ct.running, gp.ok, ct.status),
		}
		if gp.ok {
			row.Partition = gp.proc.partition
			row.Port = gp.proc.port
			row.AgeSeconds = gp.age
		}
		if meta, ok := dirMeta[row.Partition]; ok {
			row.Dimension = meta.dimension
			row.Players = meta.players
			row.PlayerHardCap = meta.playerHardCap
			row.Queue = meta.queue
			if meta.label != "" {
				row.Sietch = meta.label
			}
		} else if n, ok := dbCounts[row.Partition]; ok {
			row.Players = n
		}
		rows = append(rows, row)
	}
	return rows
}

// dockerFleetPhase summarises the battlegroup phase from the per-map rows.
func dockerFleetPhase(servers []ServerRow) string {
	if len(servers) == 0 {
		return "No servers"
	}
	for _, s := range servers {
		if s.Ready {
			return "Running"
		}
	}
	return "Stopped"
}

// GetStatus discovers the dune-server-* fleet and enriches each map with live
// director data (falling back to DB online-player counts keyed on partition_id
// when the director is unreachable). Replaces the single-container stub.
func (c *dockerControl) GetStatus(ctx context.Context, exec Executor) (*BattlegroupStatus, error) {
	containers, err := c.listDockerServerContainers(exec)
	if err != nil {
		return nil, err
	}
	procs := make(map[string]dockerGameProc, len(containers))
	for _, ct := range containers {
		if ct.running {
			procs[ct.name] = dockerGameProcFor(exec, ct.name)
		}
	}

	dirMeta, derr := c.fetchDockerDirectorPartitions(ctx, exec)
	if derr != nil {
		log.Printf("dockerControl.GetStatus: director enrichment unavailable: %v", derr)
	}

	// DB fallback only when the director gave nothing — keyed on partition_id,
	// NOT map name (dune.actors.map stores the upgraded engine name).
	var dbCounts map[int]int
	if len(dirMeta) == 0 && globalDB != nil {
		if counts, cerr := cmdFetchOnlinePlayersByPartition(ctx, globalDB); cerr == nil {
			dbCounts = counts
		} else {
			log.Printf("dockerControl.GetStatus: DB player fallback unavailable: %v", cerr)
		}
	}

	servers := buildServerRows(containers, procs, dirMeta, dbCounts)
	dbPhase := "Disconnected"
	if globalDB != nil {
		dbPhase = "Connected"
	}
	name := c.gameserver
	if name == "" {
		name = "docker"
	}
	return &BattlegroupStatus{
		Name:     name,
		Title:    "Docker Managed",
		Phase:    dockerFleetPhase(servers),
		Database: dbPhase,
		Servers:  servers,
	}, nil
}

// ── per-server lifecycle ───────────────────────────────────────────────────────

// ServerLifecycle is an optional control-plane capability: start/stop/restart of
// an individual game-server container. Only the docker fleet implements it; the
// per-server endpoint type-asserts for it and returns 501 otherwise.
type ServerLifecycle interface {
	ExecServerCommand(ctx context.Context, exec Executor, container, cmd string) (string, error)
}

// ExecServerCommand runs a lifecycle command against a single dune-server-*
// container. The name is strictly validated; the caller (handleBGServerExec)
// additionally checks it against the discovered fleet before dispatching here.
func (c *dockerControl) ExecServerCommand(_ context.Context, exec Executor, container, cmd string) (string, error) {
	if !isValidDockerServerName(container) {
		return "", fmt.Errorf("invalid container name %q", container)
	}
	var dockerCmd string
	switch cmd {
	case "start":
		dockerCmd = fmt.Sprintf("docker start %s 2>&1", shellQuote(container))
	case "stop":
		dockerCmd = fmt.Sprintf("docker stop %s 2>&1", shellQuote(container))
	case "restart":
		dockerCmd = fmt.Sprintf("docker restart %s 2>&1", shellQuote(container))
	default:
		return "", fmt.Errorf("docker control does not support server command %q", cmd)
	}
	out, err := exec.Exec(dockerCmd)
	if err != nil {
		return out, fmt.Errorf("docker %s %s: %w — %s", cmd, container, err, strings.TrimSpace(out))
	}
	return out, nil
}
