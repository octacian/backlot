package daemon

import (
	"context"
	"database/sql"
	"errors"

	"github.com/lib/pq"
	"github.com/octacian/backlot/internal/manager"
	bolt "go.etcd.io/bbolt"
)

// This witness shares the owned server volume lifetime; it is public evidence,
// never the server capability or another journal/cleanup target.
func postgresBootstrapMarker(record sharedServiceRecord) string {
	return "backlot-postgresql-bootstrap/" + hash("backlot-postgresql-bootstrap/"+record.Server.Token)
}

func (s *store) verifyPostgreSQL(ctx context.Context, db *sql.DB, record sharedServiceRecord, force bool) error {
	var directory string
	if err := db.QueryRowContext(ctx, "SELECT current_setting('data_directory')").Scan(&directory); err != nil || directory != "/var/lib/postgresql/data" {
		return problem("shared_unsupported", "PostgreSQL effective data directory is unsupported; preserved")
	}
	var secure bool
	err := db.QueryRowContext(ctx, `SELECT version() LIKE 'PostgreSQL %' AND current_setting('password_encryption')='scram-sha-256'
 AND (SELECT rolsuper AND rolcanlogin AND rolpassword LIKE 'SCRAM-SHA-256$%' FROM pg_authid WHERE rolname=current_user)
 AND (SELECT count(*)>0 AND bool_and(error IS NULL AND (type='local' OR (type IN ('host','hostssl','hostnossl') AND auth_method='scram-sha-256'))) FROM pg_hba_file_rules)
 AND NOT (SELECT datallowconn FROM pg_database WHERE datname='template0')`).Scan(&secure)
	if err != nil || !secure {
		return problem("shared_unsupported", "PostgreSQL identity/authentication enforcement unverified; preserved")
	}
	wrong, err := manager.PostgreSQLDB("127.0.0.1", record.Port, "postgres", "postgres", "wrong-"+newID())
	if err != nil {
		return err
	}
	err = wrong.PingContext(ctx)
	_ = wrong.Close()
	var denied *pq.Error
	if !errors.As(err, &denied) || denied.Code != "28P01" {
		return problem("shared_unsupported", "PostgreSQL wrong-password rejection unverified; preserved")
	}
	// Force targets only recorded allocation names; never bootstrap or repair maintenance.
	if force {
		return nil
	}
	var marker string
	err = db.QueryRowContext(ctx, "SELECT COALESCE(shobj_description(oid,'pg_database'),'') FROM pg_database WHERE datname='postgres'").Scan(&marker)
	if err != nil {
		return problem("shared_uncertain", "PostgreSQL maintenance witness unavailable; preserved")
	}
	expected := postgresBootstrapMarker(record)
	if marker != expected {
		if marker != "default administrative connection database" {
			return problem("shared_uncertain", "PostgreSQL maintenance witness changed; preserved")
		}
		// No retained allocation fact, even a released acknowledgment, permits bootstrap.
		retained := false
		err = s.db.View(func(tx *bolt.Tx) error {
			return tx.Bucket([]byte("shared_allocations")).ForEach(func(_, data []byte) error {
				allocations, err := decodeSharedAllocations(data)
				if err != nil {
					return err
				}
				for _, a := range allocations {
					if a.ServiceID == record.ID {
						retained = true
					}
				}
				return nil
			})
		})
		if err != nil {
			return err
		}
		var pristine bool
		// PostgreSQL initdb revokes PUBLIC TEMPORARY on template0/template1,
		// materializing their owner+PUBLIC-CONNECT ACLs; postgres keeps NULL defaults.
		err = db.QueryRowContext(ctx, `SELECT NOT EXISTS(SELECT FROM pg_authid WHERE rolname<>'postgres' AND rolname NOT LIKE 'pg\_%' ESCAPE '\')
 AND (SELECT count(*)=3 AND bool_and(pg_get_userbyid(datdba)='postgres' AND datconnlimit=-1
 AND ((datname='postgres' AND datacl IS NULL) OR (datname IN ('template0','template1') AND
 (SELECT count(*)=4 AND bool_or(grantee=0 AND privilege_type='CONNECT')
 AND bool_and(NOT is_grantable AND grantor=datdba AND ((grantee=datdba AND privilege_type IN ('CONNECT','CREATE','TEMPORARY')) OR (grantee=0 AND privilege_type='CONNECT')))
 FROM aclexplode(datacl))))
 AND ((datname='postgres' AND datallowconn AND NOT datistemplate AND shobj_description(oid,'pg_database')='default administrative connection database')
 OR (datname='template1' AND datallowconn AND datistemplate AND shobj_description(oid,'pg_database')='default template for new databases')
 OR (datname='template0' AND NOT datallowconn AND datistemplate AND shobj_description(oid,'pg_database')='unmodifiable empty database')))
 FROM pg_database) AND NOT EXISTS(SELECT FROM pg_db_role_setting)`).Scan(&pristine)
		if err != nil || !pristine || retained {
			return problem("shared_uncertain", "PostgreSQL bootstrap state is not pristine; preserved")
		}
		// Pristine initialization excludes application objects/default grants in
		// both connectable maintenance databases, not merely allocation-shaped names.
		for _, name := range []string{"postgres", "template1"} {
			maintenance, err := manager.PostgreSQLDB("127.0.0.1", record.Port, name, "postgres", record.AdminPassword)
			if err != nil {
				return err
			}
			err = maintenance.QueryRowContext(ctx, `SELECT pg_get_userbyid(nspowner)='pg_database_owner' AND (SELECT count(*)=3 AND bool_and(NOT is_grantable AND ((grantee=nspowner AND privilege_type IN ('USAGE','CREATE')) OR (grantee=0 AND privilege_type='USAGE'))) FROM aclexplode(nspacl)) FROM pg_namespace WHERE nspname='public'`).Scan(&pristine)
			if err == nil && pristine {
				err = manager.PostgreSQLEmpty(ctx, maintenance)
			}
			_ = maintenance.Close()
			if err != nil || !pristine {
				return problem("shared_uncertain", "PostgreSQL maintenance catalogs are not pristine; preserved")
			}
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return problem("shared_unavailable", "PostgreSQL maintenance transaction unavailable")
		}
		defer func() { _ = tx.Rollback() }()
		if _, err = tx.ExecContext(ctx, "REVOKE ALL ON DATABASE postgres,template1 FROM PUBLIC; COMMENT ON DATABASE postgres IS "+pq.QuoteLiteral(expected)); err != nil {
			return problem("shared_unavailable", "PostgreSQL maintenance initialization failed; preserved")
		}
		if err = s.sharedPoint("sql-bootstrap:before-commit"); err != nil {
			return err
		}
		if err = tx.Commit(); err != nil {
			return problem("shared_uncertain", "PostgreSQL maintenance commit unacknowledged; inspect required")
		}
		if err = s.sharedPoint("sql-bootstrap:commit"); err != nil {
			return err
		}
	}
	var restricted bool
	err = db.QueryRowContext(ctx, `SELECT count(*)=2 AND bool_and(pg_get_userbyid(datdba)='postgres' AND datallowconn AND datconnlimit=-1
 AND NOT EXISTS(SELECT FROM aclexplode(COALESCE(datacl,acldefault('d',datdba))) a WHERE grantee<>datdba)
 AND NOT EXISTS(SELECT FROM pg_db_role_setting WHERE setdatabase=d.oid)) FROM pg_database d WHERE datname IN ('postgres','template1')`).Scan(&restricted)
	if err != nil || !restricted {
		return problem("shared_uncertain", "PostgreSQL maintenance ownership or ACL drift; preserved")
	}
	return nil
}
