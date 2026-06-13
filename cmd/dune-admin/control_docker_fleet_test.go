package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// dockerPsLine builds a synthetic in-container `ps -eo pid,etimes,args` line for
// a DuneSandboxServer game process (pid, uptime-seconds, map, port, partition).
func dockerPsLine(pid, etimes int, mapName string, port, partition int) string {
	return fmt.Sprintf(
		"%d %d /x/DuneSandboxServer-Linux-Shipping DuneSandbox %s -Port=%d -PartitionIndex=%d",
		pid, etimes, mapName, port, partition)
}

func TestMapNameFromContainer(t *testing.T) {
	t.Parallel()
	tests := []struct {
		container string
		want      string
	}{
		{"dune-server-survival-1", "Survival"},
		{"dune-server-overmap", "Overmap"},
		{"dune-server-gateway", "Gateway"},
		{"dune-server-sh-arrakeen-3", "SH_Arrakeen"},
		{"dune-server-arrakeen-2", "SH_Arrakeen"},
		{"dune-server-deepdesert-1", "DeepDesert"},
		{"dune-server-survival-12", "Survival"}, // multi-digit ordinal stripped
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.container, func(t *testing.T) {
			t.Parallel()
			if got := mapNameFromContainer(tt.container); got != tt.want {
				t.Errorf("mapNameFromContainer(%q) = %q, want %q", tt.container, got, tt.want)
			}
		})
	}
}

func TestIsValidDockerServerName(t *testing.T) {
	t.Parallel()
	valid := []string{
		"dune-server-survival-1", "dune-server-overmap", "dune-server-sh-arrakeen-3",
		"dune-server-deepdesert-1",
	}
	invalid := []string{
		"", "dune-server-", "postgres", "dune-server-x; rm -rf /", "dune-server-../etc",
		"dune-server-x`whoami`", "dune-server-x y", "../dune-server-x", "dune-server-$(id)",
	}
	for _, name := range valid {
		if !isValidDockerServerName(name) {
			t.Errorf("isValidDockerServerName(%q) = false, want true", name)
		}
	}
	for _, name := range invalid {
		if isValidDockerServerName(name) {
			t.Errorf("isValidDockerServerName(%q) = true, want false", name)
		}
	}
}

// TestBuildServerRows verifies the pure assembly logic: container-derived map
// names, container-unique rows, partition/port/age from the parsed process,
// director enrichment, DB-count fallback, autoscaled flags, and stopped servers.
func TestBuildServerRows(t *testing.T) {
	t.Parallel()

	containers := []dockerServerContainer{
		{name: "dune-server-survival-1", running: true, status: "Up 2 hours"},
		{name: "dune-server-arrakeen-3", running: true, status: "Up 10 minutes"},
		{name: "dune-server-deepdesert-1", running: false, status: "Exited (0) 5 minutes ago"},
	}
	procs := map[string]dockerGameProc{
		"dune-server-survival-1": {proc: ampGameProcess{pid: 1, mapName: "Survival_1", port: 7777, partition: 1}, age: 7200, ok: true},
		"dune-server-arrakeen-3": {proc: ampGameProcess{pid: 2, mapName: "SH_Arrakeen", port: 7792, partition: 3}, age: 600, ok: true},
		// deepdesert is stopped → no process
	}
	// No director meta → exercise the DB-count fallback path keyed on partition_id.
	dbCounts := map[int]int{1: 5, 3: 7}

	rows := buildServerRows(containers, procs, nil, dbCounts)
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	byContainer := map[string]ServerRow{}
	for _, r := range rows {
		if r.Container == "" {
			t.Errorf("row for map %q has empty Container (row key)", r.Map)
		}
		byContainer[r.Container] = r
	}

	surv := byContainer["dune-server-survival-1"]
	if surv.Map != "Survival" || surv.Partition != 1 || surv.Port != 7777 || surv.AgeSeconds != 7200 {
		t.Errorf("survival row wrong: %+v", surv)
	}
	if surv.Players != 5 { // from dbCounts fallback
		t.Errorf("survival players = %d, want 5 (DB fallback)", surv.Players)
	}
	if surv.Autoscaled {
		t.Errorf("survival should not be autoscaled")
	}
	if !surv.Ready {
		t.Errorf("running survival should be Ready")
	}

	arr := byContainer["dune-server-arrakeen-3"]
	if arr.Map != "SH_Arrakeen" || arr.Partition != 3 || arr.Players != 7 {
		t.Errorf("arrakeen row wrong: %+v", arr)
	}
	if !arr.Autoscaled {
		t.Errorf("arrakeen-3 should be autoscaled")
	}

	dd := byContainer["dune-server-deepdesert-1"]
	if dd.Map != "DeepDesert" {
		t.Errorf("deepdesert map = %q, want DeepDesert", dd.Map)
	}
	if dd.Ready {
		t.Errorf("stopped deepdesert should not be Ready")
	}
	if dd.Phase == "Running" {
		t.Errorf("stopped deepdesert Phase should not be Running, got %q", dd.Phase)
	}
}

