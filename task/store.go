// Package task provides a bounded single-process durable work queue and outbox.
// Payloads live in atomic JSON files so no database runtime or unbounded task
// index is resident in memory.
package task

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"searchan/internal/fsutil"
	"searchan/internal/httpx"
	"searchan/platform"
	"searchan/render"
	"searchan/research"
)

var validID = regexp.MustCompile(`^[a-f0-9]{32}$`)
var ErrBusy = errors.New("task queue is full")
var ErrQuota = errors.New("active task quota exceeded")
var ErrNotCancellable = errors.New("task can no longer be cancelled")

type Delivery struct {
	Kind    string `json:"kind"`
	Page    int    `json:"page"`
	State   string `json:"state"`
	Receipt string `json:"receipt,omitempty"`
}
type Record struct {
	ID         string             `json:"id"`
	Query      string             `json:"query"`
	Deep       bool               `json:"deep,omitempty"`
	Target     platform.Target    `json:"target"`
	State      string             `json:"state"`
	Created    time.Time          `json:"created"`
	Updated    time.Time          `json:"updated"`
	Output     *research.Output   `json:"output,omitempty"`
	Artifacts  []render.Artifact  `json:"artifacts,omitempty"`
	Deliveries []Delivery         `json:"deliveries,omitempty"`
	Error      string             `json:"error,omitempty"`
	Failure    *FailureDiagnostic `json:"failure,omitempty"`
}

// FailureDiagnostic retains only counts and fixed search failure categories.
// A partial research result must never occupy Output, which signals resumable
// completed research to workers after a restart.
type FailureDiagnostic struct {
	SearchCalls    int            `json:"search_calls"`
	PagesRead      int            `json:"pages_read"`
	ModelCalls     int            `json:"model_calls"`
	SearchFailures map[string]int `json:"search_failures,omitempty"`
}

type Store struct {
	Dir        string
	Capacity   int
	mu         sync.Mutex
	pending    []string
	fileLock   *os.File
	closed     bool
	live       map[string]platform.Target
	storageErr error
}

