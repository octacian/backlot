package manager

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/lib/pq"
	v1 "github.com/octacian/backlot/api/v1"
)

// pq captures this at package initialization; a later environment read cannot
// establish whether its private protocol diagnostics were enabled at startup.
var postgresDebug = os.Getenv("PQGO_DEBUG") == "1"

// PostgreSQLDB opens only an explicit TCP endpoint without ambient PG settings.
// Callers retain endpoint/topology authority; raw driver errors remain private.
func PostgreSQLDB(host string, port int, database, user, password string) (*sql.DB, error) {
	if postgresDebug || host == "" || strings.ContainsAny(host, ",\x00/") || port < 1 || port > 65535 || database == "" || user == "" || password == "" || strings.ContainsAny(database+user+password, "\x00") {
		return nil, errors.New("PostgreSQL explicit connection configuration unavailable; protocol debugging must be disabled at startup")
	}
	config := pq.Config{Host: host, Port: uint16(port), Database: database, User: user, Password: password,
		SSLMode: pq.SSLModeDisable, ConnectTimeout: 2 * time.Second, ClientEncoding: "UTF8", Datestyle: "ISO, MDY",
		MinProtocolVersion: pq.ProtocolVersion30, MaxProtocolVersion: pq.ProtocolVersion30, TargetSessionAttrs: pq.TargetSessionAttrsAny}
	if user == "postgres" {
		// Startup settings protect every reconnect before any privileged query.
		config.Runtime = map[string]string{"search_path": "pg_catalog,pg_temp"}
	}
	connector, err := pq.NewConnectorConfig(config)
	if err != nil {
		return nil, errors.New("PostgreSQL connection configuration failed")
	}
	connector.Dialer(postgresDialer{})
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(1)
	return db, nil
}

func postgresFingerprint(domain, token string) string {
	sum := sha256.Sum256([]byte("backlot-postgresql-" + domain + "/" + token))
	return hex.EncodeToString(sum[:])
}

const postgresInitialGrants = `SELECT (SELECT datacl IS NULL FROM pg_database WHERE datname=current_database()) AND pg_get_userbyid(nspowner)='pg_database_owner'
 AND (SELECT count(*)=3 AND bool_and(NOT is_grantable AND ((grantee=nspowner AND privilege_type IN ('USAGE','CREATE')) OR (grantee=0 AND privilege_type='USAGE'))) FROM aclexplode(nspacl)) FROM pg_namespace WHERE nspname='public'`

type postgresObservation struct{ database, app, owner, ready bool }

