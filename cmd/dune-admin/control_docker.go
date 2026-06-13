package main

import (
	"context"
	"fmt"
	"strings"
)

// dockerControl implements ControlPlane using the Docker CLI.
// It requires configured container names and expects the Docker socket to be
// accessible by the executor (locally or via SSH to a Docker host).
type dockerControl struct {
	gameserver  string // container name for the game server
	brokerGame  string // container name for mq-game broker
	brokerAdmin string // container name for mq-admin broker
	// directorURL, when set, is the Battlegroup Director HTTP endpoint reached
	// through the executor (e.g. http://127.0.0.1:11717 on a host-networked
	// stack). When empty, GetStatus curls the director from inside
	// directorContainer instead. Both feed per-partition player/queue/dimension.
	directorURL       string
	directorContainer string // director container name (default "dune-director")
}

func (c *dockerControl) Name() string { return "docker" }

// GetStatus is implemented in control_docker_fleet.go — it discovers per-map
// dune-server-* containers rather than reporting a single gameserver.

func (c *dockerControl) ExecCommand(_ context.Context, exec Executor, cmd string) (string, error) {
	if c.gameserver == "" {
		return "", errNotSupported("docker", "ExecCommand (docker_gameserver not configured)")
	}
	var dockerCmd string
	switch cmd {
	case "start":
		dockerCmd = fmt.Sprintf("docker start %s 2>&1", c.gameserver)
	case "stop":
		dockerCmd = fmt.Sprintf("docker stop %s 2>&1", c.gameserver)
	case "restart":
		dockerCmd = fmt.Sprintf("docker restart %s 2>&1", c.gameserver)
	default:
		return "", fmt.Errorf("docker control does not support %q", cmd)
	}
	out, err := exec.Exec(dockerCmd)
	if err != nil {
		return out, fmt.Errorf("docker %s: %w — %s", cmd, err, out)
	}
	return out, nil
}

func (c *dockerControl) ListProcesses(_ context.Context, exec Executor) ([]ProcessInfo, string, error) {
	out, err := exec.Exec("docker ps --format '{{.Names}}\\t{{.Status}}' 2>&1")
	if err != nil {
		return nil, "", fmt.Errorf("docker ps: %w", err)
	}
	var procs []ProcessInfo
	for _, line := range splitLines(out) {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) < 1 || parts[0] == "" {
			continue
		}
		status := ""
		if len(parts) == 2 {
			status = parts[1]
		}
		procs = append(procs, ProcessInfo{Name: parts[0], Status: status})
	}
	return procs, "docker", nil
}

func (c *dockerControl) ListLogSources(_ context.Context, exec Executor) ([]LogSource, error) {
	out, err := exec.Exec("docker ps --format '{{.Names}}' 2>&1")
	if err != nil {
		return nil, fmt.Errorf("docker ps: %w", err)
	}
	var sources []LogSource
	for _, line := range splitLines(out) {
		name := strings.TrimSpace(line)
		if name != "" {
			sources = append(sources, LogSource{Namespace: "docker", Name: name})
		}
	}
	return sources, nil
}

func (c *dockerControl) StreamLog(_ context.Context, exec Executor, _, name string) (<-chan string, func(), error) {
	return exec.Stream(fmt.Sprintf("docker logs -f %s 2>&1", name))
}

func (c *dockerControl) CaptureJWT(_ context.Context, exec Executor) (string, string, error) {
	if c.gameserver == "" {
		return "", "", errNotSupported("docker", "CaptureJWT (docker_gameserver not configured)")
	}
	existingToken, err := exec.Exec(fmt.Sprintf(
		"docker exec %s env 2>/dev/null | grep FuncomLiveServices__ServiceAuthToken | cut -d= -f2-",
		c.gameserver))
	if err != nil || strings.TrimSpace(existingToken) == "" {
		return "", "", fmt.Errorf("read ServiceAuthToken from container: %w", err)
	}
	return buildCaptureJWT(strings.TrimSpace(existingToken))
}

func (c *dockerControl) EvalOnGameBroker(_ context.Context, exec Executor, expr string) (string, error) {
	if c.brokerGame == "" {
		return "", errNotSupported("docker", "EvalOnGameBroker (docker_broker_game not configured)")
	}
	out, err := exec.Exec(fmt.Sprintf(
		"docker exec %s rabbitmqctl eval %s 2>&1",
		c.brokerGame, shellQuote(expr)))
	if err != nil {
		return "", fmt.Errorf("rabbitmqctl eval: %w (output: %s)", err, strings.TrimSpace(out))
	}
	return strings.TrimSpace(out), nil
}

func (c *dockerControl) ReadDefaultINI(_ context.Context, exec Executor, filename string) string {
	if c.gameserver == "" {
		return ""
	}
	pathOut, err := exec.Exec(fmt.Sprintf(
		"docker exec %s find / -name %s -not -path '*/Saved/*' -not -path '*/proc/*' -not -path '*/sys/*' -not -path '*/dev/*' 2>/dev/null | head -1",
		c.gameserver, shellQuote(filename)))
	if err != nil {
		return ""
	}
	p := strings.TrimSpace(pathOut)
	if p == "" {
		return ""
	}
	content, err := exec.Exec(fmt.Sprintf("docker exec %s cat %s 2>/dev/null", c.gameserver, shellQuote(p)))
	if err != nil {
		return ""
	}
	return content
}

func (c *dockerControl) DiscoverIniDir(_ context.Context, _ Executor) (string, error) {
	return "", fmt.Errorf("docker control plane requires server_ini_dir to be set in config")
}
