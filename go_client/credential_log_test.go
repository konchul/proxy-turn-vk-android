package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func TestCredentialErrorDoesNotExposeRequestURL(t *testing.T) {
	err := fmt.Errorf("request: %w", &url.Error{
		Op:  "Post",
		URL: "https://example.invalid/method?anonymous_token=fixture&secret=fixture",
		Err: context.Canceled,
	})
	got := redactCredentialRequestError(err)
	if !errors.Is(got, context.Canceled) {
		t.Fatalf("cancellation not preserved: %v", got)
	}
	if strings.Contains(got.Error(), "fixture") || strings.Contains(got.Error(), "example.invalid") {
		t.Fatal("error still exposes the request URL")
	}
}
