package protocol

import (
	"encoding/json"
	"testing"

	"memstation/apps/server/internal/domain"
)

func TestNativeMediaSourceNormalization(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		kind domain.ContentBlockType
		url  string
		mime string
	}{
		{`{"type":"input_audio","input_audio":{"data":"aGk=","format":"wav"}}`, domain.ContentBlockAudio, "data:audio/wav;base64,aGk=", "audio/wav"},
		{`{"type":"input_audio","input_audio":{"data":"aGk=","format":"mp3"}}`, domain.ContentBlockAudio, "data:audio/mpeg;base64,aGk=", "audio/mpeg"},
		{`{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aGk="}}`, domain.ContentBlockImage, "data:image/png;base64,aGk=", "image/png"},
		{`{"type":"document","source":{"type":"url","url":"https://example.com/a.pdf"}}`, domain.ContentBlockFile, "https://example.com/a.pdf", ""},
		{`{"type":"file","file":{"file_data":"data:application/pdf;base64,aGk=","filename":"a.pdf"}}`, domain.ContentBlockFile, "data:application/pdf;base64,aGk=", ""},
	} {
		var item map[string]any
		if err := json.Unmarshal([]byte(tc.raw), &item); err != nil {
			t.Fatal(err)
		}
		b := blockFromMap(item)
		if b.Type != tc.kind || b.SourceURL != tc.url || b.MIMEType != tc.mime {
			t.Fatal(tc, b)
		}
	}
}
