package modelreference

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

const (
	defaultModelsURL      = "https://models.dev/api.json"
	defaultSyncInterval   = 30 * time.Minute
	defaultRequestTimeout = 15 * time.Second
)

// Syncer keeps the models reference table synced with models.dev.
type Syncer struct {
	db       *gorm.DB
	url      string
	interval time.Duration
	client   *http.Client
	now      func() time.Time
}

// NewSyncer constructs a models reference syncer.
func NewSyncer(db *gorm.DB) *Syncer {
	if db == nil {
		return nil
	}
	return &Syncer{
		db:       db,
		url:      defaultModelsURL,
		interval: defaultSyncInterval,
		client:   &http.Client{Timeout: defaultRequestTimeout},
		now:      time.Now,
	}
}

// Start runs the sync loop in the background.
func (s *Syncer) Start(ctx context.Context) {
	if s == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	go s.run(ctx)
	log.Infof("models reference syncer started (interval=%s)", s.interval)
}

func (s *Syncer) run(ctx context.Context) {
	if s == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	interval := s.interval
	if interval <= 0 {
		interval = defaultSyncInterval
	}

	if err := s.SyncOnce(ctx); err != nil {
		log.WithError(err).Warn("models syncer: initial sync failed")
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.SyncOnce(ctx); err != nil {
				log.WithError(err).Warn("models syncer: sync failed")
			}
		}
	}
}

// SyncOnce fetches and persists the latest models payload.
func (s *Syncer) SyncOnce(ctx context.Context) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("models syncer: nil db")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	url := strings.TrimSpace(s.url)
	if url == "" {
		return fmt.Errorf("models syncer: empty url")
	}
	client := s.client
	if client == nil {
		client = &http.Client{Timeout: defaultRequestTimeout}
	}
	clock := s.now
	if clock == nil {
		clock = time.Now
	}

	requestCtx, cancel := context.WithTimeout(ctx, defaultRequestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("models syncer: build request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("models syncer: request failed: %w", err)
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			log.WithError(errClose).Warn("models syncer: close response body failed")
		}
	}()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("models syncer: unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("models syncer: read response: %w", err)
	}

	refs, err := ParseModelsPayload(body)
	if err != nil {
		return err
	}
	if len(refs) == 0 {
		return fmt.Errorf("models syncer: empty payload")
	}

	syncTime := clock().UTC()
	if err := StoreReferences(ctx, s.db, refs, syncTime); err != nil {
		return err
	}

	return nil
}
