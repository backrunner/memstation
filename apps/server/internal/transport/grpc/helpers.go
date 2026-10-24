package grpctransport

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	structpb "google.golang.org/protobuf/types/known/structpb"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	wrapperspb "google.golang.org/protobuf/types/known/wrapperspb"

	"memstation/apps/server/internal/domain"
	pb "memstation/apps/server/proto/memstation/v1"
)

func mapError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := status.FromError(err); ok {
		return err
	}

	if errors.Is(err, domain.ErrAccessDenied) {
		return status.Error(codes.PermissionDenied, "access denied")
	}
	if errors.Is(err, domain.ErrAccessConflict) {
		return status.Error(codes.Aborted, "revision conflict")
	}
	lowered := strings.ToLower(err.Error())
	switch {
	case strings.Contains(lowered, "not found"):
		return status.Error(codes.NotFound, err.Error())
	case strings.Contains(lowered, "required"),
		strings.Contains(lowered, "invalid"),
		strings.Contains(lowered, "unsupported"):
		return status.Error(codes.InvalidArgument, err.Error())
	case strings.Contains(lowered, "unauthorized"),
		strings.Contains(lowered, "missing api key"):
		return status.Error(codes.Unauthenticated, err.Error())
	case strings.Contains(lowered, "unavailable"),
		strings.Contains(lowered, "provider"):
		return status.Error(codes.Unavailable, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}

func toProtoStruct(value map[string]any) *structpb.Struct {
	if len(value) == 0 {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	out := &structpb.Struct{}
	if err := protojson.Unmarshal(data, out); err != nil {
		return nil
	}
	return out
}

func toProtoValue(value any) *structpb.Value {
	if value == nil {
		return nil
	}
	out, err := structpb.NewValue(value)
	if err != nil {
		return nil
	}
	return out
}

func fromProtoStruct(value *structpb.Struct) map[string]any {
	if value == nil {
		return nil
	}
	return value.AsMap()
}

func fromProtoValue(value *structpb.Value) any {
	if value == nil {
		return nil
	}
	return value.AsInterface()
}

func toProtoTimestamp(value time.Time) *timestamppb.Timestamp {
	if value.IsZero() {
		return nil
	}
	return timestamppb.New(value.UTC())
}

func toProtoOptionalTimestamp(value *time.Time) *timestamppb.Timestamp {
	if value == nil {
		return nil
	}
	return toProtoTimestamp(value.UTC())
}

func parseTimestamp(value *timestamppb.Timestamp, field string) (time.Time, error) {
	if value == nil {
		return time.Time{}, nil
	}
	if err := value.CheckValid(); err != nil {
		return time.Time{}, status.Errorf(codes.InvalidArgument, "%s is invalid: %v", field, err)
	}
	return value.AsTime().UTC(), nil
}

func parseOptionalTimestamp(value *timestamppb.Timestamp, field string) (*time.Time, error) {
	if value == nil {
		return nil, nil
	}
	parsed, err := parseTimestamp(value, field)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func fromProtoBoolValue(value *wrapperspb.BoolValue) *bool {
	if value == nil {
		return nil
	}
	out := value.GetValue()
	return &out
}

func parseTimeTiers(values []string) ([]domain.TimeTier, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := make([]domain.TimeTier, 0, len(values))
	for _, value := range values {
		tier := domain.ParseTimeTier(value)
		if tier == "" {
			return nil, status.Errorf(codes.InvalidArgument, "unsupported time tier %q", value)
		}
		out = append(out, tier)
	}
	return out, nil
}

func parseMemoryTypes(values []string) ([]domain.MemoryType, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := make([]domain.MemoryType, 0, len(values))
	for _, value := range values {
		kind := domain.MemoryType(strings.TrimSpace(strings.ToLower(value)))
		if !domain.IsSupportedMemoryType(kind) {
			return nil, status.Errorf(codes.InvalidArgument, "unsupported memory type %q", value)
		}
		out = append(out, kind)
	}
	return out, nil
}

func defaultAdminUserID(userID string) string {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return "admin"
	}
	return userID
}

func toProtoHealthComponents(components []domainHealthComponent) []*pb.HealthComponent {
	out := make([]*pb.HealthComponent, 0, len(components))
	for _, component := range components {
		out = append(out, &pb.HealthComponent{
			Name:   component.Name,
			Status: component.Status,
			Error:  component.Error,
		})
	}
	return out
}

type domainHealthComponent struct {
	Name   string
	Status string
	Error  string
}

func toProtoModelCapabilities(items []domain.ModelCapability) []*pb.ModelCapability {
	out := make([]*pb.ModelCapability, 0, len(items))
	for _, item := range items {
		out = append(out, &pb.ModelCapability{
			Id:             item.ID,
			Provider:       string(item.Provider),
			Role:           item.Role,
			SupportsTools:  item.SupportsTools,
			SupportsStream: item.SupportsStream,
		})
	}
	return out
}

func toProtoUsage(value domain.UsageRecord) *pb.UsageRecord {
	return &pb.UsageRecord{
		InputTokens:  int32(value.InputTokens),
		OutputTokens: int32(value.OutputTokens),
		TotalTokens:  int32(value.TotalTokens),
	}
}

func toProtoContentBlocks(items []domain.ContentBlock) []*pb.ContentBlock {
	out := make([]*pb.ContentBlock, 0, len(items))
	for _, item := range items {
		out = append(out, &pb.ContentBlock{
			Type:     string(item.Type),
			Text:     item.Text,
			Raw:      item.Raw,
			Metadata: toProtoStruct(item.Metadata),
		})
	}
	return out
}

func toProtoToolCalls(items []domain.ToolCall) []*pb.ToolCall {
	out := make([]*pb.ToolCall, 0, len(items))
	for _, item := range items {
		out = append(out, &pb.ToolCall{
			Id:        item.ID,
			Name:      item.Name,
			Arguments: toProtoValue(item.Arguments),
		})
	}
	return out
}

func toProtoToolResults(items []domain.ToolResult) []*pb.ToolResult {
	out := make([]*pb.ToolResult, 0, len(items))
	for _, item := range items {
		out = append(out, &pb.ToolResult{
			ToolCallId: item.ToolCallID,
			Name:       item.Name,
			Output:     toProtoValue(item.Output),
		})
	}
	return out
}

func toProtoUnifiedMessage(item domain.UnifiedMessage) *pb.UnifiedMessage {
	return &pb.UnifiedMessage{
		Role:        string(item.Role),
		Text:        item.Text,
		Blocks:      toProtoContentBlocks(item.Blocks),
		ToolCalls:   toProtoToolCalls(item.ToolCalls),
		ToolResults: toProtoToolResults(item.ToolResults),
		Metadata:    toProtoStruct(item.Metadata),
	}
}

func toProtoUnifiedMessages(items []domain.UnifiedMessage) []*pb.UnifiedMessage {
	out := make([]*pb.UnifiedMessage, 0, len(items))
	for _, item := range items {
		out = append(out, toProtoUnifiedMessage(item))
	}
	return out
}

func fromProtoUnifiedMessage(item *pb.UnifiedMessage) domain.UnifiedMessage {
	if item == nil {
		return domain.UnifiedMessage{}
	}
	return domain.UnifiedMessage{
		Role:        domain.MessageRole(strings.TrimSpace(strings.ToLower(item.GetRole()))),
		Text:        item.GetText(),
		Blocks:      fromProtoContentBlocks(item.GetBlocks()),
		ToolCalls:   fromProtoToolCalls(item.GetToolCalls()),
		ToolResults: fromProtoToolResults(item.GetToolResults()),
		Metadata:    fromProtoStruct(item.GetMetadata()),
	}
}

func fromProtoUnifiedMessages(items []*pb.UnifiedMessage) []domain.UnifiedMessage {
	out := make([]domain.UnifiedMessage, 0, len(items))
	for _, item := range items {
		out = append(out, fromProtoUnifiedMessage(item))
	}
	return out
}

func fromProtoContentBlocks(items []*pb.ContentBlock) []domain.ContentBlock {
	out := make([]domain.ContentBlock, 0, len(items))
	for _, item := range items {
		out = append(out, domain.ContentBlock{
			Type:     domain.ContentBlockType(strings.TrimSpace(strings.ToLower(item.GetType()))),
			Text:     item.GetText(),
			Raw:      item.GetRaw(),
			Metadata: fromProtoStruct(item.GetMetadata()),
		})
	}
	return out
}

func fromProtoToolCalls(items []*pb.ToolCall) []domain.ToolCall {
	out := make([]domain.ToolCall, 0, len(items))
	for _, item := range items {
		out = append(out, domain.ToolCall{
			ID:        item.GetId(),
			Name:      item.GetName(),
			Arguments: fromProtoValue(item.GetArguments()),
		})
	}
	return out
}

func fromProtoToolResults(items []*pb.ToolResult) []domain.ToolResult {
	out := make([]domain.ToolResult, 0, len(items))
	for _, item := range items {
		out = append(out, domain.ToolResult{
			ToolCallID: item.GetToolCallId(),
			Name:       item.GetName(),
			Output:     fromProtoValue(item.GetOutput()),
		})
	}
	return out
}

func fromProtoToolDefinitions(items []*pb.ToolDefinition) []domain.ToolDefinition {
	out := make([]domain.ToolDefinition, 0, len(items))
	for _, item := range items {
		out = append(out, domain.ToolDefinition{
			Name:        item.GetName(),
			Description: item.GetDescription(),
			InputSchema: fromProtoStruct(item.GetInputSchema()),
			ReadOnly:    item.GetReadOnly(),
		})
	}
	return out
}

func fromProtoUnifiedRequest(item *pb.UnifiedRequest) (domain.UnifiedRequest, error) {
	if item == nil {
		return domain.UnifiedRequest{}, status.Error(codes.InvalidArgument, "request is required")
	}
	if strings.TrimSpace(item.GetProtocol()) == "" {
		return domain.UnifiedRequest{}, status.Error(codes.InvalidArgument, "request.protocol is required")
	}
	receivedAt, err := parseTimestamp(item.GetReceivedAt(), "request.received_at")
	if err != nil {
		return domain.UnifiedRequest{}, err
	}
	return domain.UnifiedRequest{
		RequestID:          item.GetRequestId(),
		ConversationID:     item.GetConversationId(),
		SessionID:          item.GetSessionId(),
		Protocol:           domain.Protocol(strings.TrimSpace(item.GetProtocol())),
		Provider:           domain.Provider(strings.TrimSpace(strings.ToLower(item.GetProvider()))),
		Mode:               domain.Mode(strings.TrimSpace(strings.ToLower(item.GetMode()))),
		Model:              item.GetModel(),
		Stream:             item.GetStream(),
		EndConversation:    item.GetEndConversation(),
		InjectContext:      item.GetInjectContext(),
		InjectTools:        item.GetInjectTools(),
		ToolWriteEnabled:   item.GetToolWriteEnabled(),
		MaxInjectionTokens: int(item.GetMaxInjectionTokens()),
		MemoryPersist:      fromProtoBoolValue(item.GetMemoryPersist()),
		SourceRetain:       fromProtoBoolValue(item.GetSourceRetain()),
		Messages:           fromProtoUnifiedMessages(item.GetMessages()),
		Tools:              fromProtoToolDefinitions(item.GetTools()),
		ProviderMetadata:   fromProtoStruct(item.GetProviderMetadata()),
		RequestMetadata:    fromProtoStruct(item.GetRequestMetadata()),
		ReceivedAt:         receivedAt,
	}, nil
}

func toProtoUnifiedResponse(item *domain.UnifiedResponse) *pb.UnifiedResponse {
	if item == nil {
		return nil
	}
	return &pb.UnifiedResponse{
		ResponseId: item.ResponseID,
		Protocol:   string(item.Protocol),
		Provider:   string(item.Provider),
		Model:      item.Model,
		Message:    toProtoUnifiedMessage(item.Message),
		Usage:      toProtoUsage(item.Usage),
		CreatedAt:  toProtoTimestamp(item.CreatedAt),
		Raw:        toProtoStruct(item.Raw),
	}
}

func toProtoRelatedMemories(items []domain.RelatedMemory) []*pb.RelatedMemory {
	out := make([]*pb.RelatedMemory, 0, len(items))
	for _, item := range items {
		out = append(out, &pb.RelatedMemory{
			NodeId:       item.NodeID,
			DocumentId:   item.DocumentID,
			DocumentType: item.DocumentType,
			RelationType: item.RelationType,
			Weight:       item.Weight,
		})
	}
	return out
}

func toProtoMemoryDocument(item domain.MemoryDocument) *pb.MemoryDocument {
	item.Metadata = domain.PublicMemoryMetadata(item.Metadata)
	return &pb.MemoryDocument{
		Id:             item.ID,
		NodeId:         item.NodeID,
		GenerationId:   item.GenerationID,
		UserId:         item.UserID,
		ConversationId: item.ConversationID,
		Type:           string(item.Type),
		Content:        item.Content,
		Score:          item.Score,
		EffectiveScore: item.EffectiveScore,
		Importance:     item.Importance,
		DecayFactor:    item.DecayFactor,
		ActivityAt:     toProtoTimestamp(item.ActivityAt),
		TimeTier:       string(item.TimeTier),
		Metadata:       toProtoStruct(item.Metadata),
		Related:        toProtoRelatedMemories(item.Related),
		CreatedAt:      toProtoTimestamp(item.CreatedAt),
		UpdatedAt:      toProtoTimestamp(item.UpdatedAt),
		Lineage:        toProtoFactLineage(item.Lineage),
	}
}

func toProtoFactLineage(item *domain.MemoryLineageMeta) *pb.FactLineage {
	if item == nil {
		return nil
	}
	return &pb.FactLineage{
		FactKey:                item.FactKey,
		LineageGroupId:         item.LineageGroupID,
		RevisionSeq:            int32(item.RevisionSeq),
		GenerationId:           item.GenerationID,
		LineagePath:            append([]string(nil), item.LineagePath...),
		SupersededAliasNodeIds: append([]string(nil), item.SupersededAliasNodeIDs...),
		RevisionDepth:          int32(item.RevisionDepth),
		CurrentHeadId:          item.CurrentHeadID,
	}
}

func toProtoSearchResult(item domain.SearchResult) *pb.SearchResponse {
	out := &pb.SearchResponse{
		Total:                int32(item.Total),
		QueryEmbeddingTokens: int32(item.QueryEmbeddingTokens),
		Memories:             make([]*pb.MemoryDocument, 0, len(item.Memories)),
	}
	for _, memory := range item.Memories {
		out.Memories = append(out.Memories, toProtoMemoryDocument(memory))
	}
	return out
}

func toProtoInjectionResult(item domain.InjectionResult) *pb.InjectionResult {
	if item.Query == "" && item.Search.Total == 0 {
		return nil
	}
	return &pb.InjectionResult{
		Query:  item.Query,
		Search: toProtoSearchResult(item.Search),
	}
}

func toProtoKeyMemory(item domain.KeyMemory) *pb.KeyMemory {
	return &pb.KeyMemory{
		Id:              item.ID,
		UserId:          item.UserID,
		Key:             item.Key,
		Value:           item.Value,
		Category:        item.Category,
		Source:          item.Source,
		ReviewStatus:    item.ReviewStatus,
		LastConfirmedAt: toProtoOptionalTimestamp(item.LastConfirmedAt),
		Importance:      item.Importance,
		DecayFactor:     item.DecayFactor,
		ActivityAt:      toProtoTimestamp(item.ActivityAt),
		TimeTier:        string(item.TimeTier),
		EffectiveScore:  item.EffectiveScore,
		Metadata:        toProtoStruct(item.Metadata),
		CreatedAt:       toProtoTimestamp(item.CreatedAt),
		UpdatedAt:       toProtoTimestamp(item.UpdatedAt),
	}
}

func toProtoConversationListItem(item adminConversationListItem) *pb.ConversationListItem {
	return &pb.ConversationListItem{
		ConversationId: item.ConversationID,
		UserId:         item.UserID,
		Provider:       item.Provider,
		Protocol:       item.Protocol,
		Mode:           item.Mode,
		FinalizeStatus: item.FinalizeStatus,
		SummaryStatus:  item.SummaryStatus,
		AnalysisStatus: item.AnalysisStatus,
		MessageCount:   int32(item.MessageCount),
		TotalTokens:    int32(item.TotalTokens),
		UpdatedAt:      toProtoTimestamp(item.UpdatedAt),
		Metadata:       toProtoStruct(item.Metadata),
	}
}

type adminConversationListItem struct {
	ConversationID string
	UserID         string
	Provider       string
	Protocol       string
	Mode           string
	FinalizeStatus string
	SummaryStatus  string
	AnalysisStatus string
	MessageCount   int
	TotalTokens    int
	UpdatedAt      time.Time
	Metadata       map[string]any
}

func toProtoConversationSegments(items []domain.ConversationSegment) []*pb.ConversationSegment {
	out := make([]*pb.ConversationSegment, 0, len(items))
	for _, item := range items {
		out = append(out, &pb.ConversationSegment{
			Id:                  item.ID,
			GenerationId:        item.GenerationID,
			UserId:              item.UserID,
			ConversationId:      item.ConversationID,
			SegmentIndex:        int32(item.SegmentIndex),
			StartSequence:       int32(item.StartSequence),
			EndSequence:         int32(item.EndSequence),
			MessageCount:        int32(item.MessageCount),
			OverlapFromSequence: int32(item.OverlapFromSequence),
			OverlapMessageCount: int32(item.OverlapMessageCount),
			Content:             item.Content,
			ContentHash:         item.ContentHash,
			Metadata:            toProtoStruct(item.Metadata),
			CreatedAt:           toProtoTimestamp(item.CreatedAt),
			UpdatedAt:           toProtoTimestamp(item.UpdatedAt),
		})
	}
	return out
}

func toProtoConversationRecord(item domain.ConversationRecord) *pb.ConversationRecord {
	return &pb.ConversationRecord{
		ConversationId: item.ConversationID,
		UserId:         item.UserID,
		RequestFormat:  item.RequestFormat,
		Provider:       string(item.Provider),
		Mode:           string(item.Mode),
		FinalizeStatus: string(item.FinalizeStatus),
		SummaryStatus:  string(item.SummaryStatus),
		AnalysisStatus: string(item.AnalysisStatus),
		Messages:       toProtoUnifiedMessages(item.Messages),
		Segments:       toProtoConversationSegments(item.Segments),
		Response:       toProtoUnifiedResponse(item.Response),
		Usage:          toProtoUsage(item.Usage),
		Metadata:       toProtoStruct(item.Metadata),
		CreatedAt:      toProtoTimestamp(item.CreatedAt),
		UpdatedAt:      toProtoTimestamp(item.UpdatedAt),
	}
}