func inspectPostgres(ctx context.Context, db *sql.DB, host string, port int, adminPassword string, r v1.ManagerRequest) (postgresObservation, error) {
	var out postgresObservation
	var appLogin bool
	for _, role := range []struct {
		name, domain string
		exists       *bool
	}{{r.Database, "owner", &out.owner}, {r.Username, "app", &out.app}} {
		var marker string
		var login, passwordNull, restricted bool
		err := db.QueryRowContext(ctx, `SELECT COALESCE(shobj_description(oid,'pg_authid'),''),rolcanlogin,rolpassword IS NULL,
 NOT rolsuper AND NOT rolcreatedb AND NOT rolcreaterole AND NOT rolreplication AND NOT rolbypassrls
 AND rolinherit AND rolconnlimit=-1 AND rolvaliduntil IS NULL
 AND NOT EXISTS(SELECT FROM pg_db_role_setting WHERE setrole=r.oid)
 AND NOT EXISTS(SELECT FROM pg_auth_members WHERE member=r.oid OR roleid=r.oid)
 AND (rolpassword IS NULL OR rolpassword LIKE 'SCRAM-SHA-256$%') FROM pg_authid r WHERE rolname=$1`, role.name).Scan(&marker, &login, &passwordNull, &restricted)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return out, errors.New("PostgreSQL role verification unavailable; preserved")
		}
		*role.exists = true
		if !r.Force && (marker != postgresFingerprint(role.domain, r.Ownership) || !restricted || (role.domain == "owner" && (login || !passwordNull)) || (role.domain == "app" && login == passwordNull)) {
			return out, errors.New("PostgreSQL role identity or attributes changed; preserved")
		}
		if role.domain == "app" {
			appLogin = login
		}
	}
	var owner string
	var limit int
	var shape bool
	err := db.QueryRowContext(ctx, `SELECT pg_get_userbyid(datdba),datconnlimit,datallowconn AND NOT datistemplate
 AND NOT EXISTS(SELECT FROM pg_db_role_setting WHERE setdatabase=d.oid)
 FROM pg_database d WHERE datname=$1`, r.Database).Scan(&owner, &limit, &shape)
	if err == nil {
		out.database = true
	} else if !errors.Is(err, sql.ErrNoRows) {
		return out, errors.New("PostgreSQL database verification unavailable; preserved")
	}
	if r.Force {
		return out, nil
	}
	if out.database && (!out.owner || !out.app || owner != r.Database || !shape || (appLogin && limit != -1) || (!appLogin && limit != 0)) {
		return out, errors.New("PostgreSQL database owner or state changed; preserved")
	}
	if !out.database {
		return out, nil
	}
	if !appLogin {
		initial, err := PostgreSQLDB(host, port, r.Database, "postgres", adminPassword)
		if err != nil {
			return out, err
		}
		var pristine bool
		err = initial.QueryRowContext(ctx, postgresInitialGrants).Scan(&pristine)
		if err == nil && pristine {
			err = PostgreSQLEmpty(ctx, initial)
		}
		_ = initial.Close()
		if err != nil || !pristine {
			return out, errors.New("PostgreSQL initial database shape or contents changed; preserved")
		}
	}
	if appLogin {
		// Exact database-local grants, excluding the owner role's inherent authority.
		var valid bool
		err = db.QueryRowContext(ctx, `SELECT has_database_privilege($2,oid,'CONNECT') AND has_database_privilege($2,oid,'CREATE') AND has_database_privilege($2,oid,'TEMPORARY')
 AND NOT EXISTS(SELECT FROM aclexplode(COALESCE(datacl,acldefault('d',datdba))) a
 WHERE (a.grantee<>datdba AND a.grantee<>(SELECT oid FROM pg_roles WHERE rolname=$2)) OR (a.grantee=(SELECT oid FROM pg_roles WHERE rolname=$2) AND a.is_grantable))
 FROM pg_database WHERE datname=$1`, r.Database, r.Username).Scan(&valid)
		if err != nil || !valid {
			return out, errors.New("PostgreSQL database grants changed; preserved")
		}
		app, err := PostgreSQLDB(host, port, r.Database, r.Username, r.Password)
		if err != nil {
			return out, err
		}
		err = app.PingContext(ctx)
		_ = app.Close()
		if err != nil {
			return out, errors.New("PostgreSQL retained application authentication failed; preserved")
		}
		wrong, err := PostgreSQLDB(host, port, r.Database, r.Username, "wrong-retained-password")
		if err != nil {
			return out, err
		}
		err = wrong.PingContext(ctx)
		_ = wrong.Close()
		var denied *pq.Error
		if !errors.As(err, &denied) || denied.Code != "28P01" {
			return out, errors.New("PostgreSQL application password enforcement unverified; preserved")
		}
		admin, err := PostgreSQLDB(host, port, r.Database, "postgres", adminPassword)
		if err != nil {
			return out, err
		}
		defer func() { _ = admin.Close() }()
		err = admin.QueryRowContext(ctx, `SELECT pg_get_userbyid(nspowner)='postgres' AND has_schema_privilege($1,oid,'USAGE') AND has_schema_privilege($1,oid,'CREATE')
 AND NOT EXISTS(SELECT FROM aclexplode(COALESCE(nspacl,acldefault('n',nspowner))) a WHERE (a.grantee<>nspowner AND a.grantee<>(SELECT oid FROM pg_roles WHERE rolname=$1)) OR (a.grantee=(SELECT oid FROM pg_roles WHERE rolname=$1) AND a.is_grantable))
 FROM pg_namespace WHERE nspname='public'`, r.Username).Scan(&valid)
		if err != nil || !valid {
			return out, errors.New("PostgreSQL public schema grants changed; preserved")
		}
		out.ready = true
	}
	return out, nil
}

