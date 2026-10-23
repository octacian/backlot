// Package manager owns resource-specific allocation effects. Backlot supplies
// durable intent and secrets; managers return explicit ownership and effect facts.
package manager

import (
	"context"
	"crypto/sha1" // MariaDB mysql_native_password ownership verification, not password generation.
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"

	v1 "github.com/octacian/backlot/api/v1"
)

var opaque = regexp.MustCompile(`^[a-f0-9]{64}$`)
var sqlName = regexp.MustCompile(`^bl[a-f0-9]{32}$`)

// ValidateRequest rejects unsupported operations and unsafe identifier/secret shapes.
func ValidateRequest(r v1.ManagerRequest) error {
	if r.Version != v1.ManagerProtocolVersion || !opaque.MatchString(r.AllocationID) || !opaque.MatchString(r.Ownership) || !sqlName.MatchString(r.Database) || !sqlName.MatchString(r.Username) || !opaque.MatchString(r.Password) {
		return errors.New("invalid private manager request")
	}
	if r.Force && r.Operation != "release" {
		return errors.New("force is supported only for manager release")
	}
	switch r.Operation {
	case "allocate", "inspect", "release":
		return nil
	}
	return errors.New("unsupported manager operation")
}

// ValidateResult binds a structured command result to the requested allocation.
// Process success alone never proves ownership or completion.
func ValidateResult(request v1.ManagerRequest, result v1.ManagerResult) error {
	if result.Version != v1.ManagerProtocolVersion || result.AllocationID != request.AllocationID {
		return errors.New("manager result identity mismatch")
	}
	switch result.Ownership {
	case "owned", "absent", "uncertain":
	default:
		return errors.New("invalid manager ownership observation")
	}
	switch result.Effect {
	case "ready":
		if result.Ownership != "owned" {
			return errors.New("ready requires verified ownership")
		}
	case "released":
		if result.Ownership != "absent" {
			return errors.New("released requires verified absence")
		}
	case "partial":
	default:
		return errors.New("invalid manager effect")
	}
	return nil
}

func fingerprint(token string) string {
	sum := sha256.Sum256([]byte("backlot-database/" + token))
	return hex.EncodeToString(sum[:])
}
func passwordHash(password string) string {
	a := sha1.Sum([]byte(password))
	b := sha1.Sum(a[:])
	return "*" + strings.ToUpper(hex.EncodeToString(b[:]))
}

type observation struct{ database, user, granted bool }

func inspect(ctx context.Context, db *sql.DB, request v1.ManagerRequest) (observation, error) {
	var out observation
	var comment string
	err := db.QueryRowContext(ctx, "SELECT SCHEMA_COMMENT FROM information_schema.SCHEMATA WHERE SCHEMA_NAME = ?", request.Database).Scan(&comment)
	if err == nil {
		out.database = true
		if !request.Force && comment != fingerprint(request.Ownership) {
			return out, errors.New("database ownership uncertain; preserved")
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return out, errors.New("database ownership unavailable; preserved")
	}
	var authentication, plugin string
	err = db.QueryRowContext(ctx, "SELECT authentication_string, plugin FROM mysql.user WHERE User = ? AND Host = '%'", request.Username).Scan(&authentication, &plugin)
	if err == nil {
		out.user = true
		if !request.Force && (plugin != "mysql_native_password" || authentication != passwordHash(request.Password)) {
			return out, errors.New("user ownership uncertain; preserved")
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return out, errors.New("user ownership unavailable; preserved")
	}
	if out.user && !request.Force {
		rows, err := db.QueryContext(ctx, "SHOW GRANTS FOR '"+request.Username+"'@'%'")
		if err != nil {
			return out, errors.New("user permissions unavailable; preserved")
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var grant string
			if err = rows.Scan(&grant); err != nil {
				return out, errors.New("user permissions unavailable; preserved")
			}
			usage := "GRANT USAGE ON *.* TO `" + request.Username + "`@`%`"
			expected := "GRANT ALL PRIVILEGES ON `" + request.Database + "`.* TO `" + request.Username + "`@`%`"
			if grant == expected {
				out.granted = true
				continue
			}
			if !strings.HasPrefix(grant, usage) || strings.Contains(grant, "WITH GRANT OPTION") {
				return out, errors.New("user permissions changed; preserved")
			}
		}
		if rows.Err() != nil {
			return out, errors.New("user permissions unavailable; preserved")
		}
	}
	return out, nil
}

// MariaDB implements the bounded allocation operations against a verified owned
// MariaDB server. Atomic database comments and the user's authentication hash
// authenticate interrupted effects. Explicit release force waives these proofs only
// for the caller's recorded targets; it still observes and verifies actual absence.
// Generated names contain no SQL grant wildcard characters. All errors are safe:
// driver diagnostics can include credentials and never cross this boundary.
// The optional checkpoint observes statement effects for private fault fixtures.
func MariaDB(ctx context.Context, db *sql.DB, request v1.ManagerRequest, checkpoint func(string) error) (v1.ManagerResult, error) {
	result := v1.ManagerResult{Version: v1.ManagerProtocolVersion, AllocationID: request.AllocationID, Ownership: "uncertain", Effect: "partial"}
	if err := ValidateRequest(request); err != nil {
		return result, err
	}
	observed, err := inspect(ctx, db, request)
	if err != nil {
		return result, err
	}
	result.Ownership = "owned"
	if !observed.database && !observed.user {
		result.Ownership = "absent"
		result.Effect = "released"
	}
	if request.Operation == "inspect" {
		if observed.database && observed.user && observed.granted {
			result.Effect = "ready"
		}
		return result, nil
	}
	exec := func(step, query string) error {
		if _, err := db.ExecContext(ctx, query); err != nil {
			return errors.New("MariaDB manager effect failed; inspect/reconcile required; resources preserved")
		}
		if checkpoint != nil {
			return checkpoint("sql-" + request.Operation + ":" + step)
		}
		return nil
	}
	database := "`" + request.Database + "`"
	user := "'" + request.Username + "'@'%'"
	if request.Operation == "release" {
		// Verify both identities before deleting either. A partial release remains
		// reconcilable with the same retained request, including lost acknowledgments.
		if observed.user {
			if err = exec("user", "DROP USER "+user); err != nil {
				return result, err
			}
		}
		if observed.database {
			if err = exec("database", "DROP DATABASE "+database); err != nil {
				return result, err
			}
		}
		remaining, err := inspect(ctx, db, request)
		if err != nil || remaining.database || remaining.user {
			result.Ownership = "uncertain"
			result.Effect = "partial"
			return result, errors.New("allocation absence unverified; obligation retained")
		}
		result.Ownership = "absent"
		result.Effect = "released"
		return result, nil
	}
	result.Ownership = "uncertain"
	result.Effect = "partial"
	if !observed.database {
		if err = exec("database", "CREATE DATABASE "+database+" COMMENT '"+fingerprint(request.Ownership)+"'"); err != nil {
			return result, err
		}
	}
	if !observed.user {
		if err = exec("user", "CREATE USER "+user+" IDENTIFIED BY '"+request.Password+"'"); err != nil {
			return result, err
		}
	}
	if err = exec("grant", "GRANT ALL PRIVILEGES ON "+database+".* TO "+user); err != nil {
		return result, err
	}
	result.Ownership = "owned"
	result.Effect = "ready"
	return result, nil
}
