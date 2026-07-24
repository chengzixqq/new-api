package service

import (
	"bytes"
	"image"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImageConfigDecodersRejectMalformedWebP(t *testing.T) {
	malformedWebP := []struct {
		name string
		data []byte
	}{
		{
			name: "truncated riff header",
			data: []byte("RIFF\x04\x00\x00\x00WEBP"),
		},
		{
			name: "truncated extended header",
			data: []byte("RIFF\x12\x00\x00\x00WEBPVP8X\x0a\x00\x00\x00\x00"),
		},
		{
			name: "oversized lossy chunk",
			data: []byte("RIFF\xff\xff\xff\x7fWEBPVP8 \xff\xff\xff\x7f\x9d\x01\x2a"),
		},
	}

	decoders := []struct {
		name   string
		decode func([]byte) (image.Config, string, error)
	}{
		{
			name:   "file service",
			decode: decodeImageConfig,
		},
		{
			name: "image service",
			decode: func(data []byte) (image.Config, string, error) {
				return getImageConfig(bytes.NewReader(data))
			},
		},
	}

	for _, decoder := range decoders {
		for _, malformed := range malformedWebP {
			t.Run(decoder.name+"/"+malformed.name, func(t *testing.T) {
				var err error
				require.NotPanics(t, func() {
					_, _, err = decoder.decode(malformed.data)
				})
				assert.Error(t, err)
			})
		}
	}
}
