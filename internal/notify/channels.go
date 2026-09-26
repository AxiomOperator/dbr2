// SPDX-License-Identifier: Apache-2.0

package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/AxiomOperator/dbr2/internal/audit"
	"github.com/AxiomOperator/dbr2/internal/auth"
	"github.com/AxiomOperator/dbr2/internal/store"
)

// Channel kinds.
const (
	KindEmail   = "email"
	KindWebhook = "webhook"
)

// SMTPSettingKey is the platform_settings key of the SMTP relay.
const SMTPSettingKey = "smtp"

// ChannelAD is the associated data sealing a channel's webhook secret.
func ChannelAD(id uuid.UUID) string { return "dbr2:notify:" + id.String() }

// SMTPAD is the associated data sealing the SMTP password.
const SMTPAD = "dbr2:settings:smtp"

// ChannelConfig is notification_channels.config.
type ChannelConfig struct {
	To  []string `json:"to,omitempty"`
	URL string   `json:"url,omitempty"`
}

// ParseConfig decodes a stored channel config (unknown or bad JSON → empty).
func ParseConfig(raw json.RawMessage) ChannelConfig {
	var c ChannelConfig
	_ = json.Unmarshal(raw, &c)
	return c
}

// ChannelInput creates or replaces a channel. Kind is fixed at creation.
type ChannelInput struct {
	Name    string
	Kind    string
	Enabled bool
	To      []string
	URL     string
	// Secret: nil keeps the current webhook secret, "" clears it.
	Secret      *string
	Events      []string
	MinSeverity string
}

type validated struct {
	name   string
	config json.RawMessage
	events []string
	minSev string
}

func validateChannel(kind string, in ChannelInput) (validated, error) {
	var v validated
	v.name = strings.TrimSpace(in.Name)
	if v.name == "" || len(v.name) > 100 {
		return v, fmt.Errorf("%w: name is required (at most 100 characters)", ErrInvalid)
	}
	var cfg ChannelConfig
	switch kind {
	case KindEmail:
		if len(in.To) == 0 || len(in.To) > 50 {
			return v, fmt.Errorf("%w: an email channel needs 1-50 recipients", ErrInvalid)
		}
		if in.URL != "" {
			return v, fmt.Errorf("%w: url applies to webhook channels only", ErrInvalid)
		}
		if in.Secret != nil && *in.Secret != "" {
			return v, fmt.Errorf("%w: secret applies to webhook channels only", ErrInvalid)
		}
		for _, t := range in.To {
			a, err := parseAddress(t)
			if err != nil {
				return v, fmt.Errorf("%w: %v", ErrInvalid, err)
			}
			cfg.To = append(cfg.To, a.Address)
		}
	case KindWebhook:
		if len(in.To) > 0 {
			return v, fmt.Errorf("%w: to applies to email channels only", ErrInvalid)
		}
		if err := ValidateWebhookURL(in.URL); err != nil {
			return v, err
		}
		cfg.URL = strings.TrimSpace(in.URL)
		if in.Secret != nil && *in.Secret != "" && len(*in.Secret) < 16 {
			return v, fmt.Errorf("%w: the webhook secret must be at least 16 characters", ErrInvalid)
		}
	default:
		return v, fmt.Errorf("%w: kind must be email or webhook", ErrInvalid)
	}
	v.config, _ = json.Marshal(cfg)
	ev, err := normalizeEvents(in.Events)
	if err != nil {
		return v, err
	}
	v.events = ev
	v.minSev = in.MinSeverity
	if v.minSev == "" {
		v.minSev = SeverityWarning
	}
	if SeverityRank(v.minSev) < 0 {
		return v, fmt.Errorf("%w: min_severity must be info, warning or critical", ErrInvalid)
	}
	return v, nil
}

func (s *Service) event(p *auth.Principal, m auth.RequestMeta, typ string) audit.Event {
	return audit.Event{OrgID: s.opts.OrgID, Type: typ, ActorUserID: &p.UserID, ActorDisplay: p.Username, ActorKind: audit.ActorUser,
		SourceIP: m.IP, RequestID: m.RequestID, Result: audit.Success}
}

