package memory

import "sort"

// Channel scores have different distributions. Reserve by within-channel rank,
// without changing the relevance values used by graph expansion or final ranking.
// Overlapping nodes count toward each channel; unused slots follow the original
// combined order. The returned tail lets the caller remove every excluded seed.
func balanceSeedChannels(ordered []string, limit int, channels ...map[string]float64) []string {
	if len(channels) == 0 || len(ordered) <= limit {
		return ordered
	}
	quota := limit / len(channels)
	if quota == 0 {
		return ordered
	}
	out := make([]string, 0, len(ordered))
	seen := map[string]bool{}
	for _, scores := range channels {
		channel := append([]string(nil), ordered...)
		sort.SliceStable(channel, func(i, j int) bool { return scores[channel[i]] > scores[channel[j]] })
		selected := 0
		for _, id := range channel {
			if scores[id] <= 0 || selected >= quota {
				break
			}
			selected++
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	for _, id := range ordered {
		if !seen[id] {
			out = append(out, id)
		}
	}
	return out
}
