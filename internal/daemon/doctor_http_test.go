package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
)

func TestDoctorHTTPRejectsInvalidTargetsAndBodies(t *testing.T) {
	h := handler(&service{}, v1.DaemonStatusResponse{}, func() { t.Fatal("doctor stopped daemon") })
	for _, body := range []string{`{"api_version":"wrong"}`, `{"api_version":"v1","instance_id":"invalid"}`, `{"api_version":"v1","scene":"dev"}`, `{"api_version":"v1","config_path":"/machine.yaml"}`, `{"api_version":"v1","unknown":true}`, `{"api_version":"v1"} {}`, `{"api_version":"v1","probe_public_ca":"true"}`} {
		t.Run(body, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/v1/doctor", strings.NewReader(body))
			r.Header.Set("X-Backlot-API-Version", v1.Version)
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code == http.StatusOK {
				t.Fatal("accepted invalid doctor contract", body, w.Body.String())
			}
			var envelope v1.ErrorResponse
			if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil || envelope.Error.Message == "" {
				t.Fatal("invalid error envelope", err, w.Body.String())
			}
		})
	}
}
