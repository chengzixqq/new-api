package common

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetPageQueryClampsPage(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name     string
		query    string
		expected int
	}{
		{name: "missing", expected: 1},
		{name: "valid", query: "?p=7", expected: 7},
		{name: "zero", query: "?p=0", expected: 1},
		{name: "negative", query: "?p=-3", expected: 1},
		{name: "invalid", query: "?p=invalid", expected: 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodGet, "/api/items"+test.query, nil)

			pageInfo := GetPageQuery(ctx)

			require.NotNil(t, pageInfo)
			assert.Equal(t, test.expected, pageInfo.Page)
		})
	}
}

func TestGetPageQueryResolvesAndClampsPageSize(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name     string
		query    string
		expected int
	}{
		{name: "missing uses default", expected: ItemsPerPage},
		{name: "page size valid", query: "?page_size=25", expected: 25},
		{name: "page size minimum", query: "?page_size=1", expected: 1},
		{name: "page size negative clamps", query: "?page_size=-5", expected: 1},
		{name: "page size over maximum clamps", query: "?page_size=101", expected: 100},
		{name: "ps compatibility", query: "?ps=30", expected: 30},
		{name: "ps compatibility negative clamps", query: "?ps=-2", expected: 1},
		{name: "ps compatibility over maximum clamps", query: "?ps=1000", expected: 100},
		{name: "size compatibility", query: "?size=40", expected: 40},
		{name: "size compatibility negative clamps", query: "?size=-2", expected: 1},
		{name: "size compatibility over maximum clamps", query: "?size=1000", expected: 100},
		{name: "page size takes precedence", query: "?page_size=20&ps=30&size=40", expected: 20},
		{name: "zero page size falls back to ps", query: "?page_size=0&ps=30&size=40", expected: 30},
		{name: "invalid page size falls back to ps", query: "?page_size=invalid&ps=30", expected: 30},
		{name: "zero ps falls back to size", query: "?ps=0&size=40", expected: 40},
		{name: "all zero values use default", query: "?page_size=0&ps=0&size=0", expected: ItemsPerPage},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodGet, "/api/items"+test.query, nil)

			pageInfo := GetPageQuery(ctx)

			require.NotNil(t, pageInfo)
			assert.Equal(t, test.expected, pageInfo.PageSize)
		})
	}
}
