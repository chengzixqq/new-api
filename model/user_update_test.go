package model

import (
	"errors"
	"fmt"
	"math"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupUserUpdateTestState(t *testing.T) {
	t.Helper()
	truncateTables(t)
	require.NoError(t, DB.Exec("DELETE FROM users").Error)

	oldRedisEnabled := common.RedisEnabled
	oldBatchUpdateEnabled := common.BatchUpdateEnabled
	common.RedisEnabled = false
	common.BatchUpdateEnabled = false
	t.Cleanup(func() {
		common.RedisEnabled = oldRedisEnabled
		common.BatchUpdateEnabled = oldBatchUpdateEnabled
	})
}

func TestUserUpdateDoesNotOverwriteAccountingFields(t *testing.T) {
	setupUserUpdateTestState(t)

	user := User{
		Id:           1,
		Username:     "quota-race-user",
		Password:     "password",
		DisplayName:  "before",
		Status:       common.UserStatusEnabled,
		Quota:        1000,
		UsedQuota:    20,
		RequestCount: 3,
	}
	require.NoError(t, DB.Create(&user).Error)

	staleUser, err := GetUserById(user.Id, true)
	require.NoError(t, err)

	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).Updates(map[string]interface{}{
		"quota":         gorm.Expr("quota - ?", 400),
		"used_quota":    gorm.Expr("used_quota + ?", 400),
		"request_count": gorm.Expr("request_count + ?", 1),
	}).Error)

	staleUser.DisplayName = "after"
	require.NoError(t, staleUser.Update(false))

	var got User
	require.NoError(t, DB.First(&got, user.Id).Error)
	assert.Equal(t, "after", got.DisplayName)
	assert.Equal(t, 600, got.Quota)
	assert.Equal(t, 420, got.UsedQuota)
	assert.Equal(t, 4, got.RequestCount)
}

func TestUpdateUserSettingOnlyUpdatesSetting(t *testing.T) {
	setupUserUpdateTestState(t)

	user := User{
		Id:           2,
		Username:     "setting-user",
		Password:     "password",
		Status:       common.UserStatusEnabled,
		Quota:        1000,
		UsedQuota:    20,
		RequestCount: 3,
	}
	require.NoError(t, DB.Create(&user).Error)

	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).Updates(map[string]interface{}{
		"quota":         gorm.Expr("quota - ?", 250),
		"used_quota":    gorm.Expr("used_quota + ?", 250),
		"request_count": gorm.Expr("request_count + ?", 1),
	}).Error)

	require.NoError(t, UpdateUserSetting(user.Id, dto.UserSetting{Language: "zh"}))

	var got User
	require.NoError(t, DB.First(&got, user.Id).Error)
	assert.Equal(t, 750, got.Quota)
	assert.Equal(t, 270, got.UsedQuota)
	assert.Equal(t, 4, got.RequestCount)
	assert.Equal(t, "zh", got.GetSetting().Language)
}

func TestValidateGroupRatioOverrides(t *testing.T) {
	tests := []struct {
		name      string
		overrides map[string]float64
		wantError bool
	}{
		{name: "nil", overrides: nil},
		{name: "empty", overrides: map[string]float64{}},
		{name: "valid discount", overrides: map[string]float64{"vip": 0.75}},
		{name: "valid surcharge", overrides: map[string]float64{"legacy-group": 2}},
		{name: "zero", overrides: map[string]float64{"vip": 0}, wantError: true},
		{name: "negative", overrides: map[string]float64{"vip": -0.5}, wantError: true},
		{name: "nan", overrides: map[string]float64{"vip": math.NaN()}, wantError: true},
		{name: "infinity", overrides: map[string]float64{"vip": math.Inf(1)}, wantError: true},
		{name: "too large", overrides: map[string]float64{"vip": 1000.01}, wantError: true},
		{name: "blank group", overrides: map[string]float64{" ": 0.5}, wantError: true},
		{name: "surrounding whitespace", overrides: map[string]float64{" vip ": 0.5}, wantError: true},
		{name: "group name too long", overrides: map[string]float64{"12345678901234567890123456789012345678901234567890123456789012345": 0.5}, wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateGroupRatioOverrides(tt.overrides)
			if tt.wantError {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
		})
	}

	tooMany := make(map[string]float64, 257)
	for i := 0; i < 257; i++ {
		tooMany[fmt.Sprintf("group-%d", i)] = 1
	}
	assert.Error(t, ValidateGroupRatioOverrides(tooMany))
}

func TestEditUserGroupRatioOverridesOmittedPreservesAndEmptyClears(t *testing.T) {
	setupUserUpdateTestState(t)

	user := User{
		Id:                     3,
		Username:               "ratio-user",
		Password:               "password",
		DisplayName:            "Ratio User",
		Group:                  "default",
		Status:                 common.UserStatusEnabled,
		GroupRatioOverridesRaw: `{"vip":0.8}`,
	}
	require.NoError(t, DB.Create(&user).Error)

	omitted, err := GetUserById(user.Id, false)
	require.NoError(t, err)
	omitted.GroupRatioOverrides = nil
	require.NoError(t, omitted.EditWithTx(DB, false))

	var stored User
	require.NoError(t, DB.First(&stored, user.Id).Error)
	assert.JSONEq(t, `{"vip":0.8}`, stored.GroupRatioOverridesRaw)

	omitted.GroupRatioOverrides = map[string]float64{}
	require.NoError(t, omitted.EditWithTx(DB, false))
	require.NoError(t, DB.First(&stored, user.Id).Error)
	assert.JSONEq(t, `{}`, stored.GroupRatioOverridesRaw)

	omitted.GroupRatioOverrides = map[string]float64{"vip": 0.65, "legacy": 1.2}
	require.NoError(t, omitted.EditWithTx(DB, false))
	require.NoError(t, DB.First(&stored, user.Id).Error)
	require.NoError(t, stored.LoadGroupRatioOverrides())
	assert.Equal(t, omitted.GroupRatioOverrides, stored.GroupRatioOverrides)
}

