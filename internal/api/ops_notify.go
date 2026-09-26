// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/AxiomOperator/dbr2/internal/notify"
	"github.com/AxiomOperator/dbr2/internal/rbac"
	"github.com/AxiomOperator/dbr2/internal/store"
)

// ChannelConfigDTO is a channel's destination.
type ChannelConfigDTO struct {
	To  []string `json:"to,omitempty" maxItems:"50" doc:"Email channels: recipient addresses."`
	URL string   `json:"url,omitempty" maxLength:"2000" doc:"Webhook channels: the endpoint. https is required; plain http is accepted only for localhost / loopback addresses. Redirects are not followed."`
}

// NotificationChannelDTO is a notification channel. Secrets are never returned.
type NotificationChannelDTO struct {
	ID             string           `json:"id" format:"uuid"`
	Name           string           `json:"name"`
	Kind           string           `json:"kind" enum:"email,webhook"`
	Enabled        bool             `json:"enabled"`
	Config         ChannelConfigDTO `json:"config"`
	SecretSet      bool             `json:"secret_set" doc:"Whether a webhook signing secret is stored (the secret itself is write-only)."`
	Events         []string         `json:"events" doc:"Subscribed event types (exact, or prefix wildcards such as backup.*); empty = all events."`
	MinSeverity    string           `json:"min_severity" enum:"info,warning,critical"`
	CreatedAt      time.Time        `json:"created_at"`
	UpdatedAt      time.Time        `json:"updated_at"`
	LastDeliveryAt *time.Time       `json:"last_delivery_at"`
	LastError      *string          `json:"last_error" doc:"Error of the most recent failed delivery; cleared by the next success."`
}

func channelDTO(c store.NotificationChannel) NotificationChannelDTO {
	cfg := notify.ParseConfig(c.Config)
	return NotificationChannelDTO{ID: c.ID.String(), Name: c.Name, Kind: c.Kind, Enabled: c.Enabled, Config: ChannelConfigDTO{To: cfg.To, URL: cfg.URL},
		SecretSet: len(c.SecretEnc) > 0, Events: nonNilStrings(c.Events), MinSeverity: c.MinSeverity, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
		LastDeliveryAt: c.LastDeliveryAt, LastError: c.LastError}
}

// NotificationDeliveryDTO is one delivery of a notification to a channel.
type NotificationDeliveryDTO struct {
	ID             int64      `json:"id"`
	NotificationID int64      `json:"notification_id"`
	EventType      string     `json:"event_type"`
	Severity       string     `json:"severity" enum:"info,warning,critical"`
	Message        string     `json:"message"`
	State          string     `json:"state" enum:"pending,sent,failed"`
	Attempts       int32      `json:"attempts"`
	NextAttemptAt  *time.Time `json:"next_attempt_at" doc:"Pending deliveries only."`
	LastError      *string    `json:"last_error"`
	SentAt         *time.Time `json:"sent_at"`
	CreatedAt      time.Time  `json:"created_at"`
}

// SMTPSettingsDTO is the SMTP relay used by email channels.
type SMTPSettingsDTO struct {
	Configured  bool       `json:"configured"`
	Host        string     `json:"host"`
	Port        int        `json:"port"`
	Username    string     `json:"username"`
	From        string     `json:"from"`
	TLS         string     `json:"tls" enum:"starttls,tls,none"`
	PasswordSet bool       `json:"password_set" doc:"Whether a password is stored (the password itself is write-only)."`
	UpdatedAt   *time.Time `json:"updated_at"`
}

func smtpDTO(v notify.SMTPView) SMTPSettingsDTO {
	tls := v.Config.TLS
	if tls == "" {
		tls = notify.TLSStartTLS
	}
	return SMTPSettingsDTO{Configured: v.Configured, Host: v.Config.Host, Port: v.Config.Port, Username: v.Config.Username, From: v.Config.From,
		TLS: tls, PasswordSet: v.PasswordSet, UpdatedAt: v.UpdatedAt}
}

// NotificationChannelWrite is the writable part of a channel.
type NotificationChannelWrite struct {
	Name        string           `json:"name" minLength:"1" maxLength:"100"`
	Config      ChannelConfigDTO `json:"config"`
	Secret      *string          `json:"secret,omitempty" maxLength:"500" writeOnly:"true" doc:"Webhook signing secret (min. 16 characters). Requests then carry X-DBR2-Signature: sha256=<hex HMAC-SHA256(secret, X-DBR2-Timestamp + \".\" + body)>. On update: omit to keep, empty string to remove."`
	Events      []string         `json:"events,omitempty" maxItems:"100" doc:"Event types to deliver (exact or prefix wildcard such as backup.*); empty or omitted = all."`
	MinSeverity string           `json:"min_severity,omitempty" enum:"info,warning,critical" default:"warning"`
}

