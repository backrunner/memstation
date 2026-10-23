package resolver

import "time"

func searchString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func parseSearchTime(value *string) (*time.Time, error) {
	if value == nil || *value == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339Nano, *value)
	if err != nil {
		return nil, err
	}
	return &t, nil
}
