package relay

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pgUniqueViolation is Postgres's error code for a unique-constraint
// violation (23505), used to translate a duplicate handle into
// ErrHandleTaken instead of a raw driver error leaking out of Store.
const pgUniqueViolation = "23505"

// schema is applied on startup. CREATE TABLE/INDEX IF NOT EXISTS makes it
// safe to run every time the server starts, so there's no separate
// migration-runner dependency for this stage -- if the schema ever needs a
// real migration (altering an existing column, say), that's the point to
// bring one in.
const schema = `
CREATE TABLE IF NOT EXISTS users (
	id            BIGSERIAL PRIMARY KEY,
	handle        TEXT NOT NULL UNIQUE,
	password_hash TEXT NOT NULL,
	created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS sessions (
	token      TEXT PRIMARY KEY,
	user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS sessions_user_id_idx ON sessions(user_id);

CREATE TABLE IF NOT EXISTS orgs (
	id            BIGSERIAL PRIMARY KEY,
	name          TEXT NOT NULL,
	owner_user_id BIGINT NOT NULL REFERENCES users(id),
	created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS org_members (
	org_id    BIGINT NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
	user_id   BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	role      TEXT NOT NULL DEFAULT 'member',
	joined_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	PRIMARY KEY (org_id, user_id)
);

CREATE TABLE IF NOT EXISTS org_invites (
	code       TEXT PRIMARY KEY,
	org_id     BIGINT NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
	created_by BIGINT NOT NULL REFERENCES users(id),
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	expires_at TIMESTAMPTZ NOT NULL,
	max_uses   INT NOT NULL DEFAULT 1,
	used_count INT NOT NULL DEFAULT 0
);
`

// PGStore is the PostgreSQL-backed Store, using a connection pool (pgxpool)
// since the server handles many concurrent client connections, each
// potentially issuing queries concurrently.
type PGStore struct {
	pool *pgxpool.Pool
}

// NewPGStore connects to connString (e.g.
// "postgres://user:pass@host:5432/dbname?sslmode=disable") and verifies the
// connection with a ping before returning, so a bad connection string or
// unreachable database fails immediately at startup rather than on the
// first query.
func NewPGStore(ctx context.Context, connString string) (*PGStore, error) {
	pool, err := pgxpool.New(ctx, connString)
	if err != nil {
		return nil, fmt.Errorf("relay: connect to postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("relay: ping postgres: %w", err)
	}
	return &PGStore{pool: pool}, nil
}

// Migrate applies the schema (idempotent -- see the schema constant).
func (s *PGStore) Migrate(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, schema); err != nil {
		return fmt.Errorf("relay: migrate schema: %w", err)
	}
	return nil
}

// Close releases the connection pool. Call once when the server shuts down.
func (s *PGStore) Close() {
	s.pool.Close()
}

func (s *PGStore) CreateUser(ctx context.Context, handle, passwordHash string) (User, error) {
	var u User
	err := s.pool.QueryRow(ctx,
		`INSERT INTO users (handle, password_hash) VALUES ($1, $2)
		 RETURNING id, handle, password_hash, created_at`,
		handle, passwordHash,
	).Scan(&u.ID, &u.Handle, &u.PasswordHash, &u.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
			return User{}, ErrHandleTaken
		}
		return User{}, fmt.Errorf("relay: create user: %w", err)
	}
	return u, nil
}

func (s *PGStore) UserByHandle(ctx context.Context, handle string) (User, error) {
	var u User
	err := s.pool.QueryRow(ctx,
		`SELECT id, handle, password_hash, created_at FROM users WHERE handle = $1`,
		handle,
	).Scan(&u.ID, &u.Handle, &u.PasswordHash, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("relay: query user: %w", err)
	}
	return u, nil
}

func (s *PGStore) CreateSession(ctx context.Context, userID int64, token string, expiresAt time.Time) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO sessions (token, user_id, expires_at) VALUES ($1, $2, $3)`,
		token, userID, expiresAt,
	)
	if err != nil {
		return fmt.Errorf("relay: create session: %w", err)
	}
	return nil
}

func (s *PGStore) UserBySessionToken(ctx context.Context, token string) (User, time.Time, error) {
	var u User
	var expiresAt time.Time
	err := s.pool.QueryRow(ctx,
		`SELECT u.id, u.handle, u.password_hash, u.created_at, s.expires_at
		 FROM sessions s JOIN users u ON u.id = s.user_id
		 WHERE s.token = $1`,
		token,
	).Scan(&u.ID, &u.Handle, &u.PasswordHash, &u.CreatedAt, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, time.Time{}, ErrNotFound
	}
	if err != nil {
		return User{}, time.Time{}, fmt.Errorf("relay: query session: %w", err)
	}
	return u, expiresAt, nil
}

func (s *PGStore) DeleteSession(ctx context.Context, token string) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE token = $1`, token); err != nil {
		return fmt.Errorf("relay: delete session: %w", err)
	}
	return nil
}

