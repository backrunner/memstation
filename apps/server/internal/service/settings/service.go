package settings

import (
	"context"
	sqlpkg "database/sql"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"memstation/apps/server/internal/cache"
	"memstation/apps/server/internal/ports"
)

type Snapshot struct {
	ChatModel                 string `json:"chat_model"`
	SummaryModel              string `json:"summary_model"`
	AnalysisModel             string `json:"analysis_model"`
	EmbeddingModel            string `json:"embedding_model"`
	EmbeddingBaseURL          string `json:"embedding_base_url"`
	EmbeddingAPIKeyConfigured bool   `json:"embedding_api_key_configured"`
	RetainSourceMaterials     bool   `json:"retain_source_materials"`
	ToolWriteEnabled          bool   `json:"tool_write_enabled"`
	MaxInjectionTokens        int    `json:"max_injection_tokens"`
}

type EmbeddingRuntime struct {
	Model         string
	BaseURL       string
	APIKey        string
	HasUserAPIKey bool
}

type Service struct {
	db        *sqlpkg.DB
	encryptor ports.Encryptor

	mu                   sync.RWMutex
	defaults             Snapshot
	users                map[string]Snapshot
	userEmbeddingAPIKeys map[string]string
	cache                *cache.JSONStore
}

func NewService(db *sqlpkg.DB, encryptor ports.Encryptor, chatModel, summaryModel, analysisModel, embeddingModel string, maxInjectionTokens int) *Service {
	return &Service{
		db:        db,
		encryptor: encryptor,
		defaults: Snapshot{
			ChatModel:             chatModel,
			SummaryModel:          summaryModel,
			AnalysisModel:         analysisModel,
			EmbeddingModel:        embeddingModel,
			RetainSourceMaterials: true,
			MaxInjectionTokens:    maxInjectionTokens,
		},
		users:                map[string]Snapshot{},
		userEmbeddingAPIKeys: map[string]string{},
	}
}

func (s *Service) SetCache(store *cache.JSONStore) {
	s.cache = store
}

func (s *Service) Snapshot() map[string]any {
	snapshot, err := s.SnapshotForUser(context.Background(), "admin")
	if err != nil {
		return s.defaultSnapshotMap()
	}
	return snapshot
}

func (s *Service) SnapshotForUser(ctx context.Context, userID string) (map[string]any, error) {
	if s.cache != nil {
		var cached Snapshot
		ok, err := s.cache.Get(ctx, settingsCacheKey(userID), &cached)
		if err == nil && ok {
			return cached.asMap(), nil
		}
	}

	s.mu.RLock()
	defaults := s.defaults
	userSnapshot, hasUserSnapshot := s.users[userID]
	userEmbeddingAPIKey := s.userEmbeddingAPIKeys[userID]
	s.mu.RUnlock()

	if s.db == nil {
		if hasUserSnapshot {
			userSnapshot.EmbeddingAPIKeyConfigured = strings.TrimSpace(userEmbeddingAPIKey) != ""
			if s.cache != nil {
				_ = s.cache.Set(ctx, settingsCacheKey(userID), userSnapshot)
			}
			return userSnapshot.asMap(), nil
		}
		defaults.EmbeddingAPIKeyConfigured = strings.TrimSpace(userEmbeddingAPIKey) != ""
		if s.cache != nil {
			_ = s.cache.Set(ctx, settingsCacheKey(userID), defaults)
		}
		return defaults.asMap(), nil
	}

	snapshot, err := s.loadSnapshotFromDB(ctx, userID, defaults)
	if err != nil {
		return nil, err
	}
	if s.cache != nil {
		_ = s.cache.Set(ctx, settingsCacheKey(userID), snapshot)
	}
	return snapshot.asMap(), nil
}

func (s *Service) Update(fields map[string]any) map[string]any {
	snapshot, err := s.UpdateForUser(context.Background(), "admin", fields)
	if err != nil {
		return s.Snapshot()
	}
	return snapshot
}