func TestUserBaseWriteContextIncludesGroupRatioOverrides(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	cache := UserBase{
		Id:                  42,
		GroupRatioOverrides: `{"vip":0.75}`,
	}

	cache.WriteContext(ctx)

	overrides, ok := common.GetContextKeyType[map[string]float64](ctx, constant.ContextKeyUserGroupRatioOverrides)
	require.True(t, ok)
	assert.Equal(t, map[string]float64{"vip": 0.75}, overrides)
}

func TestEnsureEmailAvailableRejectsExistingEmailCaseInsensitive(t *testing.T) {
	setupUserUpdateTestState(t)

	require.NoError(t, DB.Create(&User{
		Username: "existing",
		Password: "old-password",
		Email:    "Taken@Example.com",
		Status:   common.UserStatusEnabled,
	}).Error)

	err := EnsureEmailAvailable(" taken@example.COM ", 0)
	require.ErrorIs(t, err, ErrEmailAlreadyTaken)

	user, err := GetUniqueUserByEmail("TAKEN@example.com")
	require.NoError(t, err)
	assert.Equal(t, "existing", user.Username)

	require.NoError(t, EnsureEmailAvailable("taken@example.com", user.Id))
}

func TestInsertRejectsDuplicateEmailWithoutUniqueIndex(t *testing.T) {
	setupUserUpdateTestState(t)

	require.NoError(t, DB.Create(&User{
		Username: "existing",
		Password: "old-password",
		Email:    "taken@example.com",
		Status:   common.UserStatusEnabled,
	}).Error)

	user := &User{
		Username: "oauth-user",
		Email:    "TAKEN@example.com",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
	}

	err := user.Insert(0)
	require.ErrorIs(t, err, ErrEmailAlreadyTaken)

	var count int64
	require.NoError(t, DB.Model(&User{}).Where("username = ?", "oauth-user").Count(&count).Error)
	assert.Zero(t, count)
}

func TestInsertKeepsBlankPasswordForPasswordlessUser(t *testing.T) {
	setupUserUpdateTestState(t)

	user := &User{
		Username: "passwordless-user",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
	}

	require.NoError(t, user.Insert(0))

	var stored User
	require.NoError(t, DB.Where("username = ?", user.Username).First(&stored).Error)
	assert.Empty(t, stored.Password)
}

func TestValidateAndFillRejectsPasswordlessUser(t *testing.T) {
	setupUserUpdateTestState(t)

	require.NoError(t, DB.Create(&User{
		Username: "passwordless-user",
		Password: "",
		Status:   common.UserStatusEnabled,
	}).Error)

	loginUser := User{
		Username: "passwordless-user",
		Password: "NewPassword123",
	}
	err := loginUser.ValidateAndFill()
	require.ErrorIs(t, err, ErrInvalidCredentials)

	var stored User
	require.NoError(t, DB.Where("username = ?", "passwordless-user").First(&stored).Error)
	assert.Empty(t, stored.Password)
}

func TestResetUserPasswordByEmailRequiresSingleActiveMatch(t *testing.T) {
	setupUserUpdateTestState(t)

	require.NoError(t, DB.Create(&User{
		Username: "duplicate-1",
		Password: "old-1",
		Email:    "legacy@example.com",
		AffCode:  "dupe1",
		Status:   common.UserStatusEnabled,
	}).Error)
	require.NoError(t, DB.Create(&User{
		Username: "duplicate-2",
		Password: "old-2",
		Email:    "LEGACY@example.com",
		AffCode:  "dupe2",
		Status:   common.UserStatusEnabled,
	}).Error)

	err := ResetUserPasswordByEmail("legacy@example.com", "NewPassword123")
	require.ErrorIs(t, err, ErrEmailAmbiguous)

	var duplicates []User
	require.NoError(t, DB.Where("LOWER(email) = ?", "legacy@example.com").Order("username asc").Find(&duplicates).Error)
	require.Len(t, duplicates, 2)
	assert.Equal(t, "old-1", duplicates[0].Password)
	assert.Equal(t, "old-2", duplicates[1].Password)

	require.NoError(t, DB.Create(&User{
		Username: "unique",
		Password: "old",
		Email:    "unique@example.com",
		AffCode:  "unique",
		Status:   common.UserStatusEnabled,
	}).Error)

	require.NoError(t, ResetUserPasswordByEmail("UNIQUE@example.com", "NewPassword123"))

	var unique User
	require.NoError(t, DB.Where("username = ?", "unique").First(&unique).Error)
	assert.True(t, common.ValidatePasswordAndHash("NewPassword123", unique.Password))

	err = ResetUserPasswordByEmail("missing@example.com", "NewPassword123")
	require.True(t, errors.Is(err, ErrEmailNotFound))
}
