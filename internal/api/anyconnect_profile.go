package api

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/proxy-panel/proxy-panel/internal/anyconnect"
)

// anyConnectProfile exports the exact profile.xml that the selected
// AnyConnect node advertises to Cisco Secure Client. The endpoint is
// authenticated by the parent API route, but is intentionally read-only so a
// browser download does not require a CSRF token.
func (s *Server) anyConnectProfile(w http.ResponseWriter, r *http.Request) {
	nodeID := chi.URLParam(r, "id")
	node, err := s.nodes.Get(r.Context(), nodeID)
	if err != nil || node.Type != "anyconnect" {
		notFound(w)
		return
	}
	data, err := anyconnect.RenderProfileXML(node)
	if err != nil {
		writeError(w, http.StatusConflict, "PROFILE_EXPORT_FAILED", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="anyconnect-profile.xml"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	if _, err = w.Write(data); err == nil {
		s.store.Audit(r.Context(), "anyconnect.profile.export", "node", node.ID, remoteIP(r), auditJSON(map[string]any{"name": node.Name}), true)
	}
}
