package resolver

import (
	adminservice "memstation/apps/server/internal/service/admin"
	"memstation/apps/server/internal/service/aibroker"
	apikeyservice "memstation/apps/server/internal/service/apikey"
	auditsvc "memstation/apps/server/internal/service/audit"
	conversationservice "memstation/apps/server/internal/service/conversation"
	"memstation/apps/server/internal/service/identity"
	"memstation/apps/server/internal/service/ingestion"
	"memstation/apps/server/internal/service/lifecycle"
	memoryservice "memstation/apps/server/internal/service/memory"
	"memstation/apps/server/internal/service/memorycontrol"
	"memstation/apps/server/internal/service/memorygen"
	settingsservice "memstation/apps/server/internal/service/settings"
	usageservice "memstation/apps/server/internal/service/usage"
	userservice "memstation/apps/server/internal/service/user"
	"memstation/apps/server/internal/worker"
)

type Resolver struct {
	Generations          *memorygen.Service
	Identity             *identity.Service
	Ingestion            *ingestion.Service
	Lifecycle            *lifecycle.Service
	MemoryControlService *memorycontrol.Service
	AIUsage              *aibroker.UsageService
	UserService          *userservice.Service
	APIKeyService        *apikeyservice.Service
	SettingsService      *settingsservice.Service
	ConversationService  *conversationservice.Service
	MemoryService        *memoryservice.Service
	AdminService         *adminservice.Service
	UsageService         *usageservice.Service
	AuditService         *auditsvc.Service
	Publisher            *worker.Publisher
}
