package recordutil

import (
	"fmt"
	"strings"
)

func MatchesGenerationValue(userID, requestedGenerationID, actualGenerationID string, includeGenerationless bool) bool {
	requestedGenerationID = strings.TrimSpace(requestedGenerationID)
	actualGenerationID = strings.TrimSpace(actualGenerationID)
	if requestedGenerationID == "" {
		return true
	}
	if actualGenerationID == requestedGenerationID {
		return true
	}
	if !includeGenerationless {
		return false
	}
	return actualGenerationID == ""
}

func CurrentGenerationID(workspaceID string) string {
	return "gen:current:" + workspaceID
}

func GenerationFilterArgs(userID, generationID string, args ...any) []any {
	out := append([]any{}, args...)
	return AppendGenerationArg(out, userID, generationID)
}

func AppendGenerationArg(args []any, userID, generationID string) []any {
	if strings.TrimSpace(generationID) == "" {
		return args
	}
	return append(args, generationID)
}

func GenerationFilterClause(column string, argIndex int, userID, generationID string, includeGenerationless bool) string {
	generationID = strings.TrimSpace(generationID)
	if generationID == "" {
		return ""
	}
	if includeGenerationless {
		return fmt.Sprintf(" AND (%s = $%d OR %s = '')", column, argIndex, column)
	}
	return fmt.Sprintf(" AND %s = $%d", column, argIndex)
}

func CurrentGenerationIDForSQL(workspaceID string) string {
	return "gen:current:" + workspaceID
}