// channelState is the audited view of a channel (never the secret).
func channelState(c store.NotificationChannel) map[string]any {
	cfg := ParseConfig(c.Config)
	st := map[string]any{"name": c.Name, "kind": c.Kind, "enabled": c.Enabled, "events": c.Events, "min_severity": c.MinSeverity,
		"has_signing_key": len(c.SecretEnc) > 0}
	if c.Kind == KindEmail {
		st["to"] = cfg.To
	} else {
		st["url"] = cfg.URL
	}
	return st
}

func isUniqueViolation(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505"
}

// ListChannels lists the organization's channels.
func (s *Service) ListChannels(ctx context.Context) ([]store.NotificationChannel, error) {
	return s.q.ListNotificationChannels(ctx, s.opts.OrgID)
}

// GetChannel returns one channel.
func (s *Service) GetChannel(ctx context.Context, id uuid.UUID) (store.NotificationChannel, error) {
	c, err := s.q.GetNotificationChannel(ctx, store.GetNotificationChannelParams{ID: id, OrgID: s.opts.OrgID})
	return c, notFound(err)
}

func (s *Service) sealSecret(id uuid.UUID, secret *string) ([]byte, error) {
	if secret == nil || *secret == "" {
		return nil, nil
	}
	if s.box == nil {
		return nil, errors.New("secret box not configured")
	}
	return s.box.Seal([]byte(*secret), ChannelAD(id))
}

// CreateChannel creates a channel (audited).
func (s *Service) CreateChannel(ctx context.Context, p *auth.Principal, in ChannelInput, m auth.RequestMeta) (store.NotificationChannel, error) {
	v, err := validateChannel(in.Kind, in)
	if err != nil {
		return store.NotificationChannel{}, err
	}
	id := uuid.New()
	sealed, err := s.sealSecret(id, in.Secret)
	if err != nil {
		return store.NotificationChannel{}, err
	}
	var out store.NotificationChannel
	err = s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		c, err := q.CreateNotificationChannel(ctx, store.CreateNotificationChannelParams{ID: id, OrgID: s.opts.OrgID, Name: v.name, Kind: in.Kind,
			Enabled: in.Enabled, Config: v.config, SecretEnc: sealed, Events: v.events, MinSeverity: v.minSev, CreatedBy: &p.UserID})
		if err != nil {
			if isUniqueViolation(err) {
				return fmt.Errorf("%w: a channel named %q already exists", ErrConflict, v.name)
			}
			return err
		}
		out = c
		ev := s.event(p, m, audit.NotificationChannelCreated)
		ev.TargetType, ev.TargetID = "notification_channel", c.ID.String()
		ev.After = channelState(c)
		_, err = rec.Record(ctx, ev)
		return err
	})
	return out, err
}

// UpdateChannel replaces a channel's settings (audited with before/after).
func (s *Service) UpdateChannel(ctx context.Context, p *auth.Principal, id uuid.UUID, in ChannelInput, m auth.RequestMeta) (store.NotificationChannel, error) {
	var out store.NotificationChannel
	err := s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		cur, err := q.GetNotificationChannel(ctx, store.GetNotificationChannelParams{ID: id, OrgID: s.opts.OrgID})
		if err != nil {
			return notFound(err)
		}
		if in.Kind != "" && in.Kind != cur.Kind {
			return fmt.Errorf("%w: a channel's kind cannot be changed", ErrInvalid)
		}
		v, err := validateChannel(cur.Kind, in)
		if err != nil {
			return err
		}
		sealed, err := s.sealSecret(id, in.Secret)
		if err != nil {
			return err
		}
		c, err := q.UpdateNotificationChannel(ctx, store.UpdateNotificationChannelParams{ID: id, OrgID: s.opts.OrgID, Name: v.name, Enabled: in.Enabled,
			Config: v.config, Events: v.events, MinSeverity: v.minSev, SetSecret: in.Secret != nil, SecretEnc: sealed})
		if err != nil {
			if isUniqueViolation(err) {
				return fmt.Errorf("%w: a channel named %q already exists", ErrConflict, v.name)
			}
			return notFound(err)
		}
		out = c
		ev := s.event(p, m, audit.NotificationChannelUpdated)
		ev.TargetType, ev.TargetID = "notification_channel", c.ID.String()
		ev.Before, ev.After = channelState(cur), channelState(c)
		ev.Details = map[string]any{"signing_key_changed": in.Secret != nil}
		_, err = rec.Record(ctx, ev)
		return err
	})
	return out, err
}

