package pairing

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
)

const maxBody = 16 << 10

// Handler expón a API do servizo:
//
//	POST   /api/v1/pair           {user, otp, deviceID, deviceName} → {serverDeviceID, serverAddresses, token, syncthingOptions}
//	GET    /api/v1/devices        (Bearer token) → equipos do usuario
//	DELETE /api/v1/devices/{id}   (Bearer token) {otp} → revoga un equipo
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /api/v1/pair", func(w http.ResponseWriter, r *http.Request) {
		var req PairRequest
		if !readJSON(w, r, &req) {
			return
		}
		resp, err := s.Pair(r.Context(), req)
		if err != nil {
			log.Printf("pair user=%q device=%q: %v", req.User, req.DeviceID, err)
			writeErr(w, err)
			return
		}
		log.Printf("pair user=%q device=%q nome=%q: OK", req.User, req.DeviceID, req.DeviceName)
		writeJSON(w, http.StatusOK, resp)
	})
	mux.HandleFunc("GET /api/v1/devices", s.auth(func(w http.ResponseWriter, r *http.Request, c *Caller) {
		devs, err := s.ListDevices(r.Context(), c)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, devs)
	}))
	mux.HandleFunc("DELETE /api/v1/devices/{id}", s.auth(func(w http.ResponseWriter, r *http.Request, c *Caller) {
		var req struct {
			OTP string `json:"otp"`
		}
		if !readJSON(w, r, &req) {
			return
		}
		id := r.PathValue("id")
		if err := s.Revoke(r.Context(), c, id, req.OTP); err != nil {
			log.Printf("revoke user=%q device=%q: %v", c.User, id, err)
			writeErr(w, err)
			return
		}
		log.Printf("revoke user=%q device=%q (desde %q): OK", c.User, id, c.DeviceID)
		w.WriteHeader(http.StatusNoContent)
	}))
	return mux
}

// HookHandler expón a API interna para SFTPGo (xestor de eventos). Vai nun
// listener separado e protexida cun token compartido:
//
//	POST /internal/v1/sync   (Bearer hookToken) → 202; pide unha reconciliación
func (s *Service) HookHandler(hookToken string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /internal/v1/sync", func(w http.ResponseWriter, r *http.Request) {
		tok, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if hookToken == "" || subtle.ConstantTimeCompare([]byte(tok), []byte(hookToken)) != 1 {
			writeErr(w, ErrUnauthorized)
			return
		}
		s.RequestReconcile()
		w.WriteHeader(http.StatusAccepted)
	})
	return mux
}

func (s *Service) auth(h func(http.ResponseWriter, *http.Request, *Caller)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok {
			writeErr(w, ErrUnauthorized)
			return
		}
		c, err := s.Authenticate(r.Context(), tok)
		if err != nil {
			writeErr(w, err)
			return
		}
		h(w, r, c)
	}
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeErr(w, ErrBadRequest)
		return false
	}
	return true
}

func writeErr(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	msg := "erro interno"
	for e, c := range map[error]int{
		ErrBadOTP:       http.StatusUnauthorized,
		ErrUnauthorized: http.StatusUnauthorized,
		ErrLocked:       http.StatusTooManyRequests,
		ErrBadRequest:   http.StatusBadRequest,
		ErrDeviceLimit:  http.StatusConflict,
		ErrDeviceTaken:  http.StatusConflict,
		ErrNotFound:     http.StatusNotFound,
	} {
		if errors.Is(err, e) {
			code, msg = c, e.Error()
			break
		}
	}
	writeJSON(w, code, map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
