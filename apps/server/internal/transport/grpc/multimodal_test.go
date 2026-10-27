package grpctransport

import (
	"reflect"
	"testing"

	"memstation/apps/server/internal/domain"
	pb "memstation/apps/server/proto/memstation/v1"
)

func TestMultimodalBlocksRoundTripAndEventMedia(t *testing.T) {
	blocks := []domain.ContentBlock{{Type: domain.ContentBlockAudio, MIMEType: "audio/wav", BlobID: "blob", SourceURL: "data:audio/wav;base64,aGk=", ProviderFileID: "file", Filename: "a.wav", SizeBytes: 2, SHA256: "digest", PageIndex: 2, StartMS: 4, EndMS: 9, DurationMS: 5}}
	if got := fromProtoContentBlocks(toProtoContentBlocks(blocks)); !reflect.DeepEqual(got, blocks) {
		t.Fatal(got, blocks)
	}
	in := &pb.IngestBatch{Events: []*pb.SourceEvent{{Parts: []*pb.SourcePart{{Type: "media", Media: toProtoContentBlocks(blocks)[0]}}}}}
	var batch domain.ObserveBatch
	if err := decodeContract(in, &batch); err != nil {
		t.Fatal(err)
	}
	if len(batch.Events) != 1 || batch.Events[0].Parts[0].Media == nil || !reflect.DeepEqual(*batch.Events[0].Parts[0].Media, blocks[0]) {
		t.Fatal(batch)
	}
}
