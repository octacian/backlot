package native

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLegacyAuthorityWireAndPolicyFence(t *testing.T) {
	old := `{"pid":42,"birth":"original","control":"/private/owned/control.sock","token":"` + strings.Repeat("a", 64) + `","boot":"00000000-0000-0000-0000-000000000001"}`
	var id Identity
	if err := json.Unmarshal([]byte(old), &id); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(id)
	if err != nil || string(encoded) != old {
		t.Fatal("legacy tuple changed; old claim hash/wire would drift", err)
	}
	for _, extra := range []string{`,"protocol":0`, `,"protocol":2,"purpose":"strict"`, `,"Protocol":99`, `,"purpose":"application"`, `,"component":"server"`} {
		candidate := strings.TrimSuffix(old, "}") + extra + "}"
		if json.Unmarshal([]byte(candidate), &id) == nil {
			t.Fatal("unknown authority downgraded to legacy", extra)
		}
	}
	proof := `{"Identity":` + old + `,"Error":""}`
	for _, invalid := range []string{strings.TrimSuffix(proof, "}") + `,"Result":null}`, proof + ` {}`, `null`} {
		var p legacyCleanupProof
		if decodeLegacyProof(strings.NewReader(invalid), &p) == nil {
			t.Fatal("non-legacy receipt accepted")
		}
	}
	var p legacyCleanupProof
	if err := decodeLegacyProof(strings.NewReader(proof), &p); err != nil {
		t.Fatal(err)
	}
}

func TestUnknownAuthorityDoesNotReadReceipt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "receipt.json")
	original := []byte("unrelated malformed marker")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	for _, id := range []Identity{
		{Protocol: 3, Purpose: "strict", PID: 42, Birth: "unknown", Token: strings.Repeat("a", 64), Control: filepath.Join(dir, "control.sock")},
		{Protocol: 0, Purpose: "application", PID: 42, Birth: "unknown", Token: strings.Repeat("a", 64), Control: filepath.Join(dir, "control.sock")},
	} {
		if _, err := StopResult(id, 0); err == nil || !strings.Contains(err.Error(), "authority") && !strings.Contains(err.Error(), "guardian") {
			t.Fatal("unknown policy accepted", err)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(original, after) {
		t.Fatal("unrelated receipt modified", err)
	}
}

func TestHistoricalAuthorityGapsRemainReadableAndFenced(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "bl-legacy-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	socket := filepath.Join(root, "unrelated.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	for _, missing := range []string{"birth", "token", "invalid_boot"} {
		old := map[string]any{"pid": 42, "birth": "original", "token": strings.Repeat("a", 64), "control": socket}
		if missing == "invalid_boot" {
			old["boot"] = "historically-unverifiable"
		} else {
			delete(old, missing)
		}
		data, err := json.Marshal(old)
		if err != nil {
			t.Fatal(err)
		}
		var id Identity
		if err := json.Unmarshal(data, &id); err != nil {
			t.Fatal("historical uncertainty became a corrupt format", missing, err)
		}
		result, err := StopResult(id, 0)
		if err == nil || result != nil {
			t.Fatal("missing authority fabricated cleanup/session result", missing, err)
		}
	}
	if err := listener.SetDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	conn, err := listener.AcceptUnix()
	if conn != nil {
		_ = conn.Close()
		t.Fatal("unknown effect contacted arbitrary Control before validation")
	}
	if err == nil {
		t.Fatal("unknown effect supplied authority")
	}
}
