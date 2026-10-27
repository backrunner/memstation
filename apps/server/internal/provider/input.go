package provider

import (
	"context"
	"encoding/base64"
	"strings"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
)

type InputPreparer interface {
	Prepare(context.Context, domain.UnifiedRequest) (domain.UnifiedRequest, error)
}

type inputAdapter struct {
	ports.ProviderAdapter
	preparer InputPreparer
}

func WithInputPreparation(adapter ports.ProviderAdapter, preparer InputPreparer) ports.ProviderAdapter {
	return &inputAdapter{ProviderAdapter: adapter, preparer: preparer}
}

func (a *inputAdapter) Complete(ctx context.Context, in domain.UnifiedRequest) (*domain.UnifiedResponse, error) {
	in.Provider = a.Name()
	prepared, err := a.preparer.Prepare(ctx, in)
	if err != nil {
		return nil, err
	}
	return a.ProviderAdapter.Complete(ctx, prepared)
}

func (a *inputAdapter) Stream(ctx context.Context, in domain.UnifiedRequest, emit func(domain.StreamEvent) error) (*domain.UnifiedResponse, error) {
	in.Provider = a.Name()
	prepared, err := a.preparer.Prepare(ctx, in)
	if err != nil {
		return nil, err
	}
	return a.ProviderAdapter.Stream(ctx, prepared, emit)
}

// InlineData recognizes native data URLs without fetching remote resources.
func InlineData(value string) (mimeType, data string, ok bool) {
	if !strings.HasPrefix(value, "data:") {
		return "", "", false
	}
	header, data, found := strings.Cut(strings.TrimPrefix(value, "data:"), ",")
	if !found || !strings.HasSuffix(header, ";base64") || data == "" {
		return "", "", false
	}
	if _, err := base64.StdEncoding.DecodeString(data); err != nil {
		return "", "", false
	}
	return strings.TrimSuffix(header, ";base64"), data, true
}

// NativeSourceFields normalizes native Chat/Responses/Anthropic media sources.
// The boolean distinguishes an absent native representation from a present one.
func NativeSourceFields(item map[string]any, kind string) (sourceURL, fileID, mimeType string, ok bool) {
	str := func(m map[string]any, key string) string { v, _ := m[key].(string); return v }
	if source, yes := item["source"].(map[string]any); yes {
		mimeType = str(source, "media_type")
		switch str(source, "type") {
		case "base64":
			return "data:" + mimeType + ";base64," + str(source, "data"), "", mimeType, true
		case "url":
			return str(source, "url"), "", mimeType, true
		case "file":
			return "", str(source, "file_id"), mimeType, true
		}
	}
	if kind == "audio" {
		if audio, yes := item["input_audio"].(map[string]any); yes {
			format := str(audio, "format")
			mimeType = "audio/" + format
			if format == "mp3" {
				mimeType = "audio/mpeg"
			}
			return "data:" + mimeType + ";base64," + str(audio, "data"), "", mimeType, true
		}
	}
	if kind == "file" {
		file := item
		if nested, yes := item["file"].(map[string]any); yes {
			file = nested
		}
		if data := str(file, "file_data"); data != "" {
			return data, "", str(file, "mime_type"), true
		}
		if u := str(file, "file_url"); u != "" {
			return u, "", str(file, "mime_type"), true
		}
	}
	return "", "", "", false
}

func NativeSourceFilename(item map[string]any) string {
	if filename, _ := item["filename"].(string); filename != "" {
		return filename
	}
	if file, ok := item["file"].(map[string]any); ok {
		filename, _ := file["filename"].(string)
		return filename
	}
	return ""
}
