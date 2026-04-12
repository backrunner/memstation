package multimodal

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"memstation/apps/server/internal/bootstrap"
	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
)

type Service struct {
	repo  ports.MediaRepository
	mu    sync.RWMutex
	blobs ports.BlobStore
	cfg   bootstrap.MultimodalConfig
}

func NewService(repo ports.MediaRepository, blobs ports.BlobStore, cfg bootstrap.MultimodalConfig) *Service {
	return &Service{repo: repo, blobs: blobs, cfg: cfg}
}

func (s *Service) Enabled() bool {
	if s == nil {
		return false
	}
	return s.runtimeConfig().Enabled
}

func (s *Service) UpdateRuntime(cfg bootstrap.MultimodalConfig, blobs ports.BlobStore) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.cfg = cfg
	s.blobs = blobs
	s.mu.Unlock()
}

func (s *Service) Snapshot() bootstrap.MultimodalConfig {
	if s == nil {
		return bootstrap.MultimodalConfig{}
	}
	return s.runtimeConfig()
}

func (s *Service) SaveUpload(ctx context.Context, userID, filename, mimeType string, body io.Reader, metadata map[string]any) (domain.BlobObject, error) {
	cfg, blobs := s.runtime()
	if s == nil || s.repo == nil || blobs == nil {
		return domain.BlobObject{}, fmt.Errorf("multimodal storage is not configured")
	}
	filename = strings.TrimSpace(filename)
	if filename == "" {
		filename = "upload.bin"
	}
	mimeType = normalizeMIMEType(mimeType, filename)
	if !mimeAllowed(cfg, mimeType) {
		return domain.BlobObject{}, fmt.Errorf("mime type %q is not allowed", mimeType)
	}

	limited := body
	if cfg.MaxAssetBytes > 0 {
		limited = io.LimitReader(body, cfg.MaxAssetBytes+1)
	}
	data, err := io.ReadAll(limited)
	if err != nil {
		return domain.BlobObject{}, err
	}
	if cfg.MaxAssetBytes > 0 && int64(len(data)) > cfg.MaxAssetBytes {
		return domain.BlobObject{}, fmt.Errorf("asset exceeds max size of %d bytes", cfg.MaxAssetBytes)
	}

	sum := sha256.Sum256(data)
	now := time.Now().UTC()
	blob := domain.BlobObject{
		BlobID:           "blob:" + uuid.NewString(),
		UserID:           userID,
		SourceKind:       domain.BlobSourceKindUpload,
		StorageBackend:   firstNonEmpty(strings.TrimSpace(cfg.StorageBackend), "local"),
		MIMEType:         mimeType,
		SHA256:           hex.EncodeToString(sum[:]),
		SizeBytes:        int64(len(data)),
		OriginalFilename: filename,
		Metadata:         cloneMap(metadata),
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	stored, err := blobs.Put(ctx, blob, bytes.NewReader(data))
	if err != nil {
		return domain.BlobObject{}, err
	}
	if err := s.repo.PutBlob(ctx, stored); err != nil {
		_ = blobs.Delete(ctx, stored)
		return domain.BlobObject{}, err
	}
	return stored, nil
}

func (s *Service) GetBlob(ctx context.Context, userID, blobID string) (domain.BlobObject, error) {
	if s == nil || s.repo == nil {
		return domain.BlobObject{}, fmt.Errorf("multimodal storage is not configured")
	}
	return s.repo.GetBlob(ctx, userID, blobID)
}

func (s *Service) OpenBlob(ctx context.Context, userID, blobID string) (domain.BlobObject, io.ReadCloser, error) {
	_, blobs := s.runtime()
	blob, err := s.GetBlob(ctx, userID, blobID)
	if err != nil {
		return domain.BlobObject{}, nil, err
	}
	if blobs == nil {
		return domain.BlobObject{}, nil, fmt.Errorf("blob store is not configured")
	}
	reader, err := blobs.Open(ctx, blob)
	if err != nil {
		return domain.BlobObject{}, nil, err
	}
	return blob, reader, nil
}

func (s *Service) DeleteBlob(ctx context.Context, userID, blobID string) error {
	_, blobs := s.runtime()
	if s == nil || s.repo == nil {
		return nil
	}
	blob, err := s.repo.GetBlob(ctx, userID, blobID)
	if err != nil {
		return err
	}
	if blobs != nil {
		if err := blobs.Delete(ctx, blob); err != nil {
			return err
		}
	}
	return s.repo.DeleteBlob(ctx, userID, blobID, time.Now().UTC())
}

func (s *Service) CleanupConversationBlobs(ctx context.Context, userID, conversationID string) error {
	if s == nil || s.repo == nil {
		return nil
	}
	parts, err := s.repo.ListMessageParts(ctx, userID, conversationID)
	if err != nil {
		return err
	}
	seen := map[string]struct{}{}
	for _, part := range parts {
		blobID := strings.TrimSpace(part.BlobID)
		if blobID == "" {
			continue
		}
		if _, ok := seen[blobID]; ok {
			continue
		}
		seen[blobID] = struct{}{}
		if err := s.DeleteBlob(ctx, userID, blobID); err != nil && !strings.Contains(strings.ToLower(err.Error()), "not found") {
			return err
		}
	}
	return nil
}

func (s *Service) MaterializeConversation(ctx context.Context, conversation domain.ConversationRecord) ([]domain.MessagePart, error) {
	if s == nil || s.repo == nil {
		return nil, nil
	}
	parts := MaterializeMessageParts(conversation)
	if err := s.repo.ReplaceMessageParts(ctx, conversation.UserID, conversation.ConversationID, parts); err != nil {
		return nil, err
	}
	return parts, nil
}

func (s *Service) ExtractConversation(ctx context.Context, conversation domain.ConversationRecord, parts []domain.MessagePart) ([]domain.MessagePartArtifact, error) {
	if s == nil || s.repo == nil {
		return nil, nil
	}
	artifacts := make([]domain.MessagePartArtifact, 0)
	for _, part := range parts {
		generated, err := s.extractPartArtifacts(ctx, conversation.UserID, conversation.ConversationID, part)
		if err != nil {
			return nil, err
		}
		artifacts = append(artifacts, generated...)
	}
	sort.Slice(artifacts, func(i, j int) bool {
		if artifacts[i].PartID == artifacts[j].PartID {
			if artifacts[i].StartMS == artifacts[j].StartMS {
				return artifacts[i].ArtifactType < artifacts[j].ArtifactType
			}
			return artifacts[i].StartMS < artifacts[j].StartMS
		}
		return artifacts[i].PartID < artifacts[j].PartID
	})
	if err := s.repo.ReplacePartArtifacts(ctx, conversation.UserID, conversation.ConversationID, artifacts); err != nil {
		return nil, err
	}
	return artifacts, nil
}

func (s *Service) ListMessageParts(ctx context.Context, userID, conversationID string) ([]domain.MessagePart, error) {
	if s == nil || s.repo == nil {
		return nil, nil
	}
	return s.repo.ListMessageParts(ctx, userID, conversationID)
}

func (s *Service) ListPartArtifacts(ctx context.Context, userID, conversationID string) ([]domain.MessagePartArtifact, error) {
	if s == nil || s.repo == nil {
		return nil, nil
	}
	return s.repo.ListPartArtifacts(ctx, userID, conversationID)
}

func MaterializeMessageParts(conversation domain.ConversationRecord) []domain.MessagePart {
	if len(conversation.Messages) == 0 {
		return nil
	}
	now := conversation.UpdatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	parts := make([]domain.MessagePart, 0, len(conversation.Messages))
	for sequenceNum, message := range conversation.Messages {
		partIndex := 0
		blocks := canonicalBlocks(message)
		for _, block := range blocks {
			metadata := cloneMap(block.Metadata)
			if block.Filename != "" {
				metadata["filename"] = block.Filename
			}
			part := domain.MessagePart{
				PartID:         fmt.Sprintf("part:%s:%03d:%03d", conversation.ConversationID, sequenceNum, partIndex),
				UserID:         conversation.UserID,
				ConversationID: conversation.ConversationID,
				SequenceNum:    sequenceNum,
				PartIndex:      partIndex,
				Role:           message.Role,
				PartType:       block.Type,
				MIMEType:       block.MIMEType,
				BlobID:         block.BlobID,
				SourceURL:      block.SourceURL,
				ProviderFileID: block.ProviderFileID,
				Filename:       block.Filename,
				TextContent:    block.TextProxy(),
				RawPayload:     strings.TrimSpace(block.Raw),
				PageIndex:      block.PageIndex,
				StartMS:        block.StartMS,
				EndMS:          block.EndMS,
				DurationMS:     block.DurationMS,
				Metadata:       metadata,
				CreatedAt:      now,
				UpdatedAt:      now,
			}
			parts = append(parts, part)
			partIndex++
		}
	}
	return parts
}

func canonicalBlocks(message domain.UnifiedMessage) []domain.ContentBlock {
	blocks := make([]domain.ContentBlock, 0, len(message.Blocks)+len(message.ToolCalls)+len(message.ToolResults)+1)
	if len(message.Blocks) == 0 && strings.TrimSpace(message.Text) != "" {
		blocks = append(blocks, domain.ContentBlock{
			Type: domain.ContentBlockText,
			Text: strings.TrimSpace(message.Text),
		})
	} else {
		for _, block := range message.Blocks {
			blocks = append(blocks, block)
		}
	}
	for _, call := range message.ToolCalls {
		raw, _ := json.Marshal(call.Arguments)
		blocks = append(blocks, domain.ContentBlock{
			Type: domain.ContentBlockToolUse,
			Text: strings.TrimSpace(call.Name),
			Raw:  string(raw),
			Metadata: map[string]any{
				"tool_call_id": call.ID,
				"name":         call.Name,
			},
		})
		blocks = append(blocks, extractToolPayloadBlocks(call.Arguments, toolPayloadContext{
			ToolCallID:  call.ID,
			ToolName:    call.Name,
			PayloadKind: "tool_input",
		})...)
	}
	for _, result := range message.ToolResults {
		raw, _ := json.Marshal(result.Output)
		blocks = append(blocks, domain.ContentBlock{
			Type: domain.ContentBlockToolResult,
			Text: toolResultText(result),
			Raw:  string(raw),
			Metadata: map[string]any{
				"tool_call_id": result.ToolCallID,
				"name":         result.Name,
			},
		})
		blocks = append(blocks, extractToolPayloadBlocks(result.Output, toolPayloadContext{
			ToolCallID:  result.ToolCallID,
			ToolName:    result.Name,
			PayloadKind: "tool_output",
		})...)
	}
	return blocks
}

type toolPayloadContext struct {
	ToolCallID  string
	ToolName    string
	PayloadKind string
}

func extractToolPayloadBlocks(payload any, ctx toolPayloadContext) []domain.ContentBlock {
	if payload == nil {
		return nil
	}
	blocks := make([]domain.ContentBlock, 0)
	seen := map[string]struct{}{}
	var walk func(value any, path, hint string)
	walk = func(value any, path, hint string) {
		switch typed := value.(type) {
		case map[string]any:
			if block, ok := payloadMediaBlock(typed, hint, path, ctx); ok {
				key := string(block.Type) + "::" + strings.TrimSpace(block.BlobID) + "::" + strings.TrimSpace(block.SourceURL) + "::" + strings.TrimSpace(block.ProviderFileID) + "::" + strings.TrimSpace(path)
				if _, exists := seen[key]; !exists {
					seen[key] = struct{}{}
					blocks = append(blocks, block)
				}
				return
			}
			for key, nested := range typed {
				walk(nested, joinPayloadPath(path, key), key)
			}
		case []any:
			for idx, nested := range typed {
				walk(nested, fmt.Sprintf("%s[%d]", path, idx), hint)
			}
		case string:
			trimmed := strings.TrimSpace(typed)
			if trimmed == "" || (!strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[")) {
				return
			}
			var nested any
			if json.Unmarshal([]byte(trimmed), &nested) == nil {
				walk(nested, path, hint)
			}
		}
	}
	walk(payload, ctx.PayloadKind, ctx.PayloadKind)
	return blocks
}

func payloadMediaBlock(item map[string]any, hint, path string, ctx toolPayloadContext) (domain.ContentBlock, bool) {
	blockType := inferPayloadBlockType(item, hint)
	if blockType == "" {
		return domain.ContentBlock{}, false
	}

	sourceMap := map[string]any{}
	switch blockType {
	case domain.ContentBlockImage:
		sourceMap = nestedObject(item["image_url"], item["image"])
	case domain.ContentBlockAudio:
		sourceMap = nestedObject(item["audio"])
	case domain.ContentBlockVideo:
		sourceMap = nestedObject(item["video"])
	case domain.ContentBlockFile:
		sourceMap = nestedObject(item["file"], item["document"])
	}

	sourceURL := firstNonEmpty(
		stringValue(sourceMap["url"]),
		stringValue(sourceMap["source_url"]),
		stringValue(item["source_url"]),
		stringValue(item["url"]),
		stringValue(item["image_url"]),
	)
	providerFileID := firstNonEmpty(
		stringValue(sourceMap["provider_file_id"]),
		stringValue(sourceMap["file_id"]),
		stringValue(item["provider_file_id"]),
		stringValue(item["file_id"]),
	)
	blobID := strings.TrimSpace(firstNonEmpty(stringValue(item["blob_id"]), stringValue(sourceMap["blob_id"])))
	mimeType := firstNonEmpty(
		stringValue(sourceMap["mime_type"]),
		stringValue(sourceMap["media_type"]),
		stringValue(item["mime_type"]),
		stringValue(item["media_type"]),
	)
	filename := firstNonEmpty(stringValue(item["filename"]), stringValue(sourceMap["filename"]))
	mimeType = normalizeMIMEType(mimeType, filename)

	if strings.TrimSpace(sourceURL) == "" && strings.TrimSpace(providerFileID) == "" && strings.TrimSpace(blobID) == "" {
		return domain.ContentBlock{}, false
	}

	metadata := payloadMetadata(item, sourceMap)
	metadata["tool_call_id"] = ctx.ToolCallID
	metadata["tool_name"] = ctx.ToolName
	metadata["tool_payload_kind"] = ctx.PayloadKind
	metadata["tool_payload_path"] = path

	return domain.ContentBlock{
		Type:           blockType,
		MIMEType:       mimeType,
		BlobID:         blobID,
		SourceURL:      sourceURL,
		ProviderFileID: providerFileID,
		Filename:       filename,
		SizeBytes:      int64Value(item["size_bytes"]),
		SHA256:         strings.TrimSpace(stringValue(item["sha256"])),
		PageIndex:      intValue(item["page_index"]),
		StartMS:        int64Value(item["start_ms"]),
		EndMS:          int64Value(item["end_ms"]),
		DurationMS:     int64Value(item["duration_ms"]),
		Text: firstNonEmpty(
			stringValue(item["text"]),
			stringValue(item["caption"]),
			stringValue(item["ocr_text"]),
			stringValue(item["transcript"]),
			stringValue(item["summary"]),
			stringValue(item["keyframe_summary"]),
			stringValue(item["document_text"]),
			stringValue(item["parsed_text"]),
			stringValue(metadata["text"]),
			stringValue(metadata["caption"]),
			stringValue(metadata["ocr_text"]),
			stringValue(metadata["transcript"]),
			stringValue(metadata["summary"]),
			stringValue(metadata["keyframe_summary"]),
			stringValue(metadata["document_text"]),
			stringValue(metadata["parsed_text"]),
		),
		Raw:      rawJSONString(item),
		Metadata: metadata,
	}, true
}

func inferPayloadBlockType(item map[string]any, hint string) domain.ContentBlockType {
	switch strings.TrimSpace(strings.ToLower(stringValue(item["type"]))) {
	case "image", "image_url", "input_image":
		return domain.ContentBlockImage
	case "audio", "input_audio":
		return domain.ContentBlockAudio
	case "video", "input_video":
		return domain.ContentBlockVideo
	case "file", "input_file", "document":
		return domain.ContentBlockFile
	}

	switch strings.TrimSpace(strings.ToLower(hint)) {
	case "image", "images", "image_url":
		return domain.ContentBlockImage
	case "audio", "audios":
		return domain.ContentBlockAudio
	case "video", "videos":
		return domain.ContentBlockVideo
	case "file", "files", "document", "documents", "attachment", "attachments":
		return domain.ContentBlockFile
	}

	mimeType := normalizeMIMEType(firstNonEmpty(stringValue(item["mime_type"]), stringValue(item["media_type"])), firstNonEmpty(stringValue(item["filename"]), stringValue(item["name"])))
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		return domain.ContentBlockImage
	case strings.HasPrefix(mimeType, "audio/"):
		return domain.ContentBlockAudio
	case strings.HasPrefix(mimeType, "video/"):
		return domain.ContentBlockVideo
	case strings.TrimSpace(stringValue(item["blob_id"])) != "",
		strings.TrimSpace(stringValue(item["provider_file_id"])) != "",
		strings.TrimSpace(stringValue(item["file_id"])) != "",
		strings.TrimSpace(stringValue(item["source_url"])) != "",
		strings.TrimSpace(stringValue(item["url"])) != "",
		strings.TrimSpace(stringValue(item["image_url"])) != "",
		mimeType == "application/pdf":
		return domain.ContentBlockFile
	default:
		return ""
	}
}

func nestedObject(values ...any) map[string]any {
	for _, value := range values {
		if typed, ok := value.(map[string]any); ok {
			return typed
		}
	}
	return nil
}

func payloadMetadata(item map[string]any, nested map[string]any) map[string]any {
	metadata := cloneMap(item)
	for _, key := range []string{
		"type", "image_url", "image", "audio", "video", "file", "document",
		"source_url", "url", "provider_file_id", "file_id", "blob_id",
		"mime_type", "media_type", "filename", "size_bytes", "sha256",
		"page_index", "start_ms", "end_ms", "duration_ms",
		"text", "caption", "ocr_text", "transcript", "summary", "keyframe_summary",
		"document_text", "parsed_text",
	} {
		delete(metadata, key)
	}
	for key, value := range nested {
		if _, ok := metadata[key]; !ok {
			metadata[key] = value
		}
	}
	if len(metadata) == 0 {
		return map[string]any{}
	}
	return metadata
}

func joinPayloadPath(base, key string) string {
	if strings.TrimSpace(base) == "" {
		return key
	}
	return base + "." + key
}

func (s *Service) extractPartArtifacts(ctx context.Context, userID, conversationID string, part domain.MessagePart) ([]domain.MessagePartArtifact, error) {
	if part.PartType == domain.ContentBlockText || part.PartType == domain.ContentBlockToolUse || part.PartType == domain.ContentBlockToolResult || part.PartType == domain.ContentBlockJSON {
		return nil, nil
	}
	textSource, textMetadata, err := s.partTextSource(ctx, userID, part)
	if err != nil {
		return nil, err
	}
	textArtifactMetadata := stringMapToAny(textMetadata)
	now := time.Now().UTC()
	makeArtifact := func(kind, text string, metadata map[string]any) domain.MessagePartArtifact {
		return domain.MessagePartArtifact{
			ArtifactID:     fmt.Sprintf("artifact:%s:%s", part.PartID, kind),
			UserID:         userID,
			ConversationID: conversationID,
			PartID:         part.PartID,
			ArtifactType:   kind,
			Status:         "completed",
			ExtractorKind:  extractorKindForPart(part.PartType),
			ModelName:      s.modelNameForPart(part.PartType),
			TextContent:    strings.TrimSpace(text),
			PageIndex:      part.PageIndex,
			StartMS:        part.StartMS,
			EndMS:          part.EndMS,
			Confidence:     0.72,
			Metadata:       mergeMaps(cloneMap(part.Metadata), metadata),
			CreatedAt:      now,
			UpdatedAt:      now,
		}
	}

	artifacts := make([]domain.MessagePartArtifact, 0, 2)
	switch part.PartType {
	case domain.ContentBlockImage:
		if caption := firstNonEmpty(textMetadata["caption"], textSource); caption != "" {
			artifacts = append(artifacts, makeArtifact("caption", caption, textArtifactMetadata))
		}
		if ocr := firstNonEmpty(textMetadata["ocr_text"], textSource); ocr != "" {
			artifacts = append(artifacts, makeArtifact("ocr", ocr, textArtifactMetadata))
		}
	case domain.ContentBlockAudio:
		if transcript := firstNonEmpty(textMetadata["transcript"], textSource); transcript != "" {
			artifacts = append(artifacts, makeArtifact("transcript", transcript, textArtifactMetadata))
		}
		if summary := textMetadata["summary"]; summary != "" {
			artifacts = append(artifacts, makeArtifact("audio_summary", summary, textArtifactMetadata))
		}
	case domain.ContentBlockVideo:
		if transcript := firstNonEmpty(textMetadata["transcript"], textSource); transcript != "" {
			artifacts = append(artifacts, makeArtifact("transcript", transcript, textArtifactMetadata))
		}
		if summary := firstNonEmpty(textMetadata["keyframe_summary"], textMetadata["summary"], textSource); summary != "" {
			artifacts = append(artifacts, makeArtifact("keyframe_summary", summary, textArtifactMetadata))
		}
	case domain.ContentBlockFile:
		if documentText := firstNonEmpty(textMetadata["document_text"], textMetadata["parsed_text"], textSource); documentText != "" {
			artifacts = append(artifacts, makeArtifact("document_text", documentText, textArtifactMetadata))
		}
		if ocr := textMetadata["ocr_text"]; ocr != "" {
			artifacts = append(artifacts, makeArtifact("ocr", ocr, textArtifactMetadata))
		}
	}
	return dedupeArtifacts(artifacts), nil
}

func (s *Service) partTextSource(ctx context.Context, userID string, part domain.MessagePart) (string, map[string]string, error) {
	_, blobs := s.runtime()
	textMeta := map[string]string{}
	for _, key := range []string{"caption", "ocr_text", "transcript", "summary", "keyframe_summary", "document_text", "parsed_text"} {
		if value, ok := part.Metadata[key].(string); ok && strings.TrimSpace(value) != "" {
			textMeta[key] = strings.TrimSpace(value)
		}
	}
	if strings.TrimSpace(part.TextContent) != "" {
		return strings.TrimSpace(part.TextContent), textMeta, nil
	}
	if strings.TrimSpace(part.RawPayload) != "" {
		return strings.TrimSpace(part.RawPayload), textMeta, nil
	}
	if strings.TrimSpace(part.BlobID) == "" || blobs == nil || s.repo == nil {
		return "", textMeta, nil
	}
	blob, err := s.repo.GetBlob(ctx, userID, part.BlobID)
	if err != nil {
		return "", textMeta, nil
	}
	reader, err := blobs.Open(ctx, blob)
	if err != nil {
		return "", textMeta, nil
	}
	defer reader.Close()
	if !isTextLikeMIME(firstNonEmpty(part.MIMEType, blob.MIMEType)) {
		return "", textMeta, nil
	}
	data, err := io.ReadAll(io.LimitReader(reader, 1<<20))
	if err != nil {
		return "", textMeta, nil
	}
	text := strings.TrimSpace(string(data))
	if strings.HasSuffix(strings.ToLower(blob.OriginalFilename), ".json") {
		var payload any
		if json.Unmarshal(data, &payload) == nil {
			pretty, _ := json.MarshalIndent(payload, "", "  ")
			text = strings.TrimSpace(string(pretty))
		}
	}
	return text, textMeta, nil
}

func mimeAllowed(cfg bootstrap.MultimodalConfig, mimeType string) bool {
	if len(cfg.AllowedMIMETypes) == 0 {
		return true
	}
	for _, allowed := range cfg.AllowedMIMETypes {
		if strings.EqualFold(strings.TrimSpace(allowed), mimeType) {
			return true
		}
	}
	return false
}

func (s *Service) modelNameForPart(partType domain.ContentBlockType) string {
	cfg := s.runtimeConfig()
	switch partType {
	case domain.ContentBlockImage:
		return strings.TrimSpace(cfg.ImageModel)
	case domain.ContentBlockAudio:
		return strings.TrimSpace(cfg.AudioModel)
	case domain.ContentBlockVideo:
		return strings.TrimSpace(cfg.VideoModel)
	case domain.ContentBlockFile:
		return strings.TrimSpace(cfg.FileModel)
	default:
		return ""
	}
}

func (s *Service) runtime() (bootstrap.MultimodalConfig, ports.BlobStore) {
	if s == nil {
		return bootstrap.MultimodalConfig{}, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg, s.blobs
}

func (s *Service) runtimeConfig() bootstrap.MultimodalConfig {
	cfg, _ := s.runtime()
	return cfg
}

func extractorKindForPart(partType domain.ContentBlockType) string {
	switch partType {
	case domain.ContentBlockImage:
		return "vision"
	case domain.ContentBlockAudio:
		return "transcription"
	case domain.ContentBlockVideo:
		return "video_proxy"
	case domain.ContentBlockFile:
		return "document_parser"
	default:
		return "unknown"
	}
}

func dedupeArtifacts(items []domain.MessagePartArtifact) []domain.MessagePartArtifact {
	if len(items) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]domain.MessagePartArtifact, 0, len(items))
	for _, item := range items {
		key := item.ArtifactType + "::" + strings.TrimSpace(item.TextContent)
		if strings.TrimSpace(item.TextContent) == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func toolResultText(result domain.ToolResult) string {
	if output, ok := result.Output.(map[string]any); ok {
		if content, ok := output["content"]; ok {
			if text := toolPayloadText(content); text != "" {
				return text
			}
		}
	}
	if text := toolPayloadText(result.Output); text != "" {
		return text
	}
	raw, _ := json.Marshal(result.Output)
	return strings.TrimSpace(string(raw))
}

func toolPayloadText(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := toolPayloadText(item); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.TrimSpace(strings.Join(parts, " | "))
	case map[string]any:
		for _, key := range []string{"text", "caption", "ocr_text", "transcript", "summary", "keyframe_summary", "document_text", "parsed_text"} {
			if text := strings.TrimSpace(stringValue(typed[key])); text != "" {
				return text
			}
		}
		if content, ok := typed["content"]; ok {
			if text := toolPayloadText(content); text != "" {
				return text
			}
		}
		return ""
	default:
		return ""
	}
}

func normalizeMIMEType(value, filename string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value != "" {
		if parsed, _, err := mime.ParseMediaType(value); err == nil {
			return parsed
		}
		return value
	}
	if guessed := mime.TypeByExtension(strings.ToLower(filepath.Ext(filename))); guessed != "" {
		if parsed, _, err := mime.ParseMediaType(guessed); err == nil {
			return parsed
		}
		return guessed
	}
	return "application/octet-stream"
}

func isTextLikeMIME(mimeType string) bool {
	mimeType = strings.TrimSpace(strings.ToLower(mimeType))
	return strings.HasPrefix(mimeType, "text/") || mimeType == "application/json" || mimeType == "application/xml" || mimeType == "application/pdf"
}

func mergeMaps(base map[string]any, extras map[string]any) map[string]any {
	if len(base) == 0 && len(extras) == 0 {
		return nil
	}
	out := cloneMap(base)
	for key, value := range extras {
		out[key] = value
	}
	return out
}

func stringMapToAny(input map[string]string) map[string]any {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func cloneMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	default:
		return fmt.Sprintf("%v", typed)
	}
}

func intValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func int64Value(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int32:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	default:
		return 0
	}
}

func rawJSONString(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(raw)
}