// DeleteChannel deletes a channel and its deliveries (audited).
func (s *Service) DeleteChannel(ctx context.Context, p *auth.Principal, id uuid.UUID, m auth.RequestMeta) error {
	return s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		cur, err := q.GetNotificationChannel(ctx, store.GetNotificationChannelParams{ID: id, OrgID: s.opts.OrgID})
		if err != nil {
			return notFound(err)
		}
		if _, err := q.DeleteNotificationChannel(ctx, store.DeleteNotificationChannelParams{ID: id, OrgID: s.opts.OrgID}); err != nil {
			return err
		}
		ev := s.event(p, m, audit.NotificationChannelDeleted)
		ev.TargetType, ev.TargetID = "notification_channel", id.String()
		ev.Before = channelState(cur)
		_, err = rec.Record(ctx, ev)
		return err
	})
}

// ListDeliveries returns a channel's most recent deliveries.
func (s *Service) ListDeliveries(ctx context.Context, id uuid.UUID, limit int32) ([]store.ListChannelDeliveriesRow, error) {
	if _, err := s.GetChannel(ctx, id); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	return s.q.ListChannelDeliveries(ctx, store.ListChannelDeliveriesParams{ChannelID: id, Limit: limit})
}

// TestResult is the outcome of a test notification.
type TestResult struct {
	Delivered bool
	Error     string
	Duration  time.Duration
}

// Test sends a synthetic notification.test message directly to the channel
// (bypassing the outbox, filters and the enabled flag) and returns the
// delivery error, if any.
func (s *Service) Test(ctx context.Context, channelID uuid.UUID) error {
	c, err := s.GetChannel(ctx, channelID)
	if err != nil {
		return err
	}
	return s.send(ctx, c, testMessage(c, ""))
}

func testMessage(c store.NotificationChannel, requestedBy string) Message {
	details := map[string]any{"channel_id": c.ID.String(), "channel": c.Name}
	if requestedBy != "" {
		details["requested_by"] = requestedBy
	}
	b, _ := json.Marshal(details)
	return Message{DeliveryID: "test-" + uuid.NewString(), Type: EventTest, Severity: SeverityInfo,
		Message: "Test notification from DBR² (channel " + c.Name + ")", TargetType: "notification_channel", TargetID: c.ID.String(),
		Details: b, CreatedAt: time.Now().UTC()}
}

// TestChannel runs Test on behalf of a user and audits the outcome
// (notification.channel.tested, result success or failure). A failed
// delivery is reported in the result, not as an error.
func (s *Service) TestChannel(ctx context.Context, p *auth.Principal, id uuid.UUID, m auth.RequestMeta) (TestResult, error) {
	c, err := s.GetChannel(ctx, id)
	if err != nil {
		return TestResult{}, err
	}
	start := time.Now()
	sendErr := s.send(ctx, c, testMessage(c, p.Username))
	res := TestResult{Delivered: sendErr == nil, Duration: time.Since(start)}
	ev := s.event(p, m, audit.NotificationChannelTested)
	ev.TargetType, ev.TargetID = "notification_channel", id.String()
	ev.Details = map[string]any{"name": c.Name, "kind": c.Kind}
	if sendErr != nil {
		res.Error = sendErr.Error()
		ev.Result = audit.Failure
		ev.Details["error"] = res.Error
	}
	if _, err := s.audit.Record(ctx, ev); err != nil {
		return TestResult{}, err
	}
	return res, nil
}

// ---- SMTP settings -------------------------------------------------------------

// SMTPView is the SMTP configuration without the password.
type SMTPView struct {
	Configured  bool
	Config      SMTPConfig
	PasswordSet bool
	UpdatedAt   *time.Time
}

// SMTPInput replaces the SMTP configuration. Password: nil keeps, "" clears.
type SMTPInput struct {
	Config   SMTPConfig
	Password *string
}