// TestBuildServerRows_DirectorEnrichment verifies director metadata wins over the
// DB fallback and populates dimension/queue/cap/sietch.
func TestBuildServerRows_DirectorEnrichment(t *testing.T) {
	t.Parallel()
	containers := []dockerServerContainer{{name: "dune-server-overmap", running: true, status: "Up 1 hour"}}
	procs := map[string]dockerGameProc{
		"dune-server-overmap": {proc: ampGameProcess{pid: 1, port: 7794, partition: 2}, age: 3600, ok: true},
	}
	dirMeta := map[int]partitionMeta{
		2: {dimension: 0, label: "Overland", players: 9, playerHardCap: 60, queue: 3},
	}
	rows := buildServerRows(containers, procs, dirMeta, map[int]int{2: 1 /* should be ignored */})
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	r := rows[0]
	if r.Players != 9 || r.Queue != 3 || r.PlayerHardCap != 60 || r.Dimension != 0 || r.Sietch != "Overland" {
		t.Errorf("director enrichment wrong: %+v", r)
	}
}

// TestDockerGetStatus_DiscoversAndEnriches drives the full GetStatus path with a
// scripted executor: `docker ps -a` discovery, per-container `ps`, and director
// enrichment via the in-container curl fallback. Gateway and non-dune containers
// are excluded; the director JSON is the shared AMP fixture.
func TestDockerGetStatus_DiscoversAndEnriches(t *testing.T) {
	// Not parallel: GetStatus reads the globalDB package global.
	exec := &fnExecutor{fn: func(cmd string) (string, error) {
		switch {
		case strings.Contains(cmd, "curl"):
			return directorBattlegroupJSON, nil
		case strings.Contains(cmd, "docker exec"):
			switch {
			case strings.Contains(cmd, "dune-server-overmap"):
				return dockerPsLine(1001, 7200, "Overmap", 7794, 2), nil
			case strings.Contains(cmd, "dune-server-survival-1"):
				return dockerPsLine(1002, 7100, "Survival_1", 7777, 1), nil
			case strings.Contains(cmd, "dune-server-arrakeen-3"):
				return dockerPsLine(1003, 600, "SH_Arrakeen", 7792, 3), nil
			case strings.Contains(cmd, "dune-server-deepdesert-1"):
				return dockerPsLine(1004, 120, "DeepDesert_1", 7800, 143), nil
			}
			return "", nil
		case strings.Contains(cmd, "docker ps"):
			return strings.Join([]string{
				"dune-server-overmap\tUp 2 hours",
				"dune-server-survival-1\tUp 2 hours",
				"dune-server-arrakeen-3\tUp 10 minutes",
				"dune-server-deepdesert-1\tUp 2 minutes",
				"dune-server-gateway\tUp 2 hours",
				"dune-postgres\tUp 2 hours",
			}, "\n"), nil
		}
		return "", nil
	}}

	c := &dockerControl{directorContainer: "dune-director"}
	status, err := c.GetStatus(context.Background(), exec)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if len(status.Servers) != 4 {
		t.Fatalf("got %d servers, want 4 (gateway + non-dune excluded): %+v", len(status.Servers), status.Servers)
	}
	by := map[string]ServerRow{}
	for _, r := range status.Servers {
		by[r.Container] = r
	}
	if _, ok := by["dune-server-gateway"]; ok {
		t.Errorf("gateway must be excluded")
	}

	over := by["dune-server-overmap"]
	if over.Map != "Overmap" || over.Partition != 2 || over.Players != 5 || over.Queue != 2 || over.Sietch != "Overland" || over.PlayerHardCap != 60 {
		t.Errorf("overmap enrichment wrong: %+v", over)
	}
	if over.Autoscaled {
		t.Errorf("overmap should not be autoscaled")
	}

	surv := by["dune-server-survival-1"]
	if surv.Map != "Survival" || surv.Partition != 1 || surv.Players != 0 || surv.Autoscaled {
		t.Errorf("survival row wrong (no director meta → 0 players): %+v", surv)
	}

	arr := by["dune-server-arrakeen-3"]
	if arr.Map != "SH_Arrakeen" || arr.Partition != 3 || arr.Players != 7 || !arr.Autoscaled {
		t.Errorf("arrakeen row wrong: %+v", arr)
	}

	dd := by["dune-server-deepdesert-1"]
	if dd.Partition != 143 || dd.Dimension != 1 || dd.Queue != 1 || !dd.Autoscaled {
		t.Errorf("deepdesert row wrong: %+v", dd)
	}
}

func TestDockerExecServerCommand(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		container string
		cmd       string
		wantSub   string // substring expected in the issued docker command
		wantErr   bool
	}{
		{name: "restart", container: "dune-server-overmap", cmd: "restart", wantSub: "docker restart"},
		{name: "stop", container: "dune-server-arrakeen-3", cmd: "stop", wantSub: "docker stop"},
		{name: "start", container: "dune-server-survival-1", cmd: "start", wantSub: "docker start"},
		{name: "bad cmd", container: "dune-server-overmap", cmd: "kill", wantErr: true},
		{name: "bad container", container: "dune-server-x; rm -rf /", cmd: "restart", wantErr: true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var captured string
			exec := &fnExecutor{fn: func(cmd string) (string, error) {
				captured = cmd
				return "ok", nil
			}}
			c := &dockerControl{}
			out, err := c.ExecServerCommand(context.Background(), exec, tt.container, tt.cmd)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got out=%q", out)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(captured, tt.wantSub) || !strings.Contains(captured, tt.container) {
				t.Errorf("issued cmd %q missing %q or container %q", captured, tt.wantSub, tt.container)
			}
		})
	}
}
