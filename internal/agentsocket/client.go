package agentsocket

import (
	"context"
	"errors"
	"fmt"
	"math"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"
)

type AgentStatus struct {
	ProtocolVersion string `json:"protocolVersion"`
	AgentEUID       int    `json:"agentEUID"`
	Transport       string `json:"transport"`
}

func Probe(ctx context.Context, socketPath string) (AgentStatus, error) {
	if err := ctx.Err(); err != nil {
		return AgentStatus{}, contextError(err)
	}
	if socketPath == "" {
		return AgentStatus{}, coded("AGENT_RPC_TARGET_INVALID", errors.New("socket path is required"))
	}

	connection, err := grpc.NewClient("unix://"+socketPath, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return AgentStatus{}, coded("AGENT_RPC_CONNECTION_FAILED", err)
	}
	defer connection.Close()

	response, err := NewAgentProbeServiceClient(connection).GetStatus(ctx, &emptypb.Empty{})
	if err != nil {
		return AgentStatus{}, rpcError(err)
	}
	return decodeAgentStatus(response)
}

func decodeAgentStatus(response *structpb.Struct) (AgentStatus, error) {
	if response == nil || len(response.GetFields()) != 3 {
		return AgentStatus{}, coded("AGENT_RPC_RESPONSE_INVALID", errors.New("unexpected status field count"))
	}
	protocolVersion, ok := stringField(response, "protocol_version")
	if !ok || protocolVersion == "" {
		return AgentStatus{}, coded("AGENT_RPC_RESPONSE_INVALID", errors.New("invalid protocol version"))
	}
	transport, ok := stringField(response, "transport")
	if !ok || transport != "unix" {
		return AgentStatus{}, coded("AGENT_RPC_RESPONSE_INVALID", errors.New("invalid transport"))
	}
	value, ok := response.GetFields()["agent_euid"]
	if !ok || value.GetKind() == nil {
		return AgentStatus{}, coded("AGENT_RPC_RESPONSE_INVALID", errors.New("invalid agent euid"))
	}
	uid := value.GetNumberValue()
	if uid < 0 || uid > math.MaxInt || math.Trunc(uid) != uid {
		return AgentStatus{}, coded("AGENT_RPC_RESPONSE_INVALID", fmt.Errorf("invalid agent euid %v", uid))
	}
	return AgentStatus{ProtocolVersion: protocolVersion, AgentEUID: int(uid), Transport: transport}, nil
}

func stringField(response *structpb.Struct, name string) (string, bool) {
	value, ok := response.GetFields()[name]
	if !ok || value.GetKind() == nil {
		return "", false
	}
	return value.GetStringValue(), value.GetStringValue() != ""
}

func contextError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return coded("AGENT_RPC_TIMEOUT", err)
	}
	return coded("AGENT_RPC_CANCELED", err)
}

func rpcError(err error) error {
	if errors.Is(err, context.Canceled) || grpcstatus.Code(err) == codes.Canceled {
		return coded("AGENT_RPC_CANCELED", err)
	}
	if errors.Is(err, context.DeadlineExceeded) || grpcstatus.Code(err) == codes.DeadlineExceeded {
		return coded("AGENT_RPC_TIMEOUT", err)
	}
	return coded("AGENT_RPC_UNAVAILABLE", err)
}
