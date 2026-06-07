package handler

import "fmt"

const (
	defaultSearchLimit             = 50
	defaultWorkspaceInventoryDepth = 4
	defaultWorkspaceInventoryLimit = 200
)

func effectiveOptionalLimit(value *int, defaultValue int) (int, error) {
	if value == nil {
		return defaultValue, nil
	}
	if *value < 1 {
		return 0, fmt.Errorf("limit must be >= 1")
	}
	return *value, nil
}

func effectiveOptionalNonNegative(value *int, defaultValue int, name string) (int, error) {
	if value == nil {
		return defaultValue, nil
	}
	if *value < 0 {
		return 0, fmt.Errorf("%s must be >= 0", name)
	}
	return *value, nil
}
