package server

import "net/http"

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/agent", s.getAgent)
	mux.HandleFunc("GET /api/sessions", s.listSessions)
	mux.HandleFunc("POST /api/sessions", s.createSession)
	mux.HandleFunc("GET /api/sessions/{id}", s.getSession)
	mux.HandleFunc("DELETE /api/sessions/{id}", s.deleteSession)
	mux.HandleFunc("POST /api/sessions/{id}/run", s.run)
	return mux
}

func (s *Server) getAgent(w http.ResponseWriter, _ *http.Request) {
	var modelName string
	if named, ok := s.agent.(interface{ ModelName() string }); ok {
		modelName = named.ModelName()
	}
	reply(w, http.StatusOK, struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Model       string `json:"model,omitempty"`
	}{s.agent.Name(), s.agent.Description(), modelName})
}
