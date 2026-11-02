package messages

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"kurz/server/internal/jobs"
	"kurz/server/internal/permissions"
	"kurz/server/internal/platform/httpx"
	"kurz/server/internal/platform/ids"
	"kurz/server/internal/platform/objstore"
)

// Anexos (RF14, RNF13). Fluxo: 1) RequestUpload cria a linha 'pending' e devolve uma URL PUT pré-assinada;
// 2) o cliente envia o arquivo direto ao storage; 3) Send com attachmentIds confere (Stat) e promove a 'ready',
// na mesma transação da mensagem; 4) as mensagens devolvem URLs GET pré-assinadas que expiram.
const (
	MaxAttachmentSize        = 25 << 20
	MaxAttachmentsPerMessage = 10
	MaxFilenameRunes         = 255
	UploadURLTTL             = 15 * time.Minute
	DownloadURLTTL           = time.Hour
	PendingTTL               = time.Hour // quanto um upload pedido e nunca ligado a mensagem sobrevive
	JobCleanupPending        = "attachments.cleanup"
	JobDeleteObject          = "attachments.delete_object"
	cleanupBatch             = 200
	cleanupEvery             = 10 * time.Minute
)

// ErrNoStorage: o servidor roda sem S3 configurado. Vira 501 not_supported (capability `attachments` ausente).
var ErrNoStorage = httpx.NewError(http.StatusNotImplemented, "not_supported", "attachments are not supported by this server")

// allowedTypes é a allowlist de Content-Type. Fora SVG e HTML de propósito: um navegador executa script neles.
// inline diz se o download abre no navegador (mídia segura) ou força "salvar como".
var allowedTypes = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true,
	"video/mp4": true, "video/webm": true,
	"audio/mpeg": true, "audio/ogg": true, "audio/wav": true, "audio/webm": true,
	"application/pdf": false, "text/plain": false, "application/zip": false,
}

type Attachment struct {
	ID          int64
	ChannelID   int64
	UploaderID  int64
	MessageID   *int64
	Filename    string
	ContentType string
	Size        int64
	ObjectKey   string
	Status      string // pending | ready
	CreatedAt   time.Time
	URL         string // GET pré-assinada; só preenchida ao montar mensagens
}

// WithStorage liga os anexos. Sem isso, tudo que é de anexo responde 501.
func (s *Service) WithStorage(o objstore.Store) *Service { s.objs = o; return s }

// HasStorage diz se os anexos estão ligados (capability `attachments`).
func (s *Service) HasStorage() bool { return s.objs != nil }

func normalizeContentType(raw string) (string, error) {
	mt, _, err := mime.ParseMediaType(strings.TrimSpace(raw))
	if err != nil {
		return "", httpx.Validation("contentType is invalid")
	}
	mt = strings.ToLower(mt)
	if _, ok := allowedTypes[mt]; !ok {
		return "", httpx.Validation("contentType " + mt + " is not allowed")
	}
	return mt, nil
}

func validFilename(name string) (string, error) {
	name = strings.TrimSpace(name)
	if n := utf8.RuneCountInString(name); n < 1 || n > MaxFilenameRunes || !utf8.ValidString(name) {
		return "", httpx.Validation("filename must have 1-255 characters")
	}
	if name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return "", httpx.Validation("filename must not contain path separators")
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", httpx.Validation("filename must not contain control characters")
		}
	}
	return name, nil
}

type UploadInput struct {
	Filename    string
	ContentType string
	Size        int64
}

type Upload struct {
	Attachment Attachment
	URL        string
	ExpiresAt  time.Time
}

// RequestUpload valida o pedido e devolve a URL para o cliente enviar o arquivo. Exige SendMessages no canal.
func (s *Service) RequestUpload(ctx context.Context, user, channel int64, in UploadInput) (Upload, error) {
	if s.objs == nil {
		return Upload{}, ErrNoStorage
	}
	if err := s.access(ctx, user, channel, permissions.SendMessages); err != nil {
		return Upload{}, err
	}
	name, err := validFilename(in.Filename)
	if err != nil {
		return Upload{}, err
	}
	ct, err := normalizeContentType(in.ContentType)
	if err != nil {
		return Upload{}, err
	}
	if in.Size < 1 || in.Size > MaxAttachmentSize {
		return Upload{}, httpx.Validation(fmt.Sprintf("size must be between 1 and %d bytes", MaxAttachmentSize))
	}
	a := Attachment{ID: s.ids.Next(), ChannelID: channel, UploaderID: user, Filename: name, ContentType: ct, Size: in.Size, Status: "pending"}
	a.ObjectKey = fmt.Sprintf("attachments/%d/%d", channel, a.ID)
	if err := s.store.insertAttachment(ctx, s.pool, a); err != nil {
		return Upload{}, err
	}
	url, err := s.objs.PresignPut(ctx, a.ObjectKey, UploadURLTTL)
	if err != nil {
		return Upload{}, err
	}
	return Upload{Attachment: a, URL: url, ExpiresAt: time.Now().Add(UploadURLTTL)}, nil
}

