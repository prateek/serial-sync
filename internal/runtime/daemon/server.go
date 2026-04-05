package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type SourceStatus struct {
	SourceID          string     `json:"source_id"`
	LastStartedAt     *time.Time `json:"last_started_at,omitempty"`
	LastFinishedAt    *time.Time `json:"last_finished_at,omitempty"`
	LastSuccessAt     *time.Time `json:"last_success_at,omitempty"`
	LastFailureAt     *time.Time `json:"last_failure_at,omitempty"`
	LastError         string     `json:"last_error,omitempty"`
	Successes         int        `json:"successes"`
	Failures          int        `json:"failures"`
	LeaseSkips        int        `json:"lease_skips"`
	LastSyncRunID     string     `json:"last_sync_run_id,omitempty"`
	LastPublishRunID  string     `json:"last_publish_run_id,omitempty"`
	LastDiscovered    int        `json:"last_discovered"`
	LastChanged       int        `json:"last_changed"`
	LastMaterialized  int        `json:"last_materialized"`
	LastPublished     int        `json:"last_published"`
	LastPublishFailed int        `json:"last_publish_failed"`
}

type StatusSnapshot struct {
	HolderID     string         `json:"holder_id"`
	StartedAt    time.Time      `json:"started_at"`
	PollInterval string         `json:"poll_interval"`
	Healthy      bool           `json:"healthy"`
	Sources      []SourceStatus `json:"sources"`
}

type State struct {
	mu           sync.Mutex
	holderID     string
	startedAt    time.Time
	pollInterval time.Duration
	sources      map[string]*SourceStatus
}

type Server struct {
	http *http.Server
}

func NewState(holderID string, pollInterval time.Duration, sourceIDs []string) *State {
	state := &State{
		holderID:     holderID,
		startedAt:    time.Now().UTC(),
		pollInterval: pollInterval,
		sources:      map[string]*SourceStatus{},
	}
	for _, sourceID := range sourceIDs {
		state.ensureSource(sourceID)
	}
	return state
}

func Start(ctx context.Context, addr string, state *State) (*Server, error) {
	if strings.TrimSpace(addr) == "" {
		return nil, nil
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	server := &http.Server{
		Addr:    addr,
		Handler: newHandler(state),
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			// The daemon loop will still log and surface the initial startup error.
		}
	}()
	return &Server{http: server}, nil
}

func (s *Server) Close() error {
	if s == nil || s.http == nil {
		return nil
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.http.Shutdown(shutdownCtx)
}

func (s *State) MarkRunStart(sourceID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	source := s.ensureSource(sourceID)
	now := time.Now().UTC()
	source.LastStartedAt = &now
}

func (s *State) MarkLeaseSkipped(sourceID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	source := s.ensureSource(sourceID)
	source.LeaseSkips++
}

func (s *State) MarkRunSuccess(sourceID, syncRunID, publishRunID string, discovered, changed, materialized, published, publishFailed int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	source := s.ensureSource(sourceID)
	now := time.Now().UTC()
	source.LastFinishedAt = &now
	source.LastSuccessAt = &now
	source.LastError = ""
	source.LastSyncRunID = syncRunID
	source.LastPublishRunID = publishRunID
	source.LastDiscovered = discovered
	source.LastChanged = changed
	source.LastMaterialized = materialized
	source.LastPublished = published
	source.LastPublishFailed = publishFailed
	source.Successes++
}

func (s *State) MarkRunFailure(sourceID string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	source := s.ensureSource(sourceID)
	now := time.Now().UTC()
	source.LastFinishedAt = &now
	source.LastFailureAt = &now
	if err != nil {
		source.LastError = err.Error()
	}
	source.Failures++
}

func (s *State) Healthy() bool {
	snapshot := s.Snapshot()
	if len(snapshot.Sources) == 0 {
		return true
	}
	for _, source := range snapshot.Sources {
		if source.LastFailureAt == nil {
			continue
		}
		if source.LastSuccessAt == nil || source.LastFailureAt.After(*source.LastSuccessAt) {
			return false
		}
	}
	return true
}

func (s *State) Snapshot() StatusSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	sources := make([]SourceStatus, 0, len(s.sources))
	for _, source := range s.sources {
		sources = append(sources, *source)
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].SourceID < sources[j].SourceID })
	return StatusSnapshot{
		HolderID:     s.holderID,
		StartedAt:    s.startedAt,
		PollInterval: s.pollInterval.String(),
		Healthy:      s.healthyLocked(),
		Sources:      sources,
	}
}

