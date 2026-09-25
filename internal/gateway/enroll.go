// SPDX-License-Identifier: Apache-2.0

package gateway

import (
	"context"
	"errors"
	"net/netip"
	"strings"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	agentv1 "github.com/AxiomOperator/dbr2/internal/agentpb/agent/v1"
	"github.com/AxiomOperator/dbr2/internal/audit"
	"github.com/AxiomOperator/dbr2/internal/auth"
	"github.com/AxiomOperator/dbr2/internal/pki"
	"github.com/AxiomOperator/dbr2/internal/store"
)

// RegistrationTokenPrefix marks agent registration tokens.
const RegistrationTokenPrefix = "dbr2reg_"

type enrollServer struct {
	agentv1.UnimplementedEnrollmentServiceServer
	g *Gateway
}

func (e *enrollServer) GetCA(context.Context, *agentv1.GetCARequest) (*agentv1.GetCAResponse, error) {
	return &agentv1.GetCAResponse{CaCertificateDer: e.g.ca.DER}, nil
}

func peerIP(ctx context.Context) netip.Addr {
	if p, ok := peer.FromContext(ctx); ok {
		if ap, err := netip.ParseAddrPort(p.Addr.String()); err == nil {
			return ap.Addr().Unmap()
		}
	}
	return netip.Addr{}
}

// Enroll exchanges a single-use registration token and a CSR for an agent
// identity. The agent starts pending; an administrator approves it.
func (e *enrollServer) Enroll(ctx context.Context, req *agentv1.EnrollRequest) (*agentv1.EnrollResponse, error) {
	g := e.g
	if !strings.HasPrefix(req.Token, RegistrationTokenPrefix) || len(req.CsrDer) == 0 || strings.TrimSpace(req.Hostname) == "" {
		return nil, status.Error(codes.InvalidArgument, "token, csr and hostname are required")
	}
	if err := CheckProtocol(req.ProtocolVersion); err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	ip := peerIP(ctx)
	var resp *agentv1.EnrollResponse
	err := pgx.BeginFunc(ctx, g.pool, func(tx pgx.Tx) error {
		q := g.q.WithTx(tx)
		rec := g.audit.WithQuerier(q)
		tok, err := q.ClaimRegistrationToken(ctx, auth.HashToken(req.Token))
		if errors.Is(err, pgx.ErrNoRows) {
			return errInvalidToken
		}
		if err != nil {
			return err
		}
		a, err := q.CreateAgent(ctx, store.CreateAgentParams{
			OrgID: tok.OrgID, Hostname: req.Hostname, AgentVersion: req.AgentVersion, ProtocolVersion: req.ProtocolVersion,
			OsRelease: strPtr(req.OsRelease), Architecture: strPtr(req.Architecture), RegistrationTokenID: &tok.ID,
		})
		if err != nil {
			return err
		}
		iss, err := g.ca.IssueAgent(req.CsrDer, a.ID.String())
		if err != nil {
			return status.Error(codes.InvalidArgument, err.Error())
		}
		if err := q.InsertAgentCertificate(ctx, store.InsertAgentCertificateParams{Serial: iss.Serial, AgentID: a.ID,
			Fingerprint: iss.Fingerprint, NotBefore: iss.NotBefore, NotAfter: iss.NotAfter}); err != nil {
			return err
		}
		if err := q.SetAgentCertificate(ctx, store.SetAgentCertificateParams{ID: a.ID, CertSerial: &iss.Serial,
			CertFingerprint: &iss.Fingerprint, CertNotAfter: &iss.NotAfter}); err != nil {
			return err
		}
		if err := q.SetRegistrationTokenAgent(ctx, store.SetRegistrationTokenAgentParams{ID: tok.ID, UsedByAgent: &a.ID}); err != nil {
			return err
		}
		if _, err := rec.Record(ctx, audit.Event{OrgID: tok.OrgID, Type: audit.AgentEnrolled, ActorDisplay: "agent@" + req.Hostname,
			ActorKind: audit.ActorSystem, SourceIP: ip, TargetType: "agent", TargetID: a.ID.String(), Result: audit.Success,
			Details: map[string]any{"hostname": req.Hostname, "agent_version": req.AgentVersion, "registration_token": tok.ID.String(),
				"certificate_serial": iss.Serial}}); err != nil {
			return err
		}
		resp = &agentv1.EnrollResponse{AgentId: a.ID.String(), CertificateDer: iss.DER, CaCertificateDer: g.ca.DER, Status: a.Status}
		return nil
	})
	if errors.Is(err, errInvalidToken) {
		_, _ = g.audit.Record(ctx, audit.Event{OrgID: g.cfg.OrgID, Type: audit.AgentEnrollmentFailed, ActorDisplay: "agent@" + req.Hostname,
			ActorKind: audit.ActorAnonymous, SourceIP: ip, Result: audit.Denied, Reason: "invalid, used, revoked or expired registration token"})
		return nil, status.Error(codes.PermissionDenied, "registration token is invalid, already used, revoked or expired")
	}
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		g.log.ErrorContext(ctx, "enrollment failed", "err", err)
		return nil, status.Error(codes.Internal, "enrollment failed")
	}
	g.log.InfoContext(ctx, "agent enrolled (pending approval)", "agent_id", resp.AgentId, "hostname", req.Hostname)
	return resp, nil
}

