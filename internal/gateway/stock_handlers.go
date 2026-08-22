package gateway

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (s *Service) handleStockQuotes(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requestAccountID(r); !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	symbols := strings.Split(r.URL.Query().Get("symbols"), ",")
	quotes, err := s.Stocks.Quotes(r.Context(), symbols)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, errInvalidStockRequest) {
			status = http.StatusBadRequest
		}
		writeErr(w, status, err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"quotes":     quotes,
		"fetched_at": time.Now().UTC().Format(time.RFC3339),
	})
}

func (s *Service) handleStockSearch(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requestAccountID(r); !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	results, err := s.Stocks.Search(r.Context(), r.URL.Query().Get("q"))
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, errInvalidStockRequest) {
			status = http.StatusBadRequest
		}
		writeErr(w, status, err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

func (s *Service) handleStockSentiment(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requestAccountID(r); !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	if s.Sentiment == nil {
		writeErr(w, http.StatusServiceUnavailable, "stock sentiment service is unavailable")
		return
	}
	result, err := s.Sentiment.Analyze(r.Context(), strings.Split(r.URL.Query().Get("symbols"), ","))
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, errInvalidStockRequest) {
			status = http.StatusBadRequest
		}
		writeErr(w, status, err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, result)
}

func (s *Service) handleStockNewsSentiment(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requestAccountID(r); !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	if s.NewsSentiment == nil {
		writeErr(w, http.StatusServiceUnavailable, "stock news sentiment service is unavailable")
		return
	}
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	result, err := s.NewsSentiment.Latest(
		r.Context(), r.URL.Query().Get("symbol"), r.URL.Query().Get("name"), days, limit,
	)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, errInvalidStockRequest) {
			status = http.StatusBadRequest
		}
		writeErr(w, status, err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, result)
}