func (s *State) Metrics() string {
	snapshot := s.Snapshot()
	var builder strings.Builder
	builder.WriteString("# TYPE serial_sync_daemon_up gauge\n")
	builder.WriteString("serial_sync_daemon_up 1\n")
	builder.WriteString("# TYPE serial_sync_daemon_healthy gauge\n")
	builder.WriteString("serial_sync_daemon_healthy ")
	if snapshot.Healthy {
		builder.WriteString("1\n")
	} else {
		builder.WriteString("0\n")
	}
	for _, source := range snapshot.Sources {
		label := fmt.Sprintf("{source=%s}", strconv.Quote(source.SourceID))
		builder.WriteString("# TYPE serial_sync_daemon_source_success_total counter\n")
		builder.WriteString("serial_sync_daemon_source_success_total" + label + " " + strconv.Itoa(source.Successes) + "\n")
		builder.WriteString("# TYPE serial_sync_daemon_source_failure_total counter\n")
		builder.WriteString("serial_sync_daemon_source_failure_total" + label + " " + strconv.Itoa(source.Failures) + "\n")
		builder.WriteString("# TYPE serial_sync_daemon_source_lease_skip_total counter\n")
		builder.WriteString("serial_sync_daemon_source_lease_skip_total" + label + " " + strconv.Itoa(source.LeaseSkips) + "\n")
		builder.WriteString("# TYPE serial_sync_daemon_source_last_discovered gauge\n")
		builder.WriteString("serial_sync_daemon_source_last_discovered" + label + " " + strconv.Itoa(source.LastDiscovered) + "\n")
		builder.WriteString("# TYPE serial_sync_daemon_source_last_published gauge\n")
		builder.WriteString("serial_sync_daemon_source_last_published" + label + " " + strconv.Itoa(source.LastPublished) + "\n")
		builder.WriteString("# TYPE serial_sync_daemon_source_last_publish_failed gauge\n")
		builder.WriteString("serial_sync_daemon_source_last_publish_failed" + label + " " + strconv.Itoa(source.LastPublishFailed) + "\n")
		builder.WriteString("# TYPE serial_sync_daemon_source_last_success_unixtime gauge\n")
		builder.WriteString("serial_sync_daemon_source_last_success_unixtime" + label + " " + unixOrZero(source.LastSuccessAt) + "\n")
		builder.WriteString("# TYPE serial_sync_daemon_source_last_failure_unixtime gauge\n")
		builder.WriteString("serial_sync_daemon_source_last_failure_unixtime" + label + " " + unixOrZero(source.LastFailureAt) + "\n")
	}
	return builder.String()
}

func (s *State) ensureSource(sourceID string) *SourceStatus {
	source, ok := s.sources[sourceID]
	if ok {
		return source
	}
	source = &SourceStatus{SourceID: sourceID}
	s.sources[sourceID] = source
	return source
}

func (s *State) healthyLocked() bool {
	for _, source := range s.sources {
		if source.LastFailureAt == nil {
			continue
		}
		if source.LastSuccessAt == nil || source.LastFailureAt.After(*source.LastSuccessAt) {
			return false
		}
	}
	return true
}

func newHandler(state *State) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		snapshot := state.Snapshot()
		if !snapshot.Healthy {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(snapshot)
	})
	mux.HandleFunc("/status", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(state.Snapshot())
	})
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = w.Write([]byte(state.Metrics()))
	})
	return mux
}

func unixOrZero(value *time.Time) string {
	if value == nil {
		return "0"
	}
	return strconv.FormatInt(value.UTC().Unix(), 10)
}
