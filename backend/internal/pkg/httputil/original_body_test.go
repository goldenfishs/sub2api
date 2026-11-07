package httputil

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOriginalRequestBodySurvivesLenientNormalizationAndAdapterRead(t *testing.T) {
	raw := "{\"input\":\"line one\nline two\"}"
	req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(raw))
	normalized, err := ReadLenientJSONRequestBodyWithPrealloc(req, 1024)
	require.NoError(t, err)
	require.NotEqual(t, raw, string(normalized))
	require.Equal(t, raw, string(OriginalRequestBody(req)))
	req.Body = NewPrereadBody([]byte(`{"input":"rewritten"}`))
	_, err = ReadRequestBodyWithPrealloc(req)
	require.NoError(t, err)
	require.Equal(t, raw, string(OriginalRequestBody(req)))
}

func TestOriginalRequestBodyRetainsBudgetUntilRequestEnds(t *testing.T) {
	raw := "{\"input\":\"line one\nline two\"}"
	budget := NewBodyMemoryBudget(4096)
	req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(raw))
	ctx, release := WithRequestBodyMemoryBudget(req.Context(), budget)
	defer release()
	req = req.WithContext(ctx)
	normalized, err := ReadLenientJSONRequestBodyWithPrealloc(req, 1024)
	require.NoError(t, err)
	original := OriginalRequestBody(req)
	require.Equal(t, raw, string(original))
	require.Equal(t, int64(cap(original)+cap(normalized)), requestBodyBudgetUsed(budget))
	release()
	require.Zero(t, requestBodyBudgetUsed(budget))
}
