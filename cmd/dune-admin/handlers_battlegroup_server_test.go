package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// scriptedDockerExec returns an fnExecutor that answers the docker calls
// GetStatus makes: `docker ps -a`, per-container `ps`, and the director curl.
// Only dune-server-overmap is surfaced.
func scriptedDockerExec(captured *string) *fnExecutor {
	return &fnExecutor{fn: func(cmd string) (string, error) {
		switch {
		case strings.Contains(cmd, "docker restart"), strings.Contains(cmd, "docker stop"), strings.Contains(cmd, "docker start"):
			if captured != nil {
				*captured = cmd
			}
			return "done", nil
		case strings.Contains(cmd, "curl"):
			return directorBattlegroupJSON, nil
		case strings.Contains(cmd, "docker exec"):
			if strings.Contains(cmd, "dune-server-overmap") {
				return dockerPsLine(1001, 7200, "Overmap", 7794, 2), nil
			}
			return "", nil
		case strings.Contains(cmd, "docker ps"):
			return "dune-server-overmap\tUp 2 hours", nil
		}
		return "", nil
	}}
}

func bgServerReq(body string) (*httptest.ResponseRecorder, *http.Request) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/battlegroup/servers/exec", strings.NewReader(body))
	return httptest.NewRecorder(), req
}

func withDockerGlobals(t *testing.T, exec Executor) {
	t.Helper()
	origCtl, origExec := globalControl, globalExecutor
	t.Cleanup(func() { globalControl, globalExecutor = origCtl, origExec })
	globalControl = &dockerControl{directorContainer: "dune-director"}
	globalExecutor = exec
}

func TestHandleBGServerExec_Success(t *testing.T) {
	var captured string
	withDockerGlobals(t, scriptedDockerExec(&captured))

	rr, req := bgServerReq(`{"container":"dune-server-overmap","cmd":"restart"}`)
	handleBGServerExec(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(captured, "docker restart") || !strings.Contains(captured, "dune-server-overmap") {
		t.Errorf("did not issue docker restart for the container: %q", captured)
	}
}

func TestHandleBGServerExec_UnknownCommand(t *testing.T) {
	withDockerGlobals(t, scriptedDockerExec(nil))
	rr, req := bgServerReq(`{"container":"dune-server-overmap","cmd":"explode"}`)
	handleBGServerExec(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rr.Code)
	}
}

func TestHandleBGServerExec_InvalidContainerName(t *testing.T) {
	withDockerGlobals(t, scriptedDockerExec(nil))
	rr, req := bgServerReq(`{"container":"dune-server-x; rm -rf /","cmd":"restart"}`)
	handleBGServerExec(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rr.Code)
	}
}

func TestHandleBGServerExec_NotAMember(t *testing.T) {
	withDockerGlobals(t, scriptedDockerExec(nil))
	// Valid name, but not among the surfaced dune-server-* containers.
	rr, req := bgServerReq(`{"container":"dune-server-ghost-9","cmd":"restart"}`)
	handleBGServerExec(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404 (membership), got %d", rr.Code)
	}
}

func TestHandleBGServerExec_UnsupportedControlPlane(t *testing.T) {
	origCtl := globalControl
	t.Cleanup(func() { globalControl = origCtl })
	globalControl = &localControl{} // does not implement ServerLifecycle

	rr, req := bgServerReq(`{"container":"dune-server-overmap","cmd":"restart"}`)
	handleBGServerExec(rr, req)
	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("want 501, got %d", rr.Code)
	}
}

func TestHandleBGServerExec_NotConnected(t *testing.T) {
	origCtl := globalControl
	t.Cleanup(func() { globalControl = origCtl })
	globalControl = nil

	rr, req := bgServerReq(`{"container":"dune-server-overmap","cmd":"restart"}`)
	handleBGServerExec(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", rr.Code)
	}
}
