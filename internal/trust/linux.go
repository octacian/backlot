//go:build linux

package trust

import (
	"bytes"
	"context"
	"fmt"
	v1 "github.com/octacian/backlot/api/v1"
	"github.com/octacian/backlot/internal/localipc"
	"os"
	"path/filepath"
	"syscall"
)

const linuxSources = "/usr/local/share/ca-certificates"

func source(r *receipt) string {
	return filepath.Join(linuxSources, "backlot-"+r.CA.Fingerprint+".crt")
}

func inspectSource(r *receipt) (bool, error) {
	if err := rootDirectories("/usr", "/usr/local", "/usr/local/share", linuxSources); err != nil {
		return false, err
	}
	info, err := os.Lstat(source(r))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || stat.Uid != 0 || info.Mode().Perm()&0022 != 0 {
		return false, problem("conflicting system CA source preserved")
	}
	data, err := os.ReadFile(source(r))
	if err != nil || !bytes.Equal(data, []byte(r.CA.CertificatePEM)) {
		return false, problem("system CA source bytes changed or conflict; preserved for manual recovery")
	}
	if r.EntryCreated {
		identity, err := sourceIdentity(info)
		if err != nil || r.EntryIdentity == "" || identity != r.EntryIdentity {
			return false, problem("owned system CA source was replaced or lacks exact file identity; preserved for manual recovery")
		}
	}
	return true, nil
}

func install(ctx context.Context, path, staged string, r *receipt) error {
	exists, err := inspectSource(r)
	if err != nil {
		return err
	}
	linkExists, _, err := inspectLink(r)
	if err != nil {
		return err
	}
	r.LinkPreexisting = linkExists
	r.Preexisting = exists || linkExists
	if err = writeReceipt(path, *r); err != nil {
		return err
	}
	if !exists {
		if err = command(ctx, "/usr/bin/install", "-m", "0644", "--", staged, source(r)); err != nil {
			return err
		}
		exists, err = inspectSource(r)
		if err != nil || !exists {
			return problem("new exact CA source could not be verified; effects remain unknown")
		}
		info, err := os.Lstat(source(r))
		if err != nil {
			return problem("new CA source identity unavailable; effects remain unknown")
		}
		r.EntryIdentity, err = sourceIdentity(info)
		if err != nil {
			return err
		}
		r.EntryCreated = true
		if _, err = inspectSource(r); err != nil {
			return err
		}
		if err = writeReceipt(path, *r); err != nil {
			return err
		}
	}
	// Normal distro hooks are part of the explicitly authorized refresh.
	if err = command(ctx, "/usr/sbin/update-ca-certificates"); err != nil {
		return err
	}
	if _, err = inspectSource(r); err != nil {
		return err
	}
	linkExists, identity, err := inspectLink(r)
	if err != nil || !linkExists {
		return problem("generated CA link not exactly verified after refresh; effects remain partial")
	}
	if !r.LinkPreexisting {
		r.LinkCreated = true
		r.LinkIdentity = identity
		if err = writeReceipt(path, *r); err != nil {
			return err
		}
	}
	return nil
}

func retire(ctx context.Context, r *receipt) error {
	if !r.EntryCreated {
		return problem("no owned Linux source effect; external trust preserved")
	}
	exists, err := inspectSource(r)
	if err != nil {
		return err
	}
	if !exists {
		return problem("owned CA source disappeared; refresh/other effects are uncertain, use manual recovery")
	}
	linked, _, err := inspectLink(r)
	if err != nil {
		return err
	}
	if r.LinkCreated && !linked {
		return problem("owned CA link disappeared; preserve source and inspect drift")
	}
	if err = command(ctx, "/usr/bin/rm", "--", source(r)); err != nil {
		return err
	}
	if _, err = os.Lstat(source(r)); !os.IsNotExist(err) {
		return problem("CA source removal not verified; manual recovery required")
	}
	// Stock refresh can leave a dangling local .pem link. Remove only the exact
	// newly provisioned link; an external/legacy link never becomes ours.
	r.EntryOutcome = "owned-source-deleted"
	if linked && r.LinkCreated {
		if current, _, err := inspectLink(r); err != nil || !current {
			return problem("CA link changed before removal; preserve uncertain residual")
		}
		if err = command(ctx, "/usr/bin/rm", "--", certificateLink(r)); err != nil {
			return err
		}
		if _, err = os.Lstat(certificateLink(r)); !os.IsNotExist(err) {
			return problem("CA link deletion not verified; manual recovery required")
		}
		r.EntryOutcome = "owned-source-and-link-deleted"
	} else if linked {
		r.EntryOutcome = "source-deleted-external-or-unowned-link-preserved"
	}
	if err = command(ctx, "/usr/sbin/update-ca-certificates"); err != nil {
		return err
	}
	if linked && !r.LinkCreated {
		return nil
	} // Preserve an external dangling link and its derived references.
	// Local source removal can produce zero distro change notifications. Let
	// stock OpenSSL rebuild its derived index without deleting foreign PEMs.
	return command(ctx, "/usr/bin/openssl", "rehash", "/etc/ssl/certs")
}

func selectedTrust(context.Context, v1.GatewayCA) (bool, error) { return true, nil }

func validateEffects(_ context.Context, r *receipt) error {
	exists, err := inspectSource(r)
	if err != nil {
		return err
	}
	if !exists {
		return problem("retained system CA source disappeared; preserve tracking and inspect drift")
	}
	linked, _, err := inspectLink(r)
	if err != nil {
		return err
	}
	if r.LinkCreated && !linked {
		return problem("retained owned CA link disappeared; inspect drift before retry")
	}
	return nil
}

func sourceIdentity(info os.FileInfo) (string, error) {
	identity, err := localipc.Identity(info)
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", problem("CA source change time unavailable")
	}
	return fmt.Sprintf("%s:%d:%d", identity, stat.Ctim.Sec, stat.Ctim.Nsec), nil
}

func certificateLink(r *receipt) string {
	return filepath.Join("/etc/ssl/certs", "backlot-"+r.CA.Fingerprint+".pem")
}

func inspectLink(r *receipt) (bool, string, error) {
	if err := rootDirectories("/etc", "/etc/ssl", "/etc/ssl/certs"); err != nil {
		return false, "", err
	}
	info, err := os.Lstat(certificateLink(r))
	if os.IsNotExist(err) {
		return false, "", nil
	}
	if err != nil {
		return false, "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	target, err := os.Readlink(certificateLink(r))
	if !ok || stat.Uid != 0 || info.Mode()&os.ModeSymlink == 0 || err != nil || target != source(r) {
		return false, "", problem("conflicting system CA link preserved; manual recovery required")
	}
	identity, err := sourceIdentity(info)
	if err != nil {
		return false, "", err
	}
	if r.LinkCreated && (r.LinkIdentity == "" || identity != r.LinkIdentity) {
		return false, "", problem("owned CA link was replaced; preserve it and inspect drift")
	}
	return true, identity, nil
}

// authz: privileged stock effects require root-owned, non-aliased ancestry.
func rootDirectories(paths ...string) error {
	for _, path := range paths {
		info, err := os.Lstat(path)
		if err != nil {
			return problem("system CA store directory unavailable; ask an administrator to prepare the stock distro directory")
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || !info.IsDir() || stat.Uid != 0 || info.Mode().Perm()&0022 != 0 {
			return problem("system CA store ancestry is unsafe or aliased; use manual recovery")
		}
	}
	return nil
}