func Open(dir string, capacity int) (*Store, error) {
	if capacity < 1 || capacity > 256 {
		return nil, errors.New("queue capacity must be 1-256")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0700); err != nil {
		return nil, err
	}
	lock, err := lockDir(abs)
	if err != nil {
		return nil, err
	}
	s := &Store{Dir: abs, Capacity: capacity, fileLock: lock, pending: make([]string, 0, capacity), live: map[string]platform.Target{}}
	err = fsutil.WalkDirs(abs, func(id string) error {
		if !validID.MatchString(id) {
			return nil
		}
		rec, err := s.get(id)
		if os.IsNotExist(err) {
			return os.RemoveAll(s.Path(id))
		}
		if err != nil {
			return err
		}
		switch rec.State {
		case "running":
			if rec.Output != nil {
				rec.State = "ready"
				if err := s.save(rec); err != nil {
					return err
				}
				if len(s.pending) < s.Capacity {
					s.pending = append(s.pending, id)
					return nil
				}
			}
			// A crash during unknown remote work must not automatically repeat paid
			// requests. Completed research is persisted before render/send.
			rec.State = "failed"
			rec.Error = "service interrupted while processing; submit a new request"
			return s.save(rec)
		case "sending":
			rec.State = "uncertain"
			rec.Error = "interrupted while sending; acceptance is unknown"
			return s.save(rec)
		case "queued", "ready":
			if len(s.pending) < s.Capacity {
				s.pending = append(s.pending, id)
			} else {
				rec.State = "failed"
				rec.Error = "queue capacity reduced during restart"
				return s.save(rec)
			}
		}
		return nil
	})
	if err != nil {
		s.Close()
		return nil, err
	}
	for _, id := range s.pending {
		r, err := s.get(id)
		if err != nil {
			s.Close()
			return nil, err
		}
		s.live[id] = r.Target
	}
	return s, nil
}
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.fileLock != nil {
		return s.fileLock.Close()
	}
	return nil
}
func (s *Store) Path(id string) string { return filepath.Join(s.Dir, id) }
func (s *Store) get(id string) (Record, error) {
	if !validID.MatchString(id) {
		return Record{}, errors.New("invalid task ID")
	}
	f, err := os.Open(filepath.Join(s.Dir, id, "task.json"))
	if err != nil {
		return Record{}, err
	}
	defer f.Close()
	var record Record
	err = httpx.Decode(f, 512<<10, &record)
	return record, err
}
func (s *Store) Get(id string) (Record, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.get(id) }
func (s *Store) save(r Record) error {
	if s.closed {
		return errors.New("task store is closed")
	}
	r.Updated = time.Now().UTC()
	err := fsutil.WriteJSONLimit(filepath.Join(s.Dir, r.ID, "task.json"), r, 512<<10)
	s.storageErr = err
	return err
}
func (s *Store) Save(r Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validID.MatchString(r.ID) {
		return errors.New("invalid task ID")
	}
	// A worker's stale record cannot undo a cancellation.
	current, err := s.get(r.ID)
	if err != nil {
		return err
	}
	if current.State == "cancelled" && r.State != "cancelled" {
		return errors.New("task was cancelled")
	}
	if err := s.save(r); err != nil {
		return err
	}
	switch r.State {
	case "completed", "failed", "uncertain", "cancelled":
		delete(s.live, r.ID)
	}
	return nil
}
func (s *Store) Submit(event platform.Event, query string) (Record, bool, error) {
	return s.SubmitLimited(event, query, 0, 0)
}
func (s *Store) SubmitLimited(event platform.Event, query string, perUser, perChat int) (Record, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := eventID(event)
	if rec, err := s.get(id); err == nil {
		// Rename may have succeeded before the previous directory fsync failed.
		// A redelivered webhook must recover that queued record without waiting
		// for a process restart or launching a duplicate task.
		if _, live := s.live[id]; rec.State == "queued" && !live {
			if len(s.pending) >= s.Capacity {
				return Record{}, false, ErrBusy
			}
			if err := s.checkQuota(event.Target, perUser, perChat); err != nil {
				return Record{}, false, err
			}
			if err := s.save(rec); err != nil {
				return Record{}, false, err
			}
			s.pending = append(s.pending, id)
			s.live[id] = rec.Target
		}
		return rec, false, nil
	} else if !os.IsNotExist(err) {
		return Record{}, false, err
	}
	if len(s.pending) >= s.Capacity {
		return Record{}, false, ErrBusy
	}
	if err := s.checkQuota(event.Target, perUser, perChat); err != nil {
		return Record{}, false, err
	}
	r := Record{ID: id, Query: query, Target: event.Target, State: "queued", Created: time.Now().UTC()}
	cmd, _ := platform.ParseCommand(event.Text)
	r.Deep = cmd.Name == "deep"
	if err := s.save(r); err != nil {
		return Record{}, false, err
	}
	s.pending = append(s.pending, id)
	s.live[id] = event.Target
	return r, true, nil
}
func (s *Store) checkQuota(request platform.Target, perUser, perChat int) error {
	userCount, chatCount := 0, 0
	for _, target := range s.live {
		if target.Platform != request.Platform {
			continue
		}
		if target.UserID == request.UserID {
			userCount++
		}
		if target.ChatID == request.ChatID && target.Scope == request.Scope {
			chatCount++
		}
	}
	if perUser > 0 && userCount >= perUser || perChat > 0 && chatCount >= perChat {
		return ErrQuota
	}
	return nil
}
func (s *Store) Claim() (Record, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.pending) > 0 {
		id := s.pending[0]
		r, err := s.get(id)
		if err != nil {
			s.storageErr = err
			return r, false, err
		}
		if r.State == "cancelled" {
			s.pop()
			delete(s.live, id)
			continue
		}
		r.State = "running"
		if err := s.save(r); err != nil {
			return r, false, err
		}
		s.pop()
		return r, true, nil
	}
	return Record{}, false, nil
}
func (s *Store) Cancel(id string, target platform.Target) error {
	_, err := s.CancelOnce(id, target)
	return err
}

