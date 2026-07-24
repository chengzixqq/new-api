package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetUserGroupRatioAppliesPerUserOverride(t *testing.T) {
	overrides := map[string]float64{"edit_this": 0.7}

	assert.Equal(t, 0.7, GetUserGroupRatio("vip", "edit_this", overrides))
}

func TestGetUserGroupRatioFallsBackToMembershipGroupRule(t *testing.T) {
	assert.Equal(t, 0.9, GetUserGroupRatio("vip", "edit_this", nil))
}
