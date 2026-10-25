package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"time"
)

var (
	ErrConversationNotFound = errors.New("conversation not found")
	ErrAccessNotFound       = errors.New("identity not found")
	ErrAccessConflict       = errors.New("identity revision conflict")
	ErrAccessDenied         = errors.New("access denied")
	ErrMemoryTaskObsolete   = errors.New("memory task source is no longer current")
)

// These are the capabilities implemented by the current data plane. New
// capabilities must not be issued before every corresponding path enforces them.
const (
	CapabilityGateway         = "gateway.invoke"
	CapabilityMemoryRead      = "memory.read"
	CapabilityMemoryEdit      = "memory.edit"
	CapabilityFilesRead       = "files.read"
	CapabilityFilesWrite      = "files.write"
	CapabilityEventsWrite     = "events.write"
	CapabilitySourcesRead     = "sources.read"
	CapabilitySourcesRetract  = "sources.retract"
	CapabilityCandidatesWrite = "memory.propose"
	CapabilityMemoryForget    = "memory.forget"
	CapabilityMemoryExport    = "memory.export"
	CapabilityUsageRead       = "usage.read"
)

func PersonalCapabilities() []string {
	return []string{CapabilityGateway, CapabilityMemoryRead, CapabilityMemoryEdit, CapabilityFilesRead, CapabilityFilesWrite}
}

// AllCapabilities is the registry, not the default grant of a legacy key.
func AllCapabilities() []string {
	return append(PersonalCapabilities(), CapabilityEventsWrite, CapabilitySourcesRead, CapabilitySourcesRetract, CapabilityCandidatesWrite, CapabilityMemoryForget, CapabilityMemoryExport, CapabilityUsageRead)
}

type Audience struct {
	Kind     string   `json:"kind"`
	ActorIDs []string `json:"actor_ids,omitempty"`
}

func AccessID(kind, userID string) string {
	sum := sha256.Sum256([]byte(userID))
	return kind + "-" + hex.EncodeToString(sum[:])
}

func PersonalWorkspaceID(userID string) string { return AccessID("personal", userID) }
func LegacyAgentID(userID string) string       { return AccessID("legacy", userID) }

type Workspace struct {
	ID            string    `json:"id"`
	UserID        string    `json:"user_id"`
	Name          string    `json:"name"`
	Kind          string    `json:"kind"`
	Status        string    `json:"status"`
	PolicyVersion int64     `json:"policy_version"`
	Capabilities  []string  `json:"capabilities"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type WorkspaceQuery struct {
	UserID string
	After  string
	Query  string
	Limit  int
}

type WorkspacePage struct {
	Items         []Workspace `json:"items"`
	NextCursor    string      `json:"next_cursor"`
	WorkspaceMode string      `json:"workspace_mode"`
}

type Agent struct {
	ID                 string    `json:"id"`
	UserID             string    `json:"user_id"`
	ActorID            string    `json:"actor_id"`
	Name               string    `json:"name"`
	Status             string    `json:"status"`
	DefaultWorkspaceID string    `json:"default_workspace_id"`
	Revision           int64     `json:"revision"`
	Legacy             bool      `json:"legacy"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type WorkspaceGrant struct {
	UserID       string   `json:"user_id"`
	WorkspaceID  string   `json:"workspace_id"`
	AgentID      string   `json:"agent_id"`
	Capabilities []string `json:"capabilities"`
	Revision     int64    `json:"revision"`
}

type AgentQuery struct {
	UserID string
	After  string
	Query  string
	Status string
	Limit  int
}

type AgentDetail struct {
	Agent                 Agent          `json:"agent"`
	Grant                 WorkspaceGrant `json:"grant"`
	Workspace             Workspace      `json:"workspace"`
	EffectiveCapabilities []string       `json:"effective_capabilities"`
}

type CredentialAccess struct {
	Credential
	EffectiveCapabilities []string `json:"effective_capabilities"`
}

type Credential struct {
	AgentName          string     `json:"agent_name,omitempty"`
	ID                 string     `json:"id"`
	UserID             string     `json:"user_id"`
	AgentID            string     `json:"agent_id"`
	DefaultWorkspaceID string     `json:"default_workspace_id"`
	Name               string     `json:"name"`
	Prefix             string     `json:"prefix"`
	Hash               string     `json:"-"`
	Capabilities       []string   `json:"capabilities"`
	ExpiresAt          *time.Time `json:"expires_at,omitempty"`
	RevokedAt          *time.Time `json:"revoked_at,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
}

// AccessContext is constructed by authentication, never decoded from client JSON.
type AccessContext struct {
	ScopeVersion  int      `json:"scope_version,omitempty"`
	PartitionIDs  []string `json:"partition_ids,omitempty"`
	Audience      Audience `json:"audience"`
	UserID        string   `json:"user_id"`
	AgentID       string   `json:"agent_id"`
	CredentialID  string   `json:"credential_id"`
	WorkspaceID   string   `json:"workspace_id"`
	Capabilities  []string `json:"capabilities"`
	PolicyVersion int64    `json:"policy_version"`
	GrantRevision int64    `json:"grant_revision"`
	Legacy        bool     `json:"legacy"`
}

func (a AccessContext) Allows(capability string) bool {
	return slices.Contains(a.Capabilities, capability)
}

func EffectiveCapabilities(ceiling, grant, policy []string) []string {
	out := []string{}
	for _, capability := range AllCapabilities() {
		if slices.Contains(ceiling, capability) && slices.Contains(grant, capability) && slices.Contains(policy, capability) {
			out = append(out, capability)
		}
	}
	return out
}
