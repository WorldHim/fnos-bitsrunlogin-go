package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/Mmx233/BitSrunLoginGo/internal/config"
	"github.com/Mmx233/BitSrunLoginGo/internal/supervisor"
)

// versionResponse is returned by GET /api/version.
type versionResponse struct {
	Version    string `json:"version"`
	GoVersion  string `json:"go_version"`
	StartedAt  string `json:"started_at"`
	ConfigPath string `json:"config_path"`
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, versionResponse{
		Version:    s.opts.Version,
		GoVersion:  goVersion(),
		StartedAt:  s.startedAt.Format(time.RFC3339),
		ConfigPath: s.store.Path(),
	})
}

// statusResponse combines the loop state with a summary of the effective
// configuration, which is what the overview page shows.
type statusResponse struct {
	Supervisor supervisor.Status `json:"supervisor"`
	Guardian   bool              `json:"guardian_enabled"`
	Interval   uint              `json:"interval_seconds"`
	Form       FormView          `json:"form"`
	DDNS       DdnsView          `json:"ddns"`
	Interfaces string            `json:"interfaces"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	st := s.sup.Status()

	// Read the global configuration directly: it is the one the login loop
	// uses, so it is authoritative even if the file changed underneath.
	resp := statusResponse{
		Supervisor: st,
	}
	if config.Settings != nil && config.Form != nil {
		resp.Guardian = config.Settings.Guardian.Enable
		resp.Interval = config.Settings.Guardian.Duration
		resp.Form = FormView{
			Domain:      config.Form.Domain,
			Username:    config.Form.Username,
			UserType:    config.Form.UserType,
			HasPassword: config.Form.Password != "",
		}
		resp.DDNS = DdnsView{
			Enable:   config.Settings.DDNS.Enable,
			TTL:      config.Settings.DDNS.TTL,
			Domain:   config.Settings.DDNS.Domain,
			Provider: config.Settings.DDNS.Provider,
		}
		resp.Interfaces = config.Settings.Basic.Interfaces
	}
	writeJSON(w, http.StatusOK, resp)
}

// serviceResponse reports the loop state for the start/stop endpoints.
type serviceResponse struct {
	State   supervisor.State `json:"state"`
	Message string           `json:"message,omitempty"`
}

func (s *Server) handleServiceState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, serviceResponse{State: s.sup.Status().State})
}

func (s *Server) handleServiceStart(w http.ResponseWriter, r *http.Request) {
	err := s.sup.Start()
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, serviceResponse{State: supervisor.StateRunning, Message: "started"})
	case errors.Is(err, supervisor.ErrAlreadyRunning):
		writeJSON(w, http.StatusOK, serviceResponse{State: supervisor.StateRunning, Message: "already running"})
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func (s *Server) handleServiceStop(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := contextWithTimeout(r, 15*time.Second)
	defer cancel()

	err := s.sup.Stop(ctx)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, serviceResponse{State: supervisor.StateStopped, Message: "stopped"})
	case errors.Is(err, supervisor.ErrStopTimeout):
		// The loop is marked stopped even though the in-flight login is still
		// finishing; telling the caller the truth beats hiding it.
		writeJSON(w, http.StatusAccepted, serviceResponse{
			State:   supervisor.StateStopped,
			Message: "stopped, but the current login attempt is still finishing",
		})
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func (s *Server) handleServiceRestart(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := contextWithTimeout(r, 15*time.Second)
	defer cancel()

	if err := s.sup.Stop(ctx); err != nil && !errors.Is(err, supervisor.ErrStopTimeout) {
		writeError(w, http.StatusInternalServerError, "stop: "+err.Error())
		return
	}
	if err := s.sup.Start(); err != nil && !errors.Is(err, supervisor.ErrAlreadyRunning) {
		writeError(w, http.StatusInternalServerError, "start: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, serviceResponse{State: supervisor.StateRunning, Message: "restarted"})
}

// loginResponse reports the outcome of a single manual login.
type loginResponse struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

func (s *Server) handleLoginNow(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := contextWithTimeout(r, 60*time.Second)
	defer cancel()

	if err := s.sup.RunOnce(ctx); err != nil {
		writeJSON(w, http.StatusOK, loginResponse{OK: false, Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, loginResponse{OK: true, Message: "login succeeded"})
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	conf, err := s.store.Load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, NewConfigView(conf))
}

// putConfigResponse tells the UI whether the running loop was restarted.
type putConfigResponse struct {
	OK        bool       `json:"ok"`
	Message   string     `json:"message"`
	Restarted bool       `json:"restarted"`
	Config    ConfigView `json:"config"`
}

func (s *Server) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}

	var patch ConfigPatch
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&patch); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}

	conf, err := s.store.Load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	patch.Apply(conf)

	if err := s.store.Save(conf); err != nil {
		if errors.Is(err, ErrInvalidConfig) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// The supervisor reads the interval every cycle, so a restart is only
	// needed to apply changes to the credentials of a currently sleeping loop
	// — and even then the next cycle picks them up. Restart only when the
	// loop is running and the change is one that must not wait, which here
	// means the guardian toggle or the interval.
	restarted := false
	if s.sup.Status().State == supervisor.StateRunning && s.opts.AutoStart {
		ctx, cancel := contextWithTimeout(r, 15*time.Second)
		defer cancel()
		if stopErr := s.sup.Stop(ctx); stopErr == nil || errors.Is(stopErr, supervisor.ErrStopTimeout) {
			if startErr := s.sup.Start(); startErr == nil {
				restarted = true
			}
		}
	}

	saved, err := s.store.Load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, putConfigResponse{
		OK:        true,
		Message:   "configuration saved",
		Restarted: restarted,
		Config:    NewConfigView(saved),
	})
}

// logsResponse is returned by GET /api/logs.
type logsResponse struct {
	Entries []logEntry `json:"entries"`
	LastSeq uint64     `json:"last_seq"`
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	limit, err := parseUintQuery(r, "limit", 200)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if limit > defaultLogCapacity {
		limit = defaultLogCapacity
	}

	after, err := parseUintQuery(r, "after", 0)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if after > 0 {
		writeJSON(w, http.StatusOK, logsResponse{
			Entries: s.logs.since(uint64(after), int(limit)),
			LastSeq: s.logs.newest(),
		})
		return
	}

	entries, last := s.logs.snapshot(int(limit))
	writeJSON(w, http.StatusOK, logsResponse{Entries: entries, LastSeq: last})
}
