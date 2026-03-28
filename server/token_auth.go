package server

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

var ErrInvalidClientToken = errors.New("invalid client token")

type InvalidClientTokenError struct {
	Operation  string
	StatusCode int
	Token      string
	Detail     string
}

func (e *InvalidClientTokenError) Error() string {
	if e == nil {
		return ErrInvalidClientToken.Error()
	}

	base := ErrInvalidClientToken.Error()
	if e.Operation != "" {
		base = fmt.Sprintf("%s during %s", base, e.Operation)
	}
	if e.Detail != "" {
		return base + ": " + e.Detail
	}
	if e.StatusCode > 0 {
		return fmt.Sprintf("%s: status=%d", base, e.StatusCode)
	}
	return base
}

func (e *InvalidClientTokenError) Unwrap() error {
	return ErrInvalidClientToken
}

type InvalidClientTokenHandler func(error) bool

var invalidClientTokenHandler InvalidClientTokenHandler

func SetInvalidClientTokenHandler(handler InvalidClientTokenHandler) {
	invalidClientTokenHandler = handler
}

func IsInvalidClientTokenError(err error) bool {
	return errors.Is(err, ErrInvalidClientToken)
}

func handleInvalidClientToken(err error) bool {
	if invalidClientTokenHandler == nil || !IsInvalidClientTokenError(err) {
		return false
	}
	return invalidClientTokenHandler(err)
}

func classifyClientTokenResponse(operation, token string, statusCode int, body string) error {
	body = strings.TrimSpace(body)
	if indicatesInvalidClientTokenResponse(statusCode, body) {
		return &InvalidClientTokenError{
			Operation:  operation,
			StatusCode: statusCode,
			Token:      strings.TrimSpace(token),
			Detail:     body,
		}
	}
	if body == "" {
		return fmt.Errorf("status code: %d", statusCode)
	}
	return fmt.Errorf("status code: %d, %s", statusCode, body)
}

func indicatesInvalidClientTokenResponse(statusCode int, body string) bool {
	if statusCode != http.StatusUnauthorized {
		return false
	}

	body = strings.ToLower(strings.TrimSpace(body))
	if body == "" {
		return false
	}

	return strings.Contains(body, "invalid token") ||
		strings.Contains(body, "token is required") ||
		strings.Contains(body, "failed to validate token")
}