var errInvalidToken = errors.New("invalid registration token")

// Renew issues a new certificate to an authenticated agent and revokes its
// previous certificates.
func (e *enrollServer) Renew(ctx context.Context, req *agentv1.RenewRequest) (*agentv1.RenewResponse, error) {
	g := e.g
	agentID, cert, err := peerAgent(ctx)
	if err != nil {
		return nil, err
	}
	row, err := g.q.GetAgentCertificate(ctx, pki.SerialHex(cert))
	if err != nil || row.AgentID.String() != agentID || row.RevokedAt != nil {
		return nil, status.Error(codes.PermissionDenied, "certificate unknown or revoked")
	}
	ag, err := g.q.GetAgent(ctx, row.AgentID)
	if err != nil || ag.Status == "revoked" {
		return nil, status.Error(codes.PermissionDenied, "agent revoked")
	}
	iss, err := g.ca.IssueAgent(req.CsrDer, agentID)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	err = pgx.BeginFunc(ctx, g.pool, func(tx pgx.Tx) error {
		q := g.q.WithTx(tx)
		if err := q.InsertAgentCertificate(ctx, store.InsertAgentCertificateParams{Serial: iss.Serial, AgentID: ag.ID,
			Fingerprint: iss.Fingerprint, NotBefore: iss.NotBefore, NotAfter: iss.NotAfter}); err != nil {
			return err
		}
		if err := q.SetAgentCertificate(ctx, store.SetAgentCertificateParams{ID: ag.ID, CertSerial: &iss.Serial,
			CertFingerprint: &iss.Fingerprint, CertNotAfter: &iss.NotAfter}); err != nil {
			return err
		}
		if err := q.RevokeAgentCertificatesExcept(ctx, store.RevokeAgentCertificatesExceptParams{AgentID: ag.ID, Serial: iss.Serial}); err != nil {
			return err
		}
		_, err := g.audit.WithQuerier(q).Record(ctx, audit.Event{OrgID: ag.OrgID, Type: audit.AgentCertRenewed, ActorDisplay: "agent@" + ag.Hostname,
			ActorKind: audit.ActorSystem, SourceIP: peerIP(ctx), TargetType: "agent", TargetID: agentID, Result: audit.Success,
			Details: map[string]any{"certificate_serial": iss.Serial, "not_after": iss.NotAfter}})
		return err
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "renewal failed")
	}
	return &agentv1.RenewResponse{CertificateDer: iss.DER}, nil
}