// checkAttachments confere, ANTES da transação da mensagem (Stat é rede), que os anexos são pendentes, do mesmo canal
// e do mesmo usuário e que o objeto existe com o tamanho declarado.
func (s *Service) checkAttachments(ctx context.Context, user, channel int64, attIDs []int64) error {
	if len(attIDs) == 0 {
		return nil
	}
	if s.objs == nil {
		return ErrNoStorage
	}
	if len(attIDs) > MaxAttachmentsPerMessage {
		return httpx.Validation(fmt.Sprintf("a message takes at most %d attachments", MaxAttachmentsPerMessage))
	}
	sorted := slices.Clone(attIDs)
	slices.Sort(sorted)
	if len(slices.Compact(sorted)) != len(sorted) {
		return httpx.Validation("attachmentIds has duplicates")
	}
	found, err := s.store.attachmentsByID(ctx, s.pool, attIDs)
	if err != nil {
		return err
	}
	if len(found) != len(attIDs) {
		return httpx.Validation("attachmentIds must be your pending uploads in this channel")
	}
	for _, a := range found {
		if a.ChannelID != channel || a.UploaderID != user || a.Status != "pending" {
			return httpx.Validation("attachmentIds must be your pending uploads in this channel")
		}
		info, err := s.objs.Stat(ctx, a.ObjectKey)
		if errors.Is(err, objstore.ErrNotFound) {
			return httpx.Validation("attachment " + ids.Format(a.ID) + " was not uploaded")
		}
		if err != nil {
			return err
		}
		if info.Size != a.Size {
			return httpx.Validation("attachment " + ids.Format(a.ID) + " size differs from the declared one")
		}
	}
	return nil
}

// bindAttachments liga os anexos à mensagem na transação dela. Se algum deixou de ser pendente no meio
// (a varredura de órfãos o apagou, ou outro envio o pegou), nada é gravado.
func (s *Service) bindAttachments(ctx context.Context, tx pgx.Tx, user, channel, message int64, attIDs []int64) error {
	if len(attIDs) == 0 {
		return nil
	}
	n, err := s.store.bindAttachments(ctx, tx, user, channel, message, attIDs)
	if err != nil {
		return err
	}
	if n != int64(len(attIDs)) {
		return httpx.Validation("attachmentIds must be your pending uploads in this channel")
	}
	return nil
}

// loadAttachments preenche Attachments de ms (com URL de download assinada).
func (s *Service) loadAttachments(ctx context.Context, q querier, ms []Message) error {
	if s.objs == nil || len(ms) == 0 {
		return nil
	}
	idx := make(map[int64]int, len(ms))
	mids := make([]int64, len(ms))
	for i, m := range ms {
		idx[m.ID] = i
		mids[i] = m.ID
	}
	as, err := s.store.attachmentsOfMessages(ctx, q, mids)
	if err != nil {
		return err
	}
	for _, a := range as {
		a.URL, err = s.objs.PresignGet(ctx, a.ObjectKey, objstore.GetOptions{
			Filename: a.Filename, ContentType: a.ContentType, Inline: allowedTypes[a.ContentType],
		}, DownloadURLTTL)
		if err != nil {
			return err
		}
		i := idx[*a.MessageID]
		ms[i].Attachments = append(ms[i].Attachments, a)
	}
	return nil
}

// enqueueObjectDeletes agenda a remoção dos objetos na transação que apaga as linhas: se a transação desfaz, nada some.
func (s *Service) enqueueObjectDeletes(ctx context.Context, tx pgx.Tx, keys []string) error {
	for _, k := range keys {
		if err := jobs.Enqueue(ctx, tx, s.ids.Next(), JobDeleteObject, map[string]string{"key": k}, time.Time{}); err != nil {
			return err
		}
	}
	return nil
}

// CleanupPending apaga os uploads pendentes mais velhos que PendingTTL (nunca ligados a mensagem) e enfileira a
// remoção dos objetos. Devolve quantos removeu. SKIP LOCKED: não briga com um envio que está promovendo o anexo.
func (s *Service) CleanupPending(ctx context.Context) (int, error) {
	total := 0
	for {
		n := 0
		err := s.inTx(ctx, func(tx pgx.Tx) error {
			keys, err := s.store.deleteOrphans(ctx, tx, s.pendingTTL, cleanupBatch)
			if err != nil {
				return err
			}
			n = len(keys)
			return s.enqueueObjectDeletes(ctx, tx, keys)
		})
		if err != nil {
			return total, err
		}
		total += n
		if n < cleanupBatch {
			return total, nil
		}
	}
}

// RegisterJobs registra os handlers de anexo no worker (e a varredura periódica). Sem storage, não faz nada.
func (s *Service) RegisterJobs(w *jobs.Worker) {
	if s.objs == nil {
		return
	}
	w.Handle(JobCleanupPending, func(ctx context.Context, _ json.RawMessage) error {
		_, err := s.CleanupPending(ctx)
		return err
	})
	w.Handle(JobDeleteObject, func(ctx context.Context, p json.RawMessage) error {
		var v struct {
			Key string `json:"key"`
		}
		if err := json.Unmarshal(p, &v); err != nil || v.Key == "" {
			return fmt.Errorf("bad payload: %w", err) // não adianta tentar de novo, mas o limite de tentativas encerra
		}
		return s.objs.Delete(ctx, v.Key)
	})
	w.Every(JobCleanupPending, cleanupEvery)
}