// SMTPSettings returns the organization's SMTP configuration.
func (s *Service) SMTPSettings(ctx context.Context) (SMTPView, error) {
	row, err := s.q.GetPlatformSetting(ctx, store.GetPlatformSettingParams{OrgID: s.opts.OrgID, Key: SMTPSettingKey})
	if errors.Is(err, pgx.ErrNoRows) {
		return SMTPView{Config: SMTPConfig{Port: 587, TLS: TLSStartTLS}}, nil
	}
	if err != nil {
		return SMTPView{}, err
	}
	return smtpView(row), nil
}

func smtpView(row store.PlatformSetting) SMTPView {
	var c SMTPConfig
	_ = json.Unmarshal(row.Value, &c)
	t := row.UpdatedAt
	return SMTPView{Configured: c.Host != "", Config: c, PasswordSet: len(row.SecretEnc) > 0, UpdatedAt: &t}
}

// UpdateSMTPSettings replaces the SMTP configuration (audited; the password
// is sealed and never returned or audited).
func (s *Service) UpdateSMTPSettings(ctx context.Context, p *auth.Principal, in SMTPInput, m auth.RequestMeta) (SMTPView, error) {
	c := in.Config
	c.Host, c.Username, c.From = strings.TrimSpace(c.Host), strings.TrimSpace(c.Username), strings.TrimSpace(c.From)
	if err := c.Validate(); err != nil {
		return SMTPView{}, err
	}
	value, _ := json.Marshal(c)
	var sealed []byte
	if in.Password != nil && *in.Password != "" {
		if s.box == nil {
			return SMTPView{}, errors.New("secret box not configured")
		}
		var err error
		if sealed, err = s.box.Seal([]byte(*in.Password), SMTPAD); err != nil {
			return SMTPView{}, err
		}
	}
	var out SMTPView
	err := s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		var before any
		if cur, err := q.GetPlatformSetting(ctx, store.GetPlatformSettingParams{OrgID: s.opts.OrgID, Key: SMTPSettingKey}); err == nil {
			before = smtpAuditState(smtpView(cur))
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		row, err := q.UpsertPlatformSetting(ctx, store.UpsertPlatformSettingParams{OrgID: s.opts.OrgID, Key: SMTPSettingKey, Value: value,
			SecretEnc: sealed, UpdatedBy: &p.UserID, SetSecret: in.Password != nil})
		if err != nil {
			return err
		}
		out = smtpView(row)
		ev := s.event(p, m, audit.SMTPSettingsUpdated)
		ev.TargetType, ev.TargetID = "platform_setting", SMTPSettingKey
		ev.Before, ev.After = before, smtpAuditState(out)
		ev.Details = map[string]any{"credential_changed": in.Password != nil}
		_, err = rec.Record(ctx, ev)
		return err
	})
	return out, err
}

func smtpAuditState(v SMTPView) map[string]any {
	return map[string]any{"host": v.Config.Host, "port": v.Config.Port, "username": v.Config.Username, "from": v.Config.From,
		"tls": v.Config.TLS, "credential_stored": v.PasswordSet}
}

// loadSMTP returns the SMTP configuration with the unsealed password.
func (s *Service) loadSMTP(ctx context.Context, orgID uuid.UUID) (SMTPConfig, error) {
	row, err := s.q.GetPlatformSetting(ctx, store.GetPlatformSettingParams{OrgID: orgID, Key: SMTPSettingKey})
	if errors.Is(err, pgx.ErrNoRows) {
		return SMTPConfig{}, ErrSMTPNotConfigured
	}
	if err != nil {
		return SMTPConfig{}, err
	}
	var c SMTPConfig
	if err := json.Unmarshal(row.Value, &c); err != nil || c.Host == "" {
		return SMTPConfig{}, ErrSMTPNotConfigured
	}
	if len(row.SecretEnc) > 0 {
		if s.box == nil {
			return SMTPConfig{}, errors.New("secret box not configured")
		}
		pw, err := s.box.Open(row.SecretEnc, SMTPAD)
		if err != nil {
			return SMTPConfig{}, fmt.Errorf("unsealing the SMTP password: %w", err)
		}
		c.Password = string(pw)
	}
	return c, nil
}
