package model

import "errors"

// Common errors
var (
	ErrDatabase = errors.New("database error")
	// ErrInsufficientUserQuota and ErrInsufficientTokenQuota are returned by
	// conditional quota updates. Callers must use errors.Is so the database
	// remains the authority when concurrent requests race for the same balance.
	ErrInsufficientUserQuota  = errors.New("insufficient user quota")
	ErrInsufficientTokenQuota = errors.New("insufficient token quota")
)

// User auth errors
var (
	ErrInvalidCredentials   = errors.New("invalid credentials")
	ErrUserEmptyCredentials = errors.New("empty credentials")
	ErrEmailAlreadyTaken    = errors.New("email already taken")
	ErrEmailNotFound        = errors.New("email not found")
	ErrEmailAmbiguous       = errors.New("email matches multiple users")
)

// Token auth errors
var (
	ErrTokenNotProvided = errors.New("token not provided")
	ErrTokenInvalid     = errors.New("token invalid")
)

// Redemption errors
var ErrRedeemFailed = errors.New("redeem.failed")

// 2FA errors
var ErrTwoFANotEnabled = errors.New("2fa not enabled")
