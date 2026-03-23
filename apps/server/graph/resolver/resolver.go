package resolver

import (
	adminservice "memstation/apps/server/internal/service/admin"
	apikeyservice "memstation/apps/server/internal/service/apikey"
	auditsvc "memstation/apps/server/internal/service/audit"
	conversationservice "memstation/apps/server/internal/service/conversation"
	memoryservice "memstation/apps/server/internal/service/memory"
	settingsservice "memstation/apps/server/internal/service/settings"
	usageservice "memstation/apps/server/internal/service/usage"
	userservice "memstation/apps/server/internal/service/user"
	"memstation/apps/server/internal/worker"
)

type Resolver struct {
	UserService         *userservice.Service
	APIKeyService       *apikeyservice.Service
	SettingsService     *settingsservice.Service
	ConversationService *conversationservice.Service
	MemoryService       *memoryservice.Service
	AdminService        *adminservice.Service
	UsageService        *usageservice.Service
	AuditService        *auditsvc.Service
	Publisher           *worker.Publisher
}
