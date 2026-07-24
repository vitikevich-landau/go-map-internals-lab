package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go-map-internals-lab/internal/lab"
)

// TestIndexAndAPI проверяет два внешних контракта приложения:
// встроенная главная страница доступна, а API принимает предметную операцию и
// возвращает снимок выбранной модели.
func TestIndexAndAPI(t *testing.T) {
	handler := New()

	t.Run("главная страница", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		response := httptest.NewRecorder()

		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK ||
			!strings.Contains(response.Body.String(), "Go Map Lab") {
			t.Fatalf(
				"неожиданный ответ главной страницы: status=%d body=%q",
				response.Code,
				response.Body.String(),
			)
		}
	})

	t.Run("операция API", func(t *testing.T) {
		body, err := json.Marshal(request{
			Mode:  lab.ModeSwiss,
			Kind:  lab.OperationInsert,
			Key:   1,
			Value: 10,
		})
		if err != nil {
			t.Fatalf("не удалось подготовить JSON-запрос: %v", err)
		}

		request := httptest.NewRequest(
			http.MethodPost,
			"/api/operation",
			bytes.NewReader(body),
		)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()

		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf(
				"API вернул status=%d body=%q",
				response.Code,
				response.Body.String(),
			)
		}
		if !strings.Contains(response.Body.String(), `"mode":"swiss"`) {
			t.Fatalf("API вернул неожиданный JSON: %q", response.Body.String())
		}
	})
}