func (s *Service) UpdateForUser(ctx context.Context, userID string, fields map[string]any) (map[string]any, error) {
	current, err := s.SnapshotForUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	snapshot := Snapshot{
		ChatModel:                 anyString(current["chat_model"]),
		SummaryModel:              anyString(current["summary_model"]),
		AnalysisModel:             anyString(current["analysis_model"]),
		EmbeddingModel:            anyString(current["embedding_model"]),
		EmbeddingBaseURL:          anyString(current["embedding_base_url"]),
		EmbeddingAPIKeyConfigured: anyBool(current["embedding_api_key_configured"]),
		RetainSourceMaterials:     anyBool(current["retain_source_materials"]),
		ToolWriteEnabled:          anyBool(current["tool_write_enabled"]),
		MaxInjectionTokens:        anyInt(current["max_injection_tokens"]),
	}
	if value, ok := fields["chat_model"]; ok {
		snapshot.ChatModel = strings.TrimSpace(stringValue(value))
	}
	if value, ok := fields["summary_model"]; ok {
		snapshot.SummaryModel = strings.TrimSpace(stringValue(value))
	}
	if value, ok := fields["analysis_model"]; ok {
		snapshot.AnalysisModel = strings.TrimSpace(stringValue(value))
	}
	if value, ok := fields["embedding_model"]; ok {
		snapshot.EmbeddingModel = strings.TrimSpace(stringValue(value))
	}
	if value, ok := fields["embedding_base_url"]; ok {
		snapshot.EmbeddingBaseURL = strings.TrimSpace(stringValue(value))
	}
	if value, ok := fields["retain_source_materials"]; ok {
		snapshot.RetainSourceMaterials = boolOrDefault(value, snapshot.RetainSourceMaterials)
	}
	if value, ok := fields["tool_write_enabled"]; ok {
		snapshot.ToolWriteEnabled = boolOrDefault(value, snapshot.ToolWriteEnabled)
	}
	if value, ok := fields["max_injection_tokens"]; ok {
		snapshot.MaxInjectionTokens = intOrDefault(value, snapshot.MaxInjectionTokens)
	}

	if s.db == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		if clearEmbeddingAPIKey(fields) {
			delete(s.userEmbeddingAPIKeys, userID)
		}
		if value, ok := fields["embedding_api_key"]; ok {
			apiKey := strings.TrimSpace(stringValue(value))
			if apiKey != "" {
				s.userEmbeddingAPIKeys[userID] = apiKey
			}
		}
		snapshot.EmbeddingAPIKeyConfigured = strings.TrimSpace(s.userEmbeddingAPIKeys[userID]) != ""
		s.users[userID] = snapshot
		if s.cache != nil {
			_ = s.cache.Set(ctx, settingsCacheKey(userID), snapshot)
		}
		return snapshot.asMap(), nil
	}

	encryptedEmbeddingAPIKey, err := s.loadEncryptedEmbeddingAPIKey(ctx, userID)
	if err != nil {
		return nil, err
	}
	if clearEmbeddingAPIKey(fields) {
		encryptedEmbeddingAPIKey = ""
	}
	if value, ok := fields["embedding_api_key"]; ok {
		apiKey := strings.TrimSpace(stringValue(value))
		if apiKey != "" {
			if s.encryptor == nil {
				return nil, errors.New("settings encryptor is not configured")
			}
			ciphertext, encryptErr := s.encryptor.Encrypt(ctx, []byte(apiKey))
			if encryptErr != nil {
				return nil, encryptErr
			}
			encryptedEmbeddingAPIKey = ciphertext
		}
	}
	snapshot.EmbeddingAPIKeyConfigured = strings.TrimSpace(encryptedEmbeddingAPIKey) != ""

	now := time.Now().UTC()
	_, err = s.db.ExecContext(ctx, `
        INSERT INTO user_settings (
            id, user_id, chat_model, summary_model, analysis_model, embedding_model,
            embedding_base_url, embedding_api_key_encrypted, retain_source_materials,
            tool_write_enabled, max_injection_tokens, created_at, updated_at
        ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$12)
        ON CONFLICT (user_id) DO UPDATE SET
            chat_model = EXCLUDED.chat_model,
            summary_model = EXCLUDED.summary_model,
            analysis_model = EXCLUDED.analysis_model,
            embedding_model = EXCLUDED.embedding_model,
            embedding_base_url = EXCLUDED.embedding_base_url,
            embedding_api_key_encrypted = EXCLUDED.embedding_api_key_encrypted,
            retain_source_materials = EXCLUDED.retain_source_materials,
            tool_write_enabled = EXCLUDED.tool_write_enabled,
            max_injection_tokens = EXCLUDED.max_injection_tokens,
            updated_at = EXCLUDED.updated_at
    `,
		uuid.NewString(),
		userID,
		snapshot.ChatModel,
		snapshot.SummaryModel,
		snapshot.AnalysisModel,
		snapshot.EmbeddingModel,
		nullString(snapshot.EmbeddingBaseURL),
		nullString(encryptedEmbeddingAPIKey),
		snapshot.RetainSourceMaterials,
		snapshot.ToolWriteEnabled,
		snapshot.MaxInjectionTokens,
		now,
	)
	if err != nil {
		return nil, err
	}
	if s.cache != nil {
		_ = s.cache.Set(ctx, settingsCacheKey(userID), snapshot)
	}
	return snapshot.asMap(), nil
}

func (s *Service) ResolveEmbeddingRuntime(ctx context.Context, userID string) (EmbeddingRuntime, error) {
	snapshot, err := s.SnapshotForUser(ctx, userID)
	if err != nil {
		return EmbeddingRuntime{}, err
	}

	runtime := EmbeddingRuntime{
		Model:   strings.TrimSpace(anyString(snapshot["embedding_model"])),
		BaseURL: strings.TrimSpace(anyString(snapshot["embedding_base_url"])),
	}
	if s.db == nil {
		s.mu.RLock()
		runtime.APIKey = s.userEmbeddingAPIKeys[userID]
		s.mu.RUnlock()
		runtime.HasUserAPIKey = strings.TrimSpace(runtime.APIKey) != ""
		return runtime, nil
	}

	encryptedEmbeddingAPIKey, err := s.loadEncryptedEmbeddingAPIKey(ctx, userID)
	if err != nil {
		return EmbeddingRuntime{}, err
	}
	if strings.TrimSpace(encryptedEmbeddingAPIKey) == "" {
		return runtime, nil
	}
	if s.encryptor == nil {
		return EmbeddingRuntime{}, errors.New("settings encryptor is not configured")
	}
	plaintext, err := s.encryptor.Decrypt(ctx, encryptedEmbeddingAPIKey)
	if err != nil {
		return EmbeddingRuntime{}, err
	}
	runtime.APIKey = string(plaintext)
	runtime.HasUserAPIKey = strings.TrimSpace(runtime.APIKey) != ""
	return runtime, nil
}

