# Native media input

MemStation keeps original typed content blocks in the conversation. Before an upstream call, it prepares a copy for the selected model. Supported attachments are sent natively; unsupported or unavailable attachments become positional text markers such as `[audio omitted: unsupported by model or protocol]`. The original conversation remains available to summary and analysis models with different capabilities. This policy covers streaming and non-streaming gateway calls, memory processing, and the memory broker.

## Model capabilities

Declare input modalities using exact `provider:model` keys. Unknown models receive text only. Capability is never inferred from a model name or from another model using the same provider.

```sh
MEMSTATION_MODEL_INPUT_MODALITIES='{"openai_compatible:your-vision-model":["text","image"],"gemini:your-multimodal-model":["text","image","audio","video","file"]}'
```

This deployment setting requires restarting affected server/worker processes. For a broker, configure the exact role aliases returned by the broker, using the `openai_compatible` key. Changing this policy changes memory generation checkpoint identity. Model listings expose configured `input_modalities`.

Check provider model metadata before enabling a modality. Ollama `/api/show` reports capabilities such as `vision`; this does not imply audio or video support. Local configuration must remain ignored by Git.

Both model capability and transport must support a source:

| Transport | Native input implemented |
| --- | --- |
| OpenAI Chat | Images via URLs/data URLs; WAV/MP3 audio via inline data; files via provider file IDs or inline data |
| OpenAI Responses | Images via URLs/data URLs/file IDs; files via URLs/data URLs/file IDs |
| Anthropic Messages | Images and PDF documents via URL, base64 source, or provider file ID |
| Gemini | Image/audio/video/file parts via inline data or provider-supported file URI |

Formats follow [OpenAI Chat](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create), [Claude vision](https://platform.claude.com/docs/en/build-with-claude/vision), and [Gemini audio input](https://ai.google.dev/gemini-api/docs/audio). The declaration must match the actual endpoint and file formats supported by the selected model. Generic Chat-compatible endpoints are not assumed to accept proprietary video formats or remote audio URLs.

Uploaded blobs use existing user/workspace/partition checks and the `files.read` capability. Native inline payloads have a 32 MiB aggregate request limit. MemStation never fetches arbitrary external URLs to transcode or caption unsupported media. Unsupported attachments trigger no extra model calls. Supported but inaccessible/deleted uploaded blobs fail authorization/storage checks rather than being silently read across scope boundaries.

## Chatbot event ingestion

Gateway protocols accept native media content blocks. Independent chatbot integrations can include a media part in an observed source event:

```json
{
  "type": "media",
  "media": {
    "type": "image",
    "mime_type": "image/png",
    "source_url": "https://example.com/attachment.png"
  }
}
```

Use `blob_id` for scoped uploads. Event batch limits still apply, so upload large assets first. REST and gRPC preserve media fields and original source revision. A media part does not establish the speaker's identity or ownership of what appears in an image.

## Memory evidence

Summary and Fact models receive native blocks with request-local source labels, separately from textual JSON. Storage coordinates and original identifiers remain outside model input; returned labels are restored before source-grounding validation. Payloads and raw protocol JSON are never treated as captions, transcripts, or user quotes. Existing explicitly supplied text artifacts remain labelled sources; no caption/transcription model is called automatically as a fallback.

The extractor may return `media_observations` for attachments it actually received. These retain source identity and an attachment-description digest, and are stored as model observations with `media_observation` evidence, never exact quotations. Unsupported markers cannot support facts. Assistant attachments cannot become user facts. Media-only event observations belong to the workspace; actor attribution requires separate source evidence. Source and media-policy changes invalidate relevant checkpoints.

## Verification

Default tests use local HTTP fixtures and spend no model credits. Opt-in `TestLiveNativeImageMemory` sends a generated solid-color image through the production memory client and Fact service, then checks the image-only color against the fixture. It uses no judge or benchmark calls and records actual generation usage. Run only with explicit `MEMSTATION_NATIVE_MEDIA_SMOKE=true`, endpoint/model configuration, and an ignored artifact path.
