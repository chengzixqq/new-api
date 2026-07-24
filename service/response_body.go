package service

import (
	"errors"
	"io"
)

var ErrResponseBodyTooLarge = errors.New("upstream response body exceeds the configured limit")

func ReadResponseBodyLimited(reader io.Reader, maxBytes int64) ([]byte, error) {
	if reader == nil {
		return nil, errors.New("upstream response body is nil")
	}
	if maxBytes <= 0 {
		return nil, errors.New("response body limit must be positive")
	}
	body, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, ErrResponseBodyTooLarge
	}
	return body, nil
}