func (s *Service) loadSnapshotFromDB(ctx context.Context, userID string, defaults Snapshot) (Snapshot, error) {
	row := s.db.QueryRowContext(ctx, `
        SELECT chat_model, summary_model, analysis_model, embedding_model, embedding_base_url,
               embedding_api_key_encrypted, retain_source_materials, tool_write_enabled, max_injection_tokens
        FROM user_settings
        WHERE user_id = $1
    `, userID)

	var chatModel sqlpkg.NullString
	var summaryModel sqlpkg.NullString
	var analysisModel sqlpkg.NullString
	var embeddingModel sqlpkg.NullString
	var embeddingBaseURL sqlpkg.NullString
	var embeddingAPIKeyEncrypted sqlpkg.NullString
	var retainSourceMaterials bool
	var toolWriteEnabled bool
	var maxInjectionTokens int
	err := row.Scan(
		&chatModel,
		&summaryModel,
		&analysisModel,
		&embeddingModel,
		&embeddingBaseURL,
		&embeddingAPIKeyEncrypted,
		&retainSourceMaterials,
		&toolWriteEnabled,
		&maxInjectionTokens,
	)
	if err != nil {
		if errors.Is(err, sqlpkg.ErrNoRows) {
			return defaults, nil
		}
		return Snapshot{}, err
	}

	snapshot := defaults
	if chatModel.Valid {
		snapshot.ChatModel = chatModel.String
	}
	if summaryModel.Valid {
		snapshot.SummaryModel = summaryModel.String
	}
	if analysisModel.Valid {
		snapshot.AnalysisModel = analysisModel.String
	}
	if embeddingModel.Valid {
		snapshot.EmbeddingModel = embeddingModel.String
	}
	if embeddingBaseURL.Valid {
		snapshot.EmbeddingBaseURL = embeddingBaseURL.String
	}
	snapshot.EmbeddingAPIKeyConfigured = strings.TrimSpace(embeddingAPIKeyEncrypted.String) != ""
	snapshot.RetainSourceMaterials = retainSourceMaterials
	snapshot.ToolWriteEnabled = toolWriteEnabled
	if maxInjectionTokens > 0 {
		snapshot.MaxInjectionTokens = maxInjectionTokens
	}
	return snapshot, nil
}

func (s *Service) loadEncryptedEmbeddingAPIKey(ctx context.Context, userID string) (string, error) {
	if s.db == nil {
		s.mu.RLock()
		defer s.mu.RUnlock()
		return s.userEmbeddingAPIKeys[userID], nil
	}

	row := s.db.QueryRowContext(ctx, `
        SELECT embedding_api_key_encrypted
        FROM user_settings
        WHERE user_id = $1
    `, userID)
	var encryptedEmbeddingAPIKey sqlpkg.NullString
	if err := row.Scan(&encryptedEmbeddingAPIKey); err != nil {
		if errors.Is(err, sqlpkg.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return encryptedEmbeddingAPIKey.String, nil
}

func clearEmbeddingAPIKey(fields map[string]any) bool {
	value, ok := fields["clear_embedding_api_key"]
	if !ok {
		return false
	}
	return boolOrDefault(value, false)
}

func nullString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func (s *Service) defaultSnapshotMap() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.defaults.asMap()
}

func (s Snapshot) asMap() map[string]any {
	return map[string]any{
		"chat_model":                   s.ChatModel,
		"summary_model":                s.SummaryModel,
		"analysis_model":               s.AnalysisModel,
		"embedding_model":              s.EmbeddingModel,
		"embedding_base_url":           s.EmbeddingBaseURL,
		"embedding_api_key_configured": s.EmbeddingAPIKeyConfigured,
		"retain_source_materials":      s.RetainSourceMaterials,
		"tool_write_enabled":           s.ToolWriteEnabled,
		"max_injection_tokens":         s.MaxInjectionTokens,
	}
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	if typed, ok := value.(string); ok {
		return typed
	}
	return ""
}

func boolOrDefault(value any, fallback bool) bool {
	typed, ok := value.(bool)
	if !ok {
		return fallback
	}
	return typed
}

func intOrDefault(value any, fallback int) int {
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
		return fallback
	}
}

func anyString(value any) string {
	if typed, ok := value.(string); ok {
		return typed
	}
	return ""
}

func anyBool(value any) bool {
	typed, _ := value.(bool)
	return typed
}

func anyInt(value any) int {
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

func settingsCacheKey(userID string) string {
	return "settings:user:" + userID
}
