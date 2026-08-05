// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module implements the auth.SessionStore
// contract (internal/auth/session_store.go) over the v3 identity tables:
// sessions → memberships → companies. It resolves SHA-256 token hashes to
// sessions and sessions to memberships; raw bearer credentials never cross this
// boundary — only hashes (ADR-003, design §3).
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/arkelythex/drenyra-engram/internal/auth"
)

// LookupByTokenHash resolves the SHA-256 hash of a bearer credential to its
// session row, joining the membership and company rows (design §3: session →
// membership → company). A not-found token is a plain error that the resolver
// maps to PRINCIPAL_INVALID; a session whose membership or company is inactive
// returns MEMBERSHIP_INACTIVE — the session is not authenticatable. Revoked
// sessions are returned as-is; the resolver rejects them (PRINCIPAL_INVALID).
func (s *SQLiteStore) LookupByTokenHash(ctx context.Context, tokenHash string) (auth.StoredSession, error) {
	var (
		session        auth.StoredSession
		membershipStat string
		companyActive  int
		revokedAt      sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT s.id, s.membership_id, s.authentication_method, s.assurance_level,
		       s.authenticated_at, s.expires_at, s.revoked_at,
		       m.status, c.active
		FROM sessions s
		JOIN memberships m ON m.id = s.membership_id
		JOIN companies c ON c.tenant_id = m.tenant_id AND c.id = m.company_id
		WHERE s.token_hash = ?`, tokenHash,
	).Scan(
		&session.ID, &session.MembershipID, &session.AuthenticationMethod, &session.AssuranceLevel,
		&session.AuthenticatedAt, &session.ExpiresAt, &revokedAt,
		&membershipStat, &companyActive,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return auth.StoredSession{}, errors.New("session not found")
	}
	if err != nil {
		return auth.StoredSession{}, fmt.Errorf("lookup session: %w", err)
	}
	if revokedAt.Valid {
		revoked := revokedAt.String
		session.RevokedAt = &revoked
	}
	if membershipStat != "active" || companyActive != 1 {
		return auth.StoredSession{}, auth.New(auth.CodeMembershipInactive, "session membership is not active")
	}
	return session, nil
}

// LoadMembership loads the membership row, its roles (membership_roles) and the
// company active flag. It does NOT reject inactive memberships — the resolver
// maps the record to MEMBERSHIP_INACTIVE after this call (internal/auth/resolver.go).
func (s *SQLiteStore) LoadMembership(ctx context.Context, membershipID string) (auth.MembershipRecord, error) {
	var (
		m             auth.MembershipRecord
		companyActive int
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT m.id, m.subject_id, m.tenant_id, m.company_id, m.status, c.active
		FROM memberships m
		JOIN companies c ON c.tenant_id = m.tenant_id AND c.id = m.company_id
		WHERE m.id = ?`, membershipID,
	).Scan(&m.ID, &m.SubjectID, &m.TenantID, &m.CompanyID, &m.Status, &companyActive)
	if errors.Is(err, sql.ErrNoRows) {
		return auth.MembershipRecord{}, errors.New("membership not found")
	}
	if err != nil {
		return auth.MembershipRecord{}, fmt.Errorf("load membership: %w", err)
	}
	m.CompanyActive = companyActive == 1
	roles, err := s.loadMembershipRoles(ctx, membershipID)
	if err != nil {
		return auth.MembershipRecord{}, err
	}
	m.Roles = roles
	return m, nil
}

// loadMembershipRoles returns the accounting roles of a membership, canonically
// ordered (the resolver snapshot sorts/dedups again before encoding).
func (s *SQLiteStore) loadMembershipRoles(ctx context.Context, membershipID string) ([]auth.AccountingRole, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT role FROM membership_roles WHERE membership_id = ? ORDER BY role`, membershipID)
	if err != nil {
		return nil, fmt.Errorf("load membership roles: %w", err)
	}
	defer func() { _ = rows.Close() }()
	roles := make([]auth.AccountingRole, 0)
	for rows.Next() {
		var role auth.AccountingRole
		if err := rows.Scan(&role); err != nil {
			return nil, fmt.Errorf("scan membership role: %w", err)
		}
		roles = append(roles, role)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate membership roles: %w", err)
	}
	return roles, nil
}

// SessionSeed describes one authenticated session row (test and local-dev seed
// fixtures). TokenHash is the SHA-256 of the raw bearer credential — the raw
// value is never stored (design §3).
type SessionSeed struct {
	ID                   string
	TokenHash            string
	MembershipID         string
	AuthenticationMethod auth.AuthenticationMethod
	AssuranceLevel       auth.AssuranceLevel
	AuthenticatedAt      string
	ExpiresAt            string
	RevokedAt            *string
}

// SeedSession inserts one session row (FK: sessions.membership_id →
// memberships.id). Duplicate rows fail loudly — seeding is explicit, never a
// silent overwrite. created_at is the store clock.
func (s *SQLiteStore) SeedSession(seed SessionSeed) error {
	ctx := context.Background()
	var revoked any
	if seed.RevokedAt != nil {
		revoked = *seed.RevokedAt
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (
			id, token_hash, membership_id, authentication_method, assurance_level,
			authenticated_at, expires_at, revoked_at, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		seed.ID, seed.TokenHash, seed.MembershipID,
		string(seed.AuthenticationMethod), string(seed.AssuranceLevel),
		seed.AuthenticatedAt, seed.ExpiresAt, revoked, nowISO(),
	)
	if err != nil {
		return fmt.Errorf("seed session: %w", err)
	}
	return nil
}
