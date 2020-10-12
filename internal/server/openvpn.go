package server

import (
	"encoding/json"
	"net/http"
)

func (s *server) handleGetPortForwarded(w http.ResponseWriter) {
	port := s.openvpnLooper.GetPortForwarded()
	data, err := json.Marshal(struct {
		Port uint16 `json:"port"`
	}{port})
	if err != nil {
		s.logger.Warn(err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if _, err := w.Write(data); err != nil {
		s.logger.Warn(err)
		w.WriteHeader(http.StatusInternalServerError)
	}
}

func (s *server) handleGetOpenvpnSettings(w http.ResponseWriter) {
	settings := s.openvpnLooper.GetSettings()
	data, err := json.Marshal(settings)
	if err != nil {
		s.logger.Warn(err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if _, err := w.Write(data); err != nil {
		s.logger.Warn(err)
		w.WriteHeader(http.StatusInternalServerError)
	}
}