// CancelOnce reports whether this call changed active work into a cancelled
// task. Repeated cancellation is a read-only success, without refreshing the
// retention timestamp or granting another priority confirmation.
func (s *Store) CancelOnce(id string, target platform.Target) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.get(id)
	if err != nil {
		return false, err
	}
	if !SameTarget(r.Target, target) || r.Target.UserID != target.UserID {
		return false, errors.New("task is not owned by this user")
	}
	switch r.State {
	case "cancelled":
		// A rename may have succeeded before a prior fsync/bookkeeping error.
		// Reconcile memory without rewriting the durable cancellation.
		s.releaseActive(id)
		return false, nil
	case "queued", "running", "ready":
	case "sending":
		// Cancelling a progress acknowledgement stops subsequent paid work.
		// A final report/image/text or failure reply already in flight retains
		// the conservative unknown-delivery rules instead.
		if len(r.Deliveries) == 0 {
			return false, ErrNotCancellable
		}
		last := r.Deliveries[len(r.Deliveries)-1]
		if last.Kind != "accepted" || last.State != "sending" {
			return false, ErrNotCancellable
		}
	default:
		return false, ErrNotCancellable
	}
	if r.Failure != nil || r.Output == nil && r.Error != "" {
		return false, ErrNotCancellable
	}
	for _, d := range r.Deliveries {
		if d.State == "uncertain" || d.State == "sending" && d.Kind != "accepted" {
			return false, ErrNotCancellable
		}
	}
	r.State = "cancelled"
	if err := s.save(r); err != nil {
		return false, err
	}
	s.releaseActive(id)
	return true, nil
}

func (s *Store) releaseActive(id string) {
	delete(s.live, id)
	for i, queued := range s.pending {
		if queued == id {
			copy(s.pending[i:], s.pending[i+1:])
			s.pending[len(s.pending)-1] = ""
			s.pending = s.pending[:len(s.pending)-1]
			break
		}
	}
}

func (s *Store) pop() {
	copy(s.pending, s.pending[1:])
	s.pending[len(s.pending)-1] = ""
	s.pending = s.pending[:len(s.pending)-1]
}

func eventID(event platform.Event) string {
	hash := sha256.Sum256([]byte(event.Target.Platform + "\x00" + event.Target.ChatID + "\x00" + event.ID))
	return hex.EncodeToString(hash[:16])
}

// BeginReply persists send intent for short command replies before contacting
// the platform. Repeated webhooks cannot repeat a reply with unknown outcome.
func (s *Store) BeginReply(event platform.Event) (Record, bool, error) {
	return s.recordReply(event, false)
}

// IgnoreEvent durably consumes a throttled control event without sending a
// reply. A later webhook retry must not become a fresh paid search. Existing
// search/reply records are returned untouched by the shared identity check.
func (s *Store) IgnoreEvent(event platform.Event) error {
	_, _, err := s.recordReply(event, true)
	return err
}

func (s *Store) recordReply(event platform.Event, quiet bool) (Record, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := eventID(event)
	if r, err := s.get(id); err == nil {
		return r, false, nil
	} else if !os.IsNotExist(err) {
		return Record{}, false, err
	}
	r := Record{ID: id, Target: event.Target, State: "sending", Created: time.Now().UTC(), Deliveries: []Delivery{{Kind: "control", State: "sending"}}}
	if quiet {
		r.State, r.Error, r.Deliveries = "completed", "event ignored by rate limit", nil
	}
	return r, true, s.save(r)
}

func (s *Store) Pending() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.pending) }

// Probe checks actual durable writes, not just directory existence. It does not
// create tasks, change the queue, or expose persisted user content.
func (s *Store) Probe() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("task store is closed")
	}
	if s.storageErr != nil {
		return s.storageErr
	}
	f, err := os.CreateTemp(s.Dir, ".probe-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write([]byte("ok"))
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}
func SameTarget(a, b platform.Target) bool {
	return a.Platform == b.Platform && a.ChatID == b.ChatID && a.Scope == b.Scope && a.ThreadID == b.ThreadID
}

// Prune deletes only terminal tasks older than retention. Call at startup or
// periodically; at most 64 directory entries are loaded at a time.
func (s *Store) Prune(retention time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return fsutil.WalkDirs(s.Dir, func(id string) error {
		if !validID.MatchString(id) {
			return nil
		}
		r, err := s.get(id)
		if err != nil {
			return err
		}
		switch r.State {
		case "completed", "failed", "cancelled":
			if time.Since(r.Updated) > retention {
				return os.RemoveAll(s.Path(id))
			}
		}
		return nil
	})
}
