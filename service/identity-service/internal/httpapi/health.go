package httpapi

import "net/http"

type healthResponse struct {
	Status  string `json:"status"`
	Service string `json:"service"`
}

func healthHandler(serviceName string) http.HandlerFunc {
	response := healthResponse{
		Status:  "ok",
		Service: serviceName,
	}

	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, response)
	}
}
