package identity

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	errUsernameTaken  = errors.New("identity: username taken")
	errNotFound       = errors.New("identity: not found")
	errInvalidRefresh = errors.New("identity: invalid refresh token")
	errRefreshReused  = errors.New("identity: refresh token reused")
)

type User struct {
	ID       int64
	Username string
	Name     string
	Subtitle string
	Hue      *float64
}

type newRefresh struct {
	ID        int64
	UserID    int64
	FamilyID  int64
	Hash      []byte
	ExpiresAt time.Time
}

type store struct{ db *pgxpool.Pool }

const userCols = `id, username, name, subtitle, hue`

func scanUser(row pgx.Row, extra ...any) (User, error) {
	var u User
	dest := append([]any{&u.ID, &u.Username, &u.Name, &u.Subtitle, &u.Hue}, extra...)
	if err := row.Scan(dest...); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, errNotFound
		}
		return User{}, err
	}
	return u, nil
}

func (s *store) createUser(ctx context.Context, id int64, username, name, passwordHash string) (User, error) {
	_, err := s.db.Exec(ctx,
		`INSERT INTO users (id, username, name, password_hash) VALUES ($1, $2, $3, $4)`,
		id, username, name, passwordHash)
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		return User{}, errUsernameTaken
	}
	if err != nil {
		return User{}, err
	}
	return User{ID: id, Username: username, Name: name}, nil
}

func (s *store) userByUsername(ctx context.Context, username string) (User, string, error) {
	var hash string
	u, err := scanUser(s.db.QueryRow(ctx, `SELECT `+userCols+`, password_hash FROM users WHERE username = $1`, username), &hash)
	return u, hash, err
}

func (s *store) userByID(ctx context.Context, id int64) (User, error) {
	return scanUser(s.db.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id = $1`, id))
}

// updateProfile só altera os campos não nulos. Subtitle "" limpa o campo.
func (s *store) updateProfile(ctx context.Context, id int64, name, subtitle *string, hue *float64) (User, error) {
	return scanUser(s.db.QueryRow(ctx,
		`UPDATE users SET name = COALESCE($2, name), subtitle = COALESCE($3, subtitle), hue = COALESCE($4, hue)
		 WHERE id = $1 RETURNING `+userCols, id, name, subtitle, hue))
}

func (s *store) insertRefresh(ctx context.Context, r newRefresh) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO refresh_tokens (id, user_id, family_id, token_hash, expires_at) VALUES ($1, $2, $3, $4, $5)`,
		r.ID, r.UserID, r.FamilyID, r.Hash, r.ExpiresAt)
	return err
}

// rotateRefresh gasta o token antigo e grava o novo, na mesma família, numa transação.
// Token já usado = reuso: revoga a família inteira (o commit acontece mesmo assim) e devolve errRefreshReused.
func (s *store) rotateRefresh(ctx context.Context, oldHash []byte, next newRefresh, now time.Time) (int64, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // depois do Commit devolve ErrTxClosed, que ignoramos

	var id, userID, familyID int64
	var expiresAt time.Time
	var usedAt, revokedAt *time.Time
	err = tx.QueryRow(ctx,
		`SELECT id, user_id, family_id, expires_at, used_at, revoked_at FROM refresh_tokens WHERE token_hash = $1 FOR UPDATE`,
		oldHash).Scan(&id, &userID, &familyID, &expiresAt, &usedAt, &revokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, errInvalidRefresh
	}
	if err != nil {
		return 0, err
	}
	if revokedAt != nil || !now.Before(expiresAt) {
		return 0, errInvalidRefresh
	}
	if usedAt != nil {
		if _, err := tx.Exec(ctx, `UPDATE refresh_tokens SET revoked_at = $2 WHERE family_id = $1 AND revoked_at IS NULL`, familyID, now); err != nil {
			return 0, err
		}
		if err := tx.Commit(ctx); err != nil {
			return 0, err
		}
		return 0, errRefreshReused
	}
	if _, err := tx.Exec(ctx, `UPDATE refresh_tokens SET used_at = $2 WHERE id = $1`, id, now); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO refresh_tokens (id, user_id, family_id, token_hash, expires_at) VALUES ($1, $2, $3, $4, $5)`,
		next.ID, userID, familyID, next.Hash, next.ExpiresAt); err != nil {
		return 0, err
	}
	return userID, tx.Commit(ctx)
}

// revokeFamily revoga a família do token dado. Token desconhecido não é erro.
func (s *store) revokeFamily(ctx context.Context, hash []byte, now time.Time) error {
	_, err := s.db.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = $2
		 WHERE revoked_at IS NULL AND family_id = (SELECT family_id FROM refresh_tokens WHERE token_hash = $1)`,
		hash, now)
	return err
}
