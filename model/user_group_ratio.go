package model

import (
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
)

const (
	maxGroupRatioOverrides = 256
	maxGroupNameLength     = 64
	maxGroupRatioOverride  = 1000
)

func IsValidGroupRatioOverride(ratio float64) bool {
	return !math.IsNaN(ratio) && !math.IsInf(ratio, 0) && ratio > 0 && ratio <= maxGroupRatioOverride
}

func ValidateGroupRatioOverrides(overrides map[string]float64) error {
	if len(overrides) > maxGroupRatioOverrides {
		return fmt.Errorf("group ratio overrides cannot contain more than %d entries", maxGroupRatioOverrides)
	}

	for group, ratio := range overrides {
		trimmedGroup := strings.TrimSpace(group)
		if trimmedGroup == "" {
			return fmt.Errorf("group ratio override contains an empty group name")
		}
		if trimmedGroup != group {
			return fmt.Errorf("group ratio override %q contains leading or trailing whitespace", group)
		}
		if utf8.RuneCountInString(group) > maxGroupNameLength {
			return fmt.Errorf("group name %q exceeds %d characters", group, maxGroupNameLength)
		}
		if !IsValidGroupRatioOverride(ratio) {
			return fmt.Errorf("group ratio override for %q must be greater than 0 and at most %g", group, float64(maxGroupRatioOverride))
		}
	}
	return nil
}

func MarshalGroupRatioOverrides(overrides map[string]float64) (string, error) {
	if err := ValidateGroupRatioOverrides(overrides); err != nil {
		return "", err
	}
	data, err := common.Marshal(overrides)
	if err != nil {
		return "", fmt.Errorf("marshal group ratio overrides: %w", err)
	}
	return string(data), nil
}

func ParseGroupRatioOverrides(raw string) (map[string]float64, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}

	var overrides map[string]float64
	if err := common.Unmarshal([]byte(raw), &overrides); err != nil {
		return nil, fmt.Errorf("unmarshal group ratio overrides: %w", err)
	}
	if err := ValidateGroupRatioOverrides(overrides); err != nil {
		return nil, err
	}
	return overrides, nil
}

func (user *User) LoadGroupRatioOverrides() error {
	overrides, err := ParseGroupRatioOverrides(user.GroupRatioOverridesRaw)
	if err != nil {
		user.GroupRatioOverrides = nil
		return err
	}
	user.GroupRatioOverrides = overrides
	return nil
}