func (s *PGStore) CreateOrg(ctx context.Context, name string, ownerUserID int64) (Org, error) {
	var org Org
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`INSERT INTO orgs (name, owner_user_id) VALUES ($1, $2)
			 RETURNING id, name, owner_user_id, created_at`,
			name, ownerUserID,
		).Scan(&org.ID, &org.Name, &org.OwnerUserID, &org.CreatedAt); err != nil {
			return err
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, 'admin')`,
			org.ID, ownerUserID,
		)
		return err
	})
	if err != nil {
		return Org{}, fmt.Errorf("relay: create org: %w", err)
	}
	return org, nil
}

func (s *PGStore) OrgsForUser(ctx context.Context, userID int64) ([]Org, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT o.id, o.name, o.owner_user_id, o.created_at
		 FROM orgs o JOIN org_members om ON om.org_id = o.id
		 WHERE om.user_id = $1
		 ORDER BY o.name`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("relay: query orgs for user: %w", err)
	}
	defer rows.Close()

	var orgs []Org
	for rows.Next() {
		var o Org
		if err := rows.Scan(&o.ID, &o.Name, &o.OwnerUserID, &o.CreatedAt); err != nil {
			return nil, fmt.Errorf("relay: scan org: %w", err)
		}
		orgs = append(orgs, o)
	}
	return orgs, rows.Err()
}

func (s *PGStore) OrgMemberHandles(ctx context.Context, orgID int64) ([]string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT u.handle FROM org_members om JOIN users u ON u.id = om.user_id WHERE om.org_id = $1`,
		orgID,
	)
	if err != nil {
		return nil, fmt.Errorf("relay: query org members: %w", err)
	}
	defer rows.Close()
	return scanHandles(rows)
}

func (s *PGStore) OrgMateHandles(ctx context.Context, userID int64) ([]string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT DISTINCT u2.handle
		 FROM org_members om1
		 JOIN org_members om2 ON om2.org_id = om1.org_id AND om2.user_id != om1.user_id
		 JOIN users u2 ON u2.id = om2.user_id
		 WHERE om1.user_id = $1`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("relay: query org mates: %w", err)
	}
	defer rows.Close()
	return scanHandles(rows)
}

func scanHandles(rows pgx.Rows) ([]string, error) {
	var handles []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, fmt.Errorf("relay: scan handle: %w", err)
		}
		handles = append(handles, h)
	}
	return handles, rows.Err()
}

func (s *PGStore) SharesOrg(ctx context.Context, userID1, userID2 int64) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (
			SELECT 1 FROM org_members om1
			JOIN org_members om2 ON om2.org_id = om1.org_id
			WHERE om1.user_id = $1 AND om2.user_id = $2
		)`,
		userID1, userID2,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("relay: query shared org: %w", err)
	}
	return exists, nil
}

func (s *PGStore) IsOrgAdmin(ctx context.Context, orgID, userID int64) (bool, error) {
	var role string
	err := s.pool.QueryRow(ctx,
		`SELECT role FROM org_members WHERE org_id = $1 AND user_id = $2`,
		orgID, userID,
	).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("relay: query org role: %w", err)
	}
	return role == "admin", nil
}

func (s *PGStore) CreateOrgInvite(ctx context.Context, orgID, createdBy int64, code string, expiresAt time.Time) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO org_invites (code, org_id, created_by, expires_at, max_uses) VALUES ($1, $2, $3, $4, 1)`,
		code, orgID, createdBy, expiresAt,
	)
	if err != nil {
		return fmt.Errorf("relay: create org invite: %w", err)
	}
	return nil
}

func (s *PGStore) RedeemOrgInvite(ctx context.Context, code string, userID int64) (Org, error) {
	var org Org
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var orgID int64
		err := tx.QueryRow(ctx,
			`SELECT org_id FROM org_invites
			 WHERE code = $1 AND expires_at > now() AND used_count < max_uses
			 FOR UPDATE`,
			code,
		).Scan(&orgID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInviteInvalid
		}
		if err != nil {
			return err
		}

		var alreadyMember bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM org_members WHERE org_id = $1 AND user_id = $2)`,
			orgID, userID,
		).Scan(&alreadyMember); err != nil {
			return err
		}
		if alreadyMember {
			return ErrAlreadyOrgMember
		}

		if _, err := tx.Exec(ctx,
			`INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, 'member')`,
			orgID, userID,
		); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE org_invites SET used_count = used_count + 1 WHERE code = $1`,
			code,
		); err != nil {
			return err
		}

		return tx.QueryRow(ctx,
			`SELECT id, name, owner_user_id, created_at FROM orgs WHERE id = $1`,
			orgID,
		).Scan(&org.ID, &org.Name, &org.OwnerUserID, &org.CreatedAt)
	})
	if errors.Is(err, ErrInviteInvalid) || errors.Is(err, ErrAlreadyOrgMember) {
		return Org{}, err
	}
	if err != nil {
		return Org{}, fmt.Errorf("relay: redeem org invite: %w", err)
	}
	return org, nil
}
