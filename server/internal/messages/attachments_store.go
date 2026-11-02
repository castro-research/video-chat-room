package messages

import (
	"context"
	"time"
)

const attachmentCols = `id, channel_id, uploader_id, message_id, filename, content_type, size, object_key, status, created_at`

func scanAttachments(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close()
}) ([]Attachment, error) {
	defer rows.Close()
	var out []Attachment
	for rows.Next() {
		var a Attachment
		if err := rows.Scan(&a.ID, &a.ChannelID, &a.UploaderID, &a.MessageID, &a.Filename, &a.ContentType, &a.Size, &a.ObjectKey, &a.Status, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *store) insertAttachment(ctx context.Context, q querier, a Attachment) error {
	_, err := q.Exec(ctx,
		`INSERT INTO attachments (id, channel_id, uploader_id, filename, content_type, size, object_key) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		a.ID, a.ChannelID, a.UploaderID, a.Filename, a.ContentType, a.Size, a.ObjectKey)
	return err
}

func (s *store) attachmentsByID(ctx context.Context, q querier, ids []int64) ([]Attachment, error) {
	rows, err := q.Query(ctx, `SELECT `+attachmentCols+` FROM attachments WHERE id = ANY($1) ORDER BY id`, ids)
	if err != nil {
		return nil, err
	}
	return scanAttachments(rows)
}

func (s *store) attachmentsOfMessages(ctx context.Context, q querier, messageIDs []int64) ([]Attachment, error) {
	rows, err := q.Query(ctx, `SELECT `+attachmentCols+` FROM attachments WHERE message_id = ANY($1) ORDER BY message_id, id`, messageIDs)
	if err != nil {
		return nil, err
	}
	return scanAttachments(rows)
}

// bindAttachments promove a 'ready' só o que ainda é pendente, do mesmo canal e do mesmo usuário.
func (s *store) bindAttachments(ctx context.Context, q querier, user, channel, message int64, ids []int64) (int64, error) {
	tag, err := q.Exec(ctx,
		`UPDATE attachments SET status = 'ready', message_id = $1
		 WHERE id = ANY($2) AND status = 'pending' AND channel_id = $3 AND uploader_id = $4`, message, ids, channel, user)
	return tag.RowsAffected(), err
}

func (s *store) objectKeysOfMessage(ctx context.Context, q querier, message int64) ([]string, error) {
	rows, err := q.Query(ctx, `SELECT object_key FROM attachments WHERE message_id = $1`, message)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// deleteOrphans apaga até limit pendentes mais velhas que ttl e devolve as chaves dos objetos.
func (s *store) deleteOrphans(ctx context.Context, q querier, ttl time.Duration, limit int) ([]string, error) {
	rows, err := q.Query(ctx,
		`WITH o AS (
			SELECT id FROM attachments
			WHERE status = 'pending' AND created_at < now() - make_interval(secs => $1)
			ORDER BY created_at LIMIT $2 FOR UPDATE SKIP LOCKED)
		 DELETE FROM attachments a USING o WHERE a.id = o.id RETURNING a.object_key`, ttl.Seconds(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}
