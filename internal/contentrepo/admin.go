package contentrepo

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
)

// RegisterAdmin adds certificate administration (the equivalent of the
// CSADMIN "Certificates" tab) to the admin listener:
//
//	GET  /admin/contentserver/certificates
//	POST /admin/contentserver/certificates/activate    {"contRep":"Z1","authId":"CN=S4H"}
//	POST /admin/contentserver/certificates/deactivate  {"contRep":"Z1","authId":"CN=S4H"}
//
// When token is non-empty, requests need "Authorization: Bearer <token>".
func (s *Server) RegisterAdmin(mux *http.ServeMux, token string) {
	guard := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if token != "" {
				got := []byte(r.Header.Get("Authorization"))
				if subtle.ConstantTimeCompare(got, []byte("Bearer "+token)) != 1 {
					http.Error(w, "unauthorized", http.StatusUnauthorized)
					return
				}
			}
			h(w, r)
		}
	}
	mux.HandleFunc("GET /admin/contentserver/certificates", guard(func(w http.ResponseWriter, r *http.Request) {
		if err := s.certs.reload(r.Context(), 0); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		writeAdminJSON(w, http.StatusOK, map[string]any{"value": s.certs.list()})
	}))
	set := func(active bool) http.HandlerFunc {
		return guard(func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				ContRep string `json:"contRep"`
				AuthID  string `json:"authId"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil || body.ContRep == "" || body.AuthID == "" {
				http.Error(w, `body must be {"contRep":"..","authId":".."}`, http.StatusBadRequest)
				return
			}
			rec, err := s.certs.setActive(r.Context(), body.ContRep, body.AuthID, active)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			s.log.Info("content server certificate state changed", "contRep", rec.ContRep, "authId", rec.AuthID, "active", active, "fingerprint", rec.Fingerprint)
			out := *rec
			out.CertDER = nil
			writeAdminJSON(w, http.StatusOK, out)
		})
	}
	mux.HandleFunc("POST /admin/contentserver/certificates/activate", set(true))
	mux.HandleFunc("POST /admin/contentserver/certificates/deactivate", set(false))
}

func writeAdminJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
