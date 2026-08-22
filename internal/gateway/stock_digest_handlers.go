package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
)

func (s *Service) handleStockLatestDigest(w http.ResponseWriter, r *http.Request) {
	accountID, ok := s.requestAccountID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	if s.Digest == nil {
		writeErr(w, http.StatusServiceUnavailable, "stock digest service is unavailable")
		return
	}
	request := StockDigestRequest{IncludeSentiment: true}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&request); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	result, err := s.Digest.Latest(r.Context(), accountID, "api:"+newID("digest"), request)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, errInvalidStockDigest) || errors.Is(err, errInvalidStockRequest) {
			status = http.StatusBadRequest
		}
		writeErr(w, status, err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, result)
}
