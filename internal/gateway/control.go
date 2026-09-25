// SPDX-License-Identifier: Apache-2.0

package gateway

import (
	"context"
	"crypto/subtle"
	"errors"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	agentv1 "github.com/AxiomOperator/dbr2/internal/agentpb/agent/v1"
	controlv1 "github.com/AxiomOperator/dbr2/internal/agentpb/control/v1"
)

// ControlServer builds the internal gRPC server used by dbr2-worker
// (Temporal activities) to dispatch commands (ADR-0001). It listens on the
// deployment network only and requires the shared internal token.
func (g *Gateway) ControlServer(token string) *grpc.Server {
	s := grpc.NewServer(grpc.StreamInterceptor(func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, h grpc.StreamHandler) error {
		if err := checkToken(ss.Context(), token); err != nil {
			return err
		}
		return h(srv, ss)
	}), grpc.UnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
		if err := checkToken(ctx, token); err != nil {
			return nil, err
		}
		return h(ctx, req)
	}))
	controlv1.RegisterGatewayControlServiceServer(s, &controlServer{g: g})
	return s
}

func checkToken(ctx context.Context, token string) error {
	md, _ := metadata.FromIncomingContext(ctx)
	for _, v := range md.Get("authorization") {
		if tok, ok := strings.CutPrefix(v, "Bearer "); ok && token != "" && subtle.ConstantTimeCompare([]byte(tok), []byte(token)) == 1 {
			return nil
		}
	}
	return status.Error(codes.Unauthenticated, "internal token required")
}

type controlServer struct {
	controlv1.UnimplementedGatewayControlServiceServer
	g *Gateway
}

func (c *controlServer) Dispatch(req *controlv1.DispatchRequest, stream controlv1.GatewayControlService_DispatchServer) error {
	if req.Command == nil || req.Command.CommandId == "" || req.AgentId == "" {
		return status.Error(codes.InvalidArgument, "agent_id and command.command_id are required")
	}
	ctx := stream.Context()
	if req.WaitForAgentSeconds > 0 && !c.g.Connected(req.AgentId) {
		wctx, cancel := context.WithTimeout(ctx, time.Duration(req.WaitForAgentSeconds)*time.Second)
		_, err := c.g.waitSession(wctx, req.AgentId)
		cancel()
		if err != nil {
			return status.Error(codes.Unavailable, err.Error())
		}
	}
	final, err := c.g.Dispatch(ctx, req.AgentId, req.Command, func(u *agentv1.CommandUpdate) {
		_ = stream.Send(&controlv1.DispatchResponse{Update: u})
	})
	switch {
	case errors.Is(err, ErrAgentNotActive):
		return status.Error(codes.FailedPrecondition, err.Error())
	case err != nil:
		return status.Error(codes.Unavailable, err.Error())
	}
	_ = final // already streamed through onUpdate
	return nil
}
