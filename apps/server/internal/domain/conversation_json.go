package domain

import "encoding/json"

func (c ConversationRecord) MarshalJSON() ([]byte, error) {
	type publicConversation ConversationRecord
	out := publicConversation(c)
	out.Metadata = PublicMemoryMetadata(c.Metadata)
	return json.Marshal(out)
}
