package service

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelHealthSecretEnvelopeRoundTripAndTamperFailure(t *testing.T) {
	t.Setenv("CRYPTO_SECRET", "stable-secret-for-model-health-tests")
	t.Setenv("SESSION_SECRET", "")

	envelope, err := EncryptModelHealthSecret("sk-sensitive")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(envelope, "v1:"))
	assert.NotContains(t, envelope, "sk-sensitive")

	plaintext, err := DecryptModelHealthSecret(envelope)
	require.NoError(t, err)
	assert.Equal(t, "sk-sensitive", plaintext)

	encoded := strings.TrimPrefix(envelope, modelHealthSecretVersion+":")
	sealed, err := base64.RawURLEncoding.DecodeString(encoded)
	require.NoError(t, err)
	sealed[len(sealed)-1] ^= 0xff
	tampered := modelHealthSecretVersion + ":" + base64.RawURLEncoding.EncodeToString(sealed)
	_, err = DecryptModelHealthSecret(tampered)
	require.Error(t, err)
}

func TestModelHealthSecretEnvelopeChangesWithConfiguredSecret(t *testing.T) {
	t.Setenv("CRYPTO_SECRET", "first-stable-secret")
	envelope, err := EncryptModelHealthSecret("secret")
	require.NoError(t, err)

	t.Setenv("CRYPTO_SECRET", "second-stable-secret")
	_, err = DecryptModelHealthSecret(envelope)
	require.Error(t, err)
}

func TestModelHealthStableSecretRequiresExplicitEnvironmentValue(t *testing.T) {
	t.Setenv("CRYPTO_SECRET", "")
	t.Setenv("SESSION_SECRET", "stable-session-secret")
	assert.False(t, ModelHealthStableSecretConfigured())

	t.Setenv("CRYPTO_SECRET", "stable-crypto-secret")
	assert.True(t, ModelHealthStableSecretConfigured())
}

func TestMaskModelHealthEndpointRemovesPathAndQuery(t *testing.T) {
	masked := MaskModelHealthEndpoint("https://user:pass@api.example.com:8443/private/v1?token=secret")
	assert.Equal(t, "https://ap********om:8443/...", masked)
	assert.NotContains(t, masked, "private")
	assert.NotContains(t, masked, "secret")
}

func TestUpdateModelHealthTargetCredentialsUsesReplaceOnlySemantics(t *testing.T) {
	t.Setenv("CRYPTO_SECRET", "stable-secret-for-replace-only-test")
	target := &model.ModelHealthTarget{Mode: model.ModelHealthModeUpstream}

	require.NoError(t, UpdateModelHealthTargetCredentials(target, "https://api.example.com/v1", "first-key"))
	firstEndpoint := target.EndpointEncrypted
	firstKey := target.APIKeyEncrypted
	firstFingerprint := target.CredentialFingerprint
	require.NotEmpty(t, firstEndpoint)
	require.NotEmpty(t, firstKey)

	require.NoError(t, UpdateModelHealthTargetCredentials(target, "", ""))
	assert.Equal(t, firstEndpoint, target.EndpointEncrypted)
	assert.Equal(t, firstKey, target.APIKeyEncrypted)
	assert.Equal(t, firstFingerprint, target.CredentialFingerprint)

	require.NoError(t, UpdateModelHealthTargetCredentials(target, "", "second-key"))
	assert.Equal(t, firstEndpoint, target.EndpointEncrypted)
	assert.NotEqual(t, firstKey, target.APIKeyEncrypted)
	assert.NotEqual(t, firstFingerprint, target.CredentialFingerprint)
}

func TestModelHealthTargetCredentialMetadataChecksBothEnvelopes(t *testing.T) {
	t.Setenv("CRYPTO_SECRET", "stable-secret-for-metadata-test")
	target := &model.ModelHealthTarget{Mode: model.ModelHealthModeUpstream}
	require.NoError(t, UpdateModelHealthTargetCredentials(target, "https://api.example.com/v1", "valid-key"))

	_, hasKey, _, err := ModelHealthTargetCredentialMetadata(target)
	require.NoError(t, err)
	assert.True(t, hasKey)

	target.APIKeyEncrypted = "v1:broken"
	_, hasKey, _, err = ModelHealthTargetCredentialMetadata(target)
	require.ErrorIs(t, err, ErrModelHealthCredentialsNeedReplacement)
	assert.True(t, hasKey)
}

func TestModelHealthCredentialEnvelopeRejectsOversizedInput(t *testing.T) {
	t.Setenv("CRYPTO_SECRET", "stable-secret-for-size-limit-test")
	_, err := EncryptModelHealthSecret(strings.Repeat("x", modelHealthMaxSecretBytes+1))
	require.Error(t, err)
	_, err = DecryptModelHealthSecret(strings.Repeat("x", modelHealthMaxEnvelopeBytes+1))
	require.Error(t, err)
}
