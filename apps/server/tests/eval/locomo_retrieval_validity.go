package eval

import "memstation/apps/server/internal/domain"

// Entity recall is mandatory in the current production evaluation protocol.
// A healthy empty extraction differs from an unavailable/partial channel.
// Operational degradation can serve a live request, but is not a complete
// quality measurement and must not authorize a candidate replacement.
func pipelineEntityRecallComplete(diagnostic *domain.EntityRecallDiagnostics) bool {
	return diagnostic != nil && (diagnostic.Status == "complete" || diagnostic.Status == "no_entities")
}
