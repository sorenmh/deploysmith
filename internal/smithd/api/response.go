package api

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/sorenmh/deploysmith/internal/smithd/service"
)

// ErrorResponse represents an API error response
type ErrorResponse struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// writeJSON writes a JSON response
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
	}
}

// writeError writes an error response
func writeError(w http.ResponseWriter, status int, code, message string) {
	resp := ErrorResponse{}
	resp.Error.Code = code
	resp.Error.Message = message
	writeJSON(w, status, resp)
}

// writeServiceError writes the response for an error returned by the service
// layer: request errors map to 4xx, anything else is logged and reported as
// a 500 with the given fallback message
func writeServiceError(w http.ResponseWriter, err error, fallback string) {
	if svcErr, ok := service.AsError(err); ok {
		status := http.StatusBadRequest
		switch svcErr.Kind {
		case service.KindNotFound:
			status = http.StatusNotFound
		case service.KindConflict:
			status = http.StatusConflict
		case service.KindForbidden:
			status = http.StatusForbidden
		}
		writeError(w, status, svcErr.Code, svcErr.Message)
		return
	}

	log.Printf("%s: %v", fallback, err)
	writeError(w, http.StatusInternalServerError, "internal_error", fallback)
}
