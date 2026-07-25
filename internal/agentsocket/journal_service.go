package agentsocket

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/journal"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

type JournalProvider interface {
	Query(context.Context, journal.Query) (journal.Page, error)
}

type journalService struct{ provider JournalProvider }

func newJournalService(provider JournalProvider) *journalService {
	return &journalService{provider: provider}
}

func (service *journalService) Query(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
	if service.provider == nil {
		return nil, grpcstatus.Error(codes.Unavailable, "AGENT_JOURNAL_UNAVAILABLE")
	}
	query, err := decodeJournalQuery(request)
	if err != nil {
		return nil, grpcstatus.Error(codes.InvalidArgument, "JOURNAL_QUERY_INVALID")
	}
	page, err := service.provider.Query(ctx, query)
	if err != nil {
		return nil, grpcstatus.Error(codes.Unavailable, "AGENT_JOURNAL_UNAVAILABLE")
	}
	return dashboardStruct(page, "AGENT_JOURNAL_RESPONSE_INVALID")
}

func decodeJournalQuery(request *structpb.Struct) (journal.Query, error) {
	if request == nil {
		return journal.Query{}, errors.New("journal query is required")
	}
	values := request.AsMap()
	limit, ok := values["limit"].(float64)
	if !ok || math.Trunc(limit) != limit {
		return journal.Query{}, errors.New("journal limit is invalid")
	}
	query := journal.Query{Limit: int(limit)}
	if value, ok := values["unit"].(string); ok {
		query.Unit = value
	}
	if value, ok := values["cursor"].(string); ok {
		query.Cursor = value
	}
	if value, ok := values["since"].(string); ok && value != "" {
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			return journal.Query{}, err
		}
		query.Since = &parsed
	}
	return query, nil
}
