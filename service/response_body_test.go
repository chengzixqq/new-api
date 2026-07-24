package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadResponseBodyLimited(t *testing.T) {
	body, err := ReadResponseBodyLimited(strings.NewReader("1234"), 4)
	require.NoError(t, err)
	assert.Equal(t, []byte("1234"), body)

	_, err = ReadResponseBodyLimited(strings.NewReader("12345"), 4)
	require.ErrorIs(t, err, ErrResponseBodyTooLarge)
}