// PostgreSQL reconciles only the three recorded SQL targets, preserving drift.
func PostgreSQL(ctx context.Context, db *sql.DB, host string, port int, adminPassword string, r v1.ManagerRequest, checkpoint func(string) error) (v1.ManagerResult, error) {
	result := v1.ManagerResult{Version: v1.ManagerProtocolVersion, AllocationID: r.AllocationID, Ownership: "uncertain", Effect: "partial"}
	if err := ValidateRequest(r); err != nil {
		return result, err
	}
	if r.Database == r.Username {
		return result, errors.New("PostgreSQL application and ownership roles must differ")
	}
	observed, err := inspectPostgres(ctx, db, host, port, adminPassword, r)
	if err != nil {
		return result, err
	}
	result.Ownership = "owned"
	if !observed.database && !observed.app && !observed.owner {
		result.Ownership = "absent"
		result.Effect = "released"
	}
	if r.Operation == "inspect" {
		if observed.ready {
			result.Effect = "ready"
		}
		return result, nil
	}
	if !r.Force && observed.app != observed.owner {
		return result, errors.New("PostgreSQL role pair incomplete; preserved")
	}
	point := func(step string) error {
		if checkpoint != nil {
			return checkpoint("sql-" + r.Operation + ":" + step)
		}
		return nil
	}
	effect := func(target *sql.DB, query, step string) error {
		if _, err := target.ExecContext(ctx, query); err != nil {
			return errors.New("PostgreSQL effect failed; inspect/reconcile required; resources preserved")
		}
		return point(step)
	}
	database, user := pq.QuoteIdentifier(r.Database), pq.QuoteIdentifier(r.Username)
	if r.Operation == "release" {
		if observed.database {
			if err = effect(db, "DROP DATABASE "+database, "database"); err != nil {
				return result, err
			}
		}
		query := ""
		if observed.app {
			query += "DROP ROLE " + user + "; "
		}
		if observed.owner {
			query += "DROP ROLE " + database + "; "
		}
		if err = postgresTransaction(ctx, db, query, func() error { return point("roles-before-commit") }); err != nil {
			return result, err
		}
		if err = point("roles"); err != nil {
			return result, err
		}
		remaining, err := inspectPostgres(ctx, db, host, port, adminPassword, r)
		if err != nil || remaining.database || remaining.app || remaining.owner {
			return result, errors.New("PostgreSQL allocation absence unverified; obligation retained")
		}
		result.Ownership = "absent"
		result.Effect = "released"
		return result, nil
	}
	if observed.ready {
		result.Effect = "ready"
		return result, nil
	}
	if observed.app && !observed.database {
		var login bool
		if err = db.QueryRowContext(ctx, "SELECT rolcanlogin FROM pg_roles WHERE rolname=$1", r.Username).Scan(&login); err != nil || login {
			return result, errors.New("PostgreSQL finalized database missing; preserved")
		}
	}
	result.Ownership = "uncertain"
	result.Effect = "partial"
	if !observed.owner {
		query := "CREATE ROLE " + database + " NOLOGIN; COMMENT ON ROLE " + database + " IS " + pq.QuoteLiteral(postgresFingerprint("owner", r.Ownership)) + "; CREATE ROLE " + user + " NOLOGIN PASSWORD NULL; COMMENT ON ROLE " + user + " IS " + pq.QuoteLiteral(postgresFingerprint("app", r.Ownership)) + ";"
		if err = postgresTransaction(ctx, db, query, func() error { return point("roles-before-commit") }); err != nil {
			return result, err
		}
		if err = point("roles"); err != nil {
			return result, err
		}
	}
	if !observed.database {
		if err = effect(db, "CREATE DATABASE "+database+" OWNER "+database+" TEMPLATE template0 CONNECTION LIMIT 0", "database"); err != nil {
			return result, err
		}
	}
	allocationDB, err := PostgreSQLDB(host, port, r.Database, "postgres", adminPassword)
	if err != nil {
		return result, err
	}
	defer func() { _ = allocationDB.Close() }()
	// Newly created or verified initial databases have only platform-default ACLs.
	var pristine bool
	err = allocationDB.QueryRowContext(ctx, postgresInitialGrants).Scan(&pristine)
	if err != nil || !pristine {
		return result, errors.New("PostgreSQL initial grants changed; preserved")
	}
	if err = PostgreSQLEmpty(ctx, allocationDB); err != nil {
		return result, err
	}
	query := "REVOKE ALL ON DATABASE " + database + " FROM PUBLIC; GRANT CONNECT,TEMPORARY,CREATE ON DATABASE " + database + " TO " + user + "; ALTER SCHEMA public OWNER TO postgres; REVOKE ALL ON SCHEMA public FROM PUBLIC; GRANT USAGE,CREATE ON SCHEMA public TO " + user + "; ALTER ROLE " + user + " LOGIN PASSWORD " + pq.QuoteLiteral(r.Password) + "; ALTER DATABASE " + database + " CONNECTION LIMIT -1;"
	if err = postgresTransaction(ctx, allocationDB, query, func() error { return point("grant-before-commit") }); err != nil {
		return result, err
	}
	if err = point("grant"); err != nil {
		return result, err
	}
	final, err := inspectPostgres(ctx, db, host, port, adminPassword, r)
	if err != nil || !final.ready {
		return result, errors.New("PostgreSQL readiness unverified; preserved")
	}
	result.Ownership = "owned"
	result.Effect = "ready"
	return result, nil
}

// Roles/comments and final grants use one transaction per real effect boundary.
func postgresTransaction(ctx context.Context, db *sql.DB, query string, beforeCommit func() error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("PostgreSQL transaction unavailable; preserved")
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, query); err != nil {
		return errors.New("PostgreSQL transaction failed; inspect/reconcile required; preserved")
	}
	if err = beforeCommit(); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return errors.New("PostgreSQL commit unacknowledged; inspect/reconcile required; preserved")
	}
	return nil
}

// PostgreSQLEmpty verifies the supported fresh database contents before LOGIN.
// Ready reuse deliberately skips this check: projects own their schemas/data.
func PostgreSQLEmpty(ctx context.Context, db *sql.DB) error {
	var empty bool
	err := db.QueryRowContext(ctx, `SELECT NOT EXISTS(SELECT FROM pg_namespace WHERE nspname NOT IN ('public','information_schema') AND nspname NOT LIKE 'pg\_%' ESCAPE '\')
 AND NOT EXISTS(SELECT FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public')
 AND NOT EXISTS(SELECT FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='public')
 AND NOT EXISTS(SELECT FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname='public')
 AND NOT EXISTS(SELECT FROM pg_extension WHERE extname<>'plpgsql') AND NOT EXISTS(SELECT FROM pg_default_acl)
 AND NOT EXISTS(SELECT FROM pg_event_trigger)`).Scan(&empty)
	if err != nil || !empty {
		return errors.New("PostgreSQL initial database is not empty; preserved")
	}
	return nil
}