func (b NotificationChannelWrite) input(kind string, enabled bool) notify.ChannelInput {
	return notify.ChannelInput{Name: b.Name, Kind: kind, Enabled: enabled, To: b.Config.To, URL: b.Config.URL, Secret: b.Secret,
		Events: b.Events, MinSeverity: b.MinSeverity}
}

func (d *Deps) notifyErr(ctx context.Context, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, notify.ErrNotFound):
		return problem(http.StatusNotFound, CodeNotFound, "not found")
	case errors.Is(err, notify.ErrInvalid):
		return problem(http.StatusBadRequest, CodeValidation, err.Error())
	case errors.Is(err, notify.ErrConflict):
		return problem(http.StatusConflict, CodeConflict, err.Error())
	}
	return d.fail(ctx, err)
}

type channelOut struct{ Body NotificationChannelDTO }

func registerNotify(a huma.API, d *Deps) {
	const tag, settingsTag = "Notifications", "Settings"
	const base = "/api/v1/notification-channels"

	huma.Register(a, op("list-notification-channels", http.MethodGet, base, tag,
		"List notification channels", "Email and webhook channels that receive alerts (backup failed, restore finished, agent offline, …). Secrets are never returned.",
		rbac.PolicyRead),
		func(ctx context.Context, _ *struct{}) (*struct {
			Body struct {
				Items []NotificationChannelDTO `json:"items"`
			}
		}, error) {
			rows, err := d.Notify.ListChannels(ctx)
			if err != nil {
				return nil, d.notifyErr(ctx, err)
			}
			out := &struct {
				Body struct {
					Items []NotificationChannelDTO `json:"items"`
				}
			}{}
			out.Body.Items = []NotificationChannelDTO{}
			for _, c := range rows {
				out.Body.Items = append(out.Body.Items, channelDTO(c))
			}
			return out, nil
		})

	huma.Register(a, withStatus(op("create-notification-channel", http.MethodPost, base, tag,
		"Create a notification channel",
		"Creates an email channel (`config.to`; uses the SMTP settings) or a webhook channel (`config.url`, optional signing `secret`). "+
			"Notifications whose type matches `events` and whose severity is at least `min_severity` are delivered, with retries "+
			"(1 m, 5 m, 30 m, 2 h, 6 h; failed after 6 attempts).",
		rbac.PolicyManage, http.StatusBadRequest, http.StatusConflict), http.StatusCreated),
		func(ctx context.Context, in *struct {
			Body struct {
				NotificationChannelWrite
				Kind    string `json:"kind" enum:"email,webhook"`
				Enabled *bool  `json:"enabled,omitempty" default:"true"`
			}
		}) (*channelOut, error) {
			enabled := in.Body.Enabled == nil || *in.Body.Enabled
			c, err := d.Notify.CreateChannel(ctx, principal(ctx), in.Body.input(in.Body.Kind, enabled), metaFrom(ctx))
			if err != nil {
				return nil, d.notifyErr(ctx, err)
			}
			return &channelOut{channelDTO(c)}, nil
		})

	huma.Register(a, op("get-notification-channel", http.MethodGet, base+"/{id}", tag,
		"Get a notification channel", "Secrets are never returned (`secret_set` tells whether one is stored).", rbac.PolicyRead, http.StatusNotFound),
		func(ctx context.Context, in *idPath) (*channelOut, error) {
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			c, err := d.Notify.GetChannel(ctx, id)
			if err != nil {
				return nil, d.notifyErr(ctx, err)
			}
			return &channelOut{channelDTO(c)}, nil
		})

	huma.Register(a, op("update-notification-channel", http.MethodPut, base+"/{id}", tag,
		"Update a notification channel", "Replaces the channel's settings (its kind cannot change). Omit `secret` to keep the stored secret, send an empty string to remove it.",
		rbac.PolicyManage, http.StatusBadRequest, http.StatusNotFound, http.StatusConflict),
		func(ctx context.Context, in *struct {
			ID   string `path:"id" format:"uuid"`
			Body struct {
				NotificationChannelWrite
				Enabled bool `json:"enabled"`
			}
		}) (*channelOut, error) {
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			c, err := d.Notify.UpdateChannel(ctx, principal(ctx), id, in.Body.input("", in.Body.Enabled), metaFrom(ctx))
			if err != nil {
				return nil, d.notifyErr(ctx, err)
			}
			return &channelOut{channelDTO(c)}, nil
		})

	huma.Register(a, withStatus(op("delete-notification-channel", http.MethodDelete, base+"/{id}", tag,
		"Delete a notification channel", "Deletes the channel and its delivery history. Alerts themselves are kept.", rbac.PolicyManage, http.StatusNotFound), http.StatusNoContent),
		func(ctx context.Context, in *idPath) (*struct{}, error) {
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			return nil, d.notifyErr(ctx, d.Notify.DeleteChannel(ctx, principal(ctx), id, metaFrom(ctx)))
		})

	huma.Register(a, op("test-notification-channel", http.MethodPost, base+"/{id}/test", tag,
		"Send a test notification",
		"Sends a synthetic `notification.test` message (severity info) straight to the channel, ignoring its filters and enabled flag, and returns the "+
			"outcome. A failed delivery is reported in the body (`delivered: false`), not as an HTTP error. Audited.",
		rbac.PolicyManage, http.StatusNotFound),
		func(ctx context.Context, in *idPath) (*struct {
			Body struct {
				Delivered  bool   `json:"delivered"`
				Error      string `json:"error,omitempty"`
				DurationMS int64  `json:"duration_ms"`
			}
		}, error) {
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			res, err := d.Notify.TestChannel(ctx, principal(ctx), id, metaFrom(ctx))
			if err != nil {
				return nil, d.notifyErr(ctx, err)
			}
			out := &struct {
				Body struct {
					Delivered  bool   `json:"delivered"`
					Error      string `json:"error,omitempty"`
					DurationMS int64  `json:"duration_ms"`
				}
			}{}
			out.Body.Delivered, out.Body.Error, out.Body.DurationMS = res.Delivered, res.Error, res.Duration.Milliseconds()
			return out, nil
		})

	huma.Register(a, op("list-notification-deliveries", http.MethodGet, base+"/{id}/deliveries", tag,
		"List a channel's recent deliveries", "Most recent first, with state, attempts and the last error.", rbac.PolicyRead, http.StatusNotFound),
		func(ctx context.Context, in *struct {
			ID    string `path:"id" format:"uuid"`
			Limit int32  `query:"limit" minimum:"1" maximum:"500" default:"50"`
		}) (*struct {
			Body struct {
				Items []NotificationDeliveryDTO `json:"items"`
			}
		}, error) {
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			rows, err := d.Notify.ListDeliveries(ctx, id, in.Limit)
			if err != nil {
				return nil, d.notifyErr(ctx, err)
			}
			out := &struct {
				Body struct {
					Items []NotificationDeliveryDTO `json:"items"`
				}
			}{}
			out.Body.Items = []NotificationDeliveryDTO{}
			for _, r := range rows {
				dto := NotificationDeliveryDTO{ID: r.ID, NotificationID: r.NotificationID, EventType: r.EventType, Severity: r.Severity, Message: r.Message,
					State: r.State, Attempts: r.Attempts, LastError: r.LastError, SentAt: r.SentAt, CreatedAt: r.CreatedAt}
				if r.State == "pending" {
					t := r.NextAttemptAt
					dto.NextAttemptAt = &t
				}
				out.Body.Items = append(out.Body.Items, dto)
			}
			return out, nil
		})

	// ---- SMTP settings ----
	huma.Register(a, op("get-smtp-settings", http.MethodGet, "/api/v1/settings/smtp", settingsTag,
		"Get the SMTP settings", "The SMTP relay used by email channels. The password is write-only (`password_set`).", rbac.PolicyRead),
		func(ctx context.Context, _ *struct{}) (*struct{ Body SMTPSettingsDTO }, error) {
			v, err := d.Notify.SMTPSettings(ctx)
			if err != nil {
				return nil, d.notifyErr(ctx, err)
			}
			return &struct{ Body SMTPSettingsDTO }{smtpDTO(v)}, nil
		})

	huma.Register(a, op("update-smtp-settings", http.MethodPut, "/api/v1/settings/smtp", settingsTag,
		"Update the SMTP settings",
		"Replaces the SMTP relay settings. `tls`: `starttls` (typically port 587), `tls` (implicit TLS, typically 465) or `none` (plain text; "+
			"allowed only for a relay on localhost). PLAIN authentication is used when `username` is set. Omit `password` to keep the stored "+
			"password, send an empty string to remove it. The password is sealed at rest and never returned. Audited.",
		rbac.PolicyManage, http.StatusBadRequest),
		func(ctx context.Context, in *struct {
			Body struct {
				Host     string  `json:"host" minLength:"1" maxLength:"253"`
				Port     int     `json:"port" minimum:"1" maximum:"65535"`
				Username string  `json:"username,omitempty" maxLength:"200"`
				From     string  `json:"from" minLength:"3" maxLength:"320" doc:"Sender address, e.g. DBR2 <dbr2@example.com>."`
				TLS      string  `json:"tls" enum:"starttls,tls,none"`
				Password *string `json:"password,omitempty" maxLength:"500" writeOnly:"true"`
			}
		}) (*struct{ Body SMTPSettingsDTO }, error) {
			b := in.Body
			v, err := d.Notify.UpdateSMTPSettings(ctx, principal(ctx), notify.SMTPInput{Config: notify.SMTPConfig{Host: b.Host, Port: b.Port,
				Username: b.Username, From: b.From, TLS: b.TLS}, Password: b.Password}, metaFrom(ctx))
			if err != nil {
				return nil, d.notifyErr(ctx, err)
			}
			return &struct{ Body SMTPSettingsDTO }{smtpDTO(v)}, nil
		})
}
