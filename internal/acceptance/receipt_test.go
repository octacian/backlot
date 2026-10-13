//go:build darwin && arm64

package acceptance

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestProviderHandoffReceiptRetry(t *testing.T) {
	f := ownerFixture(t, nil)
	upstream := f.upstream(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	d := f.proxy(&harness{t: t}, upstream)
	p := d.path
	name := p.receiptPath()
	if err := os.Mkdir(name, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(name, "owned-marker")
	if err := os.WriteFile(marker, []byte("owned test obstruction"), 0600); err != nil {
		t.Fatal(err)
	}
	markerProof, err := os.Lstat(marker)
	if err != nil {
		t.Fatal(err)
	}
	obstruction, err := os.Lstat(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.handoff(); err == nil || !strings.Contains(err.Error(), "receipt incomplete") {
		t.Fatalf("metadata failure falsely reported complete: %v", err)
	}
	if target, err := os.Readlink(p.endpoint); err != nil || target != upstream {
		t.Fatal("endpoint did not switch before metadata failure")
	}
	if err := p.verifyHandoff(); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "owned test obstruction" {
		t.Fatal("unknown receipt obstruction overwritten")
	}
	if err := removeProvenPath(marker, markerProof); err != nil {
		t.Fatal(err)
	}
	if err := removeProvenPath(name, obstruction); err != nil {
		t.Fatal(err)
	}
	if err := p.handoff(); err != nil {
		t.Fatal("already-upstream retry did not complete metadata", err)
	}
	proof, err := os.Lstat(name)
	if err != nil || proof.Mode().Perm() != 0600 || !proof.Mode().IsRegular() || !os.SameFile(proof, p.receiptProof) {
		t.Fatal("receipt ownership/mode incorrect", err)
	}
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	var receipt struct {
		Endpoint string `json:"endpoint"`
		Upstream string `json:"verified_upstream"`
		Inode    uint64 `json:"link_inode"`
		Device   int32  `json:"link_device"`
		Recovery string `json:"recovery"`
	}
	if json.Unmarshal(data, &receipt) != nil || receipt.Endpoint != p.endpoint || receipt.Upstream != upstream || receipt.Inode != p.linkProof.Sys().(*syscall.Stat_t).Ino || receipt.Device != p.linkProof.Sys().(*syscall.Stat_t).Dev || receipt.Recovery == "" {
		t.Fatal("receipt contents do not describe exact owned handoff")
	}
	if err := p.handoff(); err != nil {
		t.Fatal("verified receipt retry", err)
	}
	if again, err := os.Lstat(name); err != nil || !os.SameFile(proof, again) {
		t.Fatal("successful retry replaced receipt")
	}
	// A replacement remains uncertain even though the endpoint still works.
	backup := name + ".owned"
	if err := os.Rename(name, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte("foreign replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	replacement, err := os.Lstat(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.handoff(); err == nil || !strings.Contains(err.Error(), "uncertain recovery receipt") {
		t.Fatal("replacement falsely accepted", err)
	}
	if data, err := os.ReadFile(name); err != nil || string(data) != "foreign replacement" {
		t.Fatal("replacement overwritten")
	}
	if err := removeProvenPath(name, replacement); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(backup, name); err != nil {
		t.Fatal(err)
	}
	if err := p.handoff(); err != nil {
		t.Fatal(err)
	}
	t.Log("receipt creation failed on owned obstruction without overwrite; already-upstream retry completed exact private receipt; repeated retry retained inode; foreign replacement preserved")
}
