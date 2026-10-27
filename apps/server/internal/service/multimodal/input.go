package multimodal

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/modelcap"
	"memstation/apps/server/internal/provider"
	"memstation/apps/server/internal/security"
)

type InputPreparer struct {
	Policy modelcap.InputPolicy
	Media  *Service
}

func (p *InputPreparer) PolicyID() string {
	raw, _ := json.Marshal(p.Policy)
	digest := sha256.Sum256(raw)
	return "native_media_v1:" + hex.EncodeToString(digest[:])
}

// Prepare filters a copy of the outbound request. The persisted conversation
// retains original media for a differently capable summary or analysis model.
func (p *InputPreparer) Prepare(ctx context.Context, in domain.UnifiedRequest) (domain.UnifiedRequest, error) {
	in, err := p.prepareToolInputs(ctx, in)
	if err != nil {
		return in, err
	}
	return p.prepareBlocks(ctx, in)
}

func (p *InputPreparer) prepareBlocks(ctx context.Context, in domain.UnifiedRequest) (domain.UnifiedRequest, error) {
	in.Messages = append([]domain.UnifiedMessage(nil), in.Messages...)
	var total int64
	const maxInlineBytes = 32 << 20
	for i := range in.Messages {
		message := &in.Messages[i]
		blocks := append([]domain.ContentBlock(nil), message.Blocks...)
		for j, b := range blocks {
			if !IsMedia(b.Type) {
				continue
			}
			if message.Role == domain.RoleSystem || !p.Policy.Supports(in.Provider, in.Model, b.Type) || !wireSupports(in.Provider, b) {
				blocks[j] = omitted(b.Type, "unsupported by model or protocol")
				continue
			}
			if b.BlobID != "" {
				if err := security.RequireCapability(ctx, domain.CapabilityFilesRead); err != nil {
					return in, err
				}
				if p.Media == nil {
					return in, errors.New("media storage unavailable")
				}
				blob, reader, err := p.Media.OpenBlob(ctx, in.UserID, b.BlobID)
				if err != nil {
					return in, err
				}
				data, readErr := io.ReadAll(io.LimitReader(reader, maxInlineBytes-total+1))
				_ = reader.Close()
				if readErr != nil {
					return in, readErr
				}
				total += int64(len(data))
				if total > maxInlineBytes {
					return in, errors.New("native media exceeds 32 MiB input budget")
				}
				b.MIMEType = firstNonEmpty(b.MIMEType, blob.MIMEType)
				b.Filename = firstNonEmpty(b.Filename, blob.OriginalFilename)
				b.SourceURL = "data:" + b.MIMEType + ";base64," + base64.StdEncoding.EncodeToString(data)
				b.ProviderFileID, b.BlobID = "", ""
			} else if strings.HasPrefix(b.SourceURL, "data:") {
				mimeType, data, ok := provider.InlineData(b.SourceURL)
				if !ok {
					return in, errors.New("invalid inline media")
				}
				total += int64(base64.StdEncoding.DecodedLen(len(data)))
				if total > maxInlineBytes {
					return in, errors.New("native media exceeds 32 MiB input budget")
				}
				b.MIMEType = mimeType
			}
			if b.SourceURL == "" && b.ProviderFileID == "" {
				blocks[j] = omitted(b.Type, "source unavailable")
				continue
			}
			if !wireSupports(in.Provider, b) {
				blocks[j] = omitted(b.Type, "unsupported source format")
				continue
			}
			if b.Type == domain.ContentBlockFile && b.Filename == "" {
				b.Filename = "attachment"
				if b.MIMEType == "application/pdf" {
					b.Filename += ".pdf"
				}
			}
			// Raw protocol payloads and metadata are never a second outbound source.
			b.Raw, b.Metadata, b.Text = "", nil, ""
			blocks[j] = b
		}
		message.Blocks = blocks
	}
	return in, nil
}

func IsMedia(kind domain.ContentBlockType) bool {
	return kind == domain.ContentBlockImage || kind == domain.ContentBlockAudio || kind == domain.ContentBlockVideo || kind == domain.ContentBlockFile
}

func omitted(kind domain.ContentBlockType, reason string) domain.ContentBlock {
	return domain.ContentBlock{Type: domain.ContentBlockText, Text: fmt.Sprintf("[%s omitted: %s]", kind, reason)}
}

// Model capability and the selected transport must both support the source.
func wireSupports(p domain.Provider, b domain.ContentBlock) bool {
	switch p {
	case domain.ProviderAnthropic:
		return b.Type == domain.ContentBlockImage || (b.Type == domain.ContentBlockFile && (b.MIMEType == "" || b.MIMEType == "application/pdf"))
	case domain.ProviderGemini:
		return b.ProviderFileID == "" || b.SourceURL != "" || b.BlobID != ""
	case domain.ProviderOpenAICompatible:
		if b.Type == domain.ContentBlockImage {
			return b.BlobID != "" || b.SourceURL != ""
		}
		if b.Type == domain.ContentBlockVideo {
			return false
		}
		if b.Type == domain.ContentBlockAudio && b.BlobID == "" {
			mimeType, _, ok := provider.InlineData(b.SourceURL)
			return ok && (mimeType == "audio/wav" || mimeType == "audio/mpeg" || mimeType == "audio/mp3")
		}
		if b.Type == domain.ContentBlockFile {
			return b.BlobID != "" || b.ProviderFileID != "" || strings.HasPrefix(b.SourceURL, "data:")
		}
		return true
	case domain.ProviderOpenAIResponses:
		return b.Type == domain.ContentBlockImage || b.Type == domain.ContentBlockFile
	default:
		return false
	}
}
