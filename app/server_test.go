package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"searchan/platform"
	"searchan/reader"
	"searchan/render"
	"searchan/research"
	"searchan/search"
	"searchan/task"
)

type testProvider struct {
	calls       atomic.Int32
	started     chan struct{}
	block       bool
	snippetOnly bool
}

func (p *testProvider) Name() string { return "fixture" }
func (p *testProvider) Search(ctx context.Context, _ search.Request) (search.Response, error) {
	p.calls.Add(1)
	if p.block {
		close(p.started)
		<-ctx.Done()
		return search.Response{}, ctx.Err()
	}
	if p.snippetOnly {
		return search.Response{Results: []search.Result{{URL: "https://go.dev/doc/", Title: "Go context", Snippet: "Go context cancellation propagates to workers."}}}, nil
	}
	return search.Response{Results: []search.Result{{URL: "https://go.dev/doc/", Title: "Go context", Content: strings.Repeat("Go context cancellation propagates to workers. ", 5), ContentProvider: "fixture"}}}, nil
}

type unusedReader struct{}

func (unusedReader) Read(context.Context, string) (reader.Document, error) {
	return reader.Document{}, errors.New("unexpected read")
}

type testRenderer struct{ fail bool }

func (r testRenderer) Render(context.Context, string, research.Output, string) ([]render.Artifact, error) {
	if r.fail {
		return nil, errors.New("compiler unavailable")
	}
	return []render.Artifact{{Path: "fixture.png", Width: 1000, Height: 1600}}, nil
}

type testSender struct {
	mu         sync.Mutex
	texts      []string
	images     int
	imageError error
	textError  error
}

func (s *testSender) SendText(_ context.Context, _ platform.Target, text string, seq int) (platform.Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.texts = append(s.texts, text)
	return platform.Receipt{MessageID: fmt.Sprint(seq)}, s.textError
}
func (s *testSender) SendImage(_ context.Context, _ platform.Target, _ render.Artifact, _ string, seq int) (platform.Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.images++
	return platform.Receipt{MessageID: fmt.Sprint(seq)}, s.imageError
}
func setup(t *testing.T, p *testProvider, sender *testSender, renderer testRenderer) (*Server, platform.Event) {
	t.Helper()
	e, err := research.New(research.Config{Providers: []search.Provider{p}, Reader: unusedReader{}, Model: testModel{}, MaxSearchCalls: 1})
	if err != nil {
		t.Fatal(err)
	}
	r := &Runtime{Config: Config{DataDir: t.TempDir(), QueueSize: 4, Timeout: 5 * time.Second, ResearchTimeout: 3 * time.Second, AllowPublic: true}, Engine: e, Renderer: renderer, Senders: map[string]platform.Sender{"telegram": sender}}
	s, err := NewServer(r)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, platform.Event{ID: "event1", Text: "/search Go context cancellation", Target: platform.Target{Platform: "telegram", ChatID: "chat", UserID: "user", MessageID: "message"}}
}
func TestPipelineDeliveryAndFallback(t *testing.T) {
	for _, tt := range []struct {
		name          string
		renderFail    bool
		imageError    error
		state         string
		images, texts int
	}{
		{"image", false, nil, "completed", 1, 2},
		{"render-fallback", true, nil, "completed", 0, 2},
		{"rejected-image-fallback", false, &platform.RejectedError{Code: 400}, "completed", 1, 2},
		{"ambiguous-image-no-retry", false, context.DeadlineExceeded, "uncertain", 1, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := &testProvider{}
			sender := &testSender{imageError: tt.imageError}
			s, event := setup(t, p, sender, testRenderer{tt.renderFail})
			if err := s.Accept(context.Background(), event); err != nil {
				t.Fatal(err)
			}
			rec, ok, err := s.Store.Claim()
			if err != nil || !ok {
				t.Fatal(err)
			}
			s.process(context.Background(), rec)
			stored, err := s.Store.Get(rec.ID)
			if err != nil {
				t.Fatal(err)
			}
			if stored.State != tt.state || sender.images != tt.images || len(sender.texts) != tt.texts || p.calls.Load() != 1 {
				t.Fatalf("state=%s images=%d texts=%d calls=%d", stored.State, sender.images, len(sender.texts), p.calls.Load())
			}
			if stored.Output == nil || len(stored.Output.Sources) == 0 {
				t.Fatal("missing persisted evidence")
			}
			if _, created, err := s.Store.Submit(event, rec.Query); err != nil || created {
				t.Fatal("event was not deduplicated")
			}
			if stored.State == "uncertain" {
				s.Store.Close()
				recovered, err := task.Open(s.Store.Dir, 4)
				if err != nil {
					t.Fatal(err)
				}
				defer recovered.Close()
				if _, ok, err := recovered.Claim(); err != nil || ok {
					t.Fatal("ambiguous message automatically repeated")
				}
			}
		})
	}
}
func TestCancelStopsProviderAndCannotBeUndone(t *testing.T) {
	p := &testProvider{block: true, started: make(chan struct{})}
	s, event := setup(t, p, &testSender{}, testRenderer{})
	if err := s.Accept(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	rec, _, _ := s.Store.Claim()
	done := make(chan struct{})
	go func() { defer close(done); s.process(context.Background(), rec) }()
	select {
	case <-p.started:
	case <-time.After(time.Second):
		t.Fatal("search never started")
	}
	event.ID = "cancel"
	event.Text = "/cancel " + rec.ID
	if err := s.Accept(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancel did not propagate")
	}
	stored, _ := s.Store.Get(rec.ID)
	if stored.State != "cancelled" {
		t.Fatalf("cancellation overwritten: %s", stored.State)
	}
}
func TestConfigurationBounds(t *testing.T) {
	for _, env := range []map[string]string{{"SEARCHAN_WORKERS": "100"}, {"SEARCHAN_QUEUE_SIZE": "0"}, {"SEARCHAN_MAX_PAGES": "100"}, {"SEARCHAN_PROVIDERS": "ddg,ddg"}, {"SEARCHAN_RESEARCH_TIMEOUT": "2m"}, {"SEARCHAN_MODEL_VERIFY": "invalid"}} {
		if _, err := Load(func(k string) string { return env[k] }); err == nil {
			t.Fatalf("unbounded configuration accepted: %v", env)
		}
	}
	c, err := Load(func(string) string { return "" })
	if err != nil || c.Workers != 2 || c.QueueSize != 32 {
		t.Fatalf("bad defaults: %+v %v", c, err)
	}
}

func TestRecoveryAfterFailureReplyDoesNotRepeatSearch(t *testing.T) {
	p := &testProvider{}
	s, event := setup(t, p, &testSender{}, testRenderer{})
	r, _, err := s.Store.Submit(event, "Go context")
	if err != nil {
		t.Fatal(err)
	}
	r.State = "ready"
	r.Error = "previous research failed"
	r.Deliveries = []task.Delivery{{Kind: "accepted", State: "sent"}, {Kind: "failure", State: "sent"}}
	if err := s.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	s.process(context.Background(), r)
	stored, _ := s.Store.Get(r.ID)
	if p.calls.Load() != 0 || stored.State != "failed" {
		t.Fatalf("repeated failed research: state=%s calls=%d", stored.State, p.calls.Load())
	}
}

// testModel supplies a cited AI answer; tests no longer depend on an extractive fallback.
type testModel struct{ err error }

func (m testModel) Complete(_ context.Context, _ string, _ any, output any) error {
	if m.err != nil {
		return m.err
	}
	*output.(*research.Answer) = research.Answer{Title: "Go context", Lead: research.Claim{Text: "Workers observe cancellation.", EvidenceIDs: []string{"e1"}}}
	return nil
}

type testModelFunc func(context.Context, string, any, any) error

func (f testModelFunc) Complete(ctx context.Context, prompt string, input, output any) error {
	return f(ctx, prompt, input, output)
}

func TestAIAnswerPolicyDelivery(t *testing.T) {
	for _, name := range []string{"missing-model", "generation-failure", "invalid-citations", "snippet-image", "snippet-text", "snippet-repair-image", "verification-image"} {
		t.Run(name, func(t *testing.T) {
			p := &testProvider{snippetOnly: strings.HasPrefix(name, "snippet-")}
			sender := &testSender{}
			s, event := setup(t, p, sender, testRenderer{fail: name == "snippet-text"})
			cfg := research.Config{Providers: []search.Provider{p}, Reader: unusedReader{}, Model: testModel{}, MaxSearchCalls: 1}
			wantFailure := ""
			switch name {
			case "missing-model":
				cfg.Model = nil
				wantFailure = "未配置模型"
			case "generation-failure":
				cfg.Model = testModel{err: errors.New("private-upstream-credential")}
				wantFailure = "AI 总结生成失败"
			case "invalid-citations":
				cfg.Model = testModelFunc(func(_ context.Context, _ string, _ any, output any) error {
					*output.(*research.Answer) = research.Answer{Title: "Bad", Lead: research.Claim{Text: "Unsupported", EvidenceIDs: []string{"invented"}}}
					return nil
				})
				wantFailure = "未通过格式或引用校验"
			case "verification-image":
				cfg.VerifyWithModel = true
				cfg.Model = testModelFunc(func(ctx context.Context, prompt string, input, output any) error {
					if strings.HasPrefix(prompt, "核对 answer") {
						return json.Unmarshal([]byte(`{"unsupported":["bad assertion"],"missing":[]}`), output)
					}
					return (testModel{}).Complete(ctx, prompt, input, output)
				})
			case "snippet-repair-image":
				calls := 0
				cfg.Model = testModelFunc(func(ctx context.Context, prompt string, input, output any) error {
					calls++
					if calls == 1 {
						*output.(*research.Answer) = research.Answer{Title: "Brief", Lead: research.Claim{Text: strings.Repeat("字", 301), EvidenceIDs: []string{"e1"}}}
						return nil
					}
					if calls != 2 || input.(map[string]any)["brief_correction"] == nil {
						t.Fatal("invalid repair sequence")
					}
					return (testModel{}).Complete(ctx, prompt, input, output)
				})
				if binary := os.Getenv("SEARCHAN_TEST_TYPST"); binary != "" {
					s.Runtime.Renderer = render.New(binary, 1)
				}
			}
			e, err := research.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			s.Runtime.Engine = e
			if err := s.Accept(context.Background(), event); err != nil {
				t.Fatal(err)
			}
			rec, ok, err := s.Store.Claim()
			if err != nil || !ok {
				t.Fatal("no queued task", err)
			}
			s.process(context.Background(), rec)
			stored, err := s.Store.Get(rec.ID)
			if err != nil {
				t.Fatal(err)
			}
			if wantFailure != "" {
				if stored.State != "failed" || stored.Output != nil || sender.images != 0 || len(sender.texts) != 2 || !strings.Contains(sender.texts[1], wantFailure) || strings.Contains(sender.texts[1], "private-upstream") {
					t.Fatalf("failed AI request delivered output: %+v texts=%v images=%d", stored, sender.texts, sender.images)
				}
				if name == "missing-model" && p.calls.Load() != 0 {
					t.Fatal("searched despite missing model")
				}
				return
			}
			if stored.State != "completed" || stored.Output == nil || stored.Output.AnswerMode != "brief_summary" || len(stored.Output.Sources) == 0 || !strings.Contains(stored.Output.Text(), "关键事实待确认") {
				t.Fatalf("short summary not delivered: %+v", stored)
			}
			if name == "snippet-repair-image" && (stored.Output.Stats.AnswerRepairs != 1 || stored.Output.Stats.ModelCalls != 2) {
				t.Fatal("layout repair was not recorded")
			}
			if name == "snippet-text" {
				if sender.images != 0 || len(sender.texts) != 2 || !strings.Contains(sender.texts[1], "未读取原文") {
					t.Fatalf("short text fallback missing notice: %v", sender.texts)
				}
			} else if sender.images != 1 || len(sender.texts) != 2 {
				t.Fatal("short summary did not use image delivery")
			}
		})
	}
}

func TestAcceptRepliesInsteadOfFailingWebhook(t *testing.T) {
	sender := &testSender{}
	s, event := setup(t, &testProvider{}, sender, testRenderer{})
	s.Runtime.Config.MaxUserTasks, s.Runtime.Config.MaxChatTasks = 1, 4
	if err := s.Accept(context.Background(), event); err != nil || s.Store.Pending() != 1 {
		t.Fatal("first search not queued", err)
	}
	second := event
	second.ID, second.Target.MessageID = "event2", "message2"
	s.last = map[string]time.Time{}
	// Quota is a user-facing answer; a webhook error would trigger redelivery.
	if err := s.Accept(context.Background(), second); err != nil || s.Store.Pending() != 1 || len(sender.texts) != 1 || !strings.Contains(sender.texts[0], "进行中") {
		t.Fatalf("quota not answered: %v %q", err, sender.texts)
	}
	s.last = map[string]time.Time{}
	if err := s.Accept(context.Background(), second); err != nil || len(sender.texts) != 1 {
		t.Fatalf("redelivered quota event replied twice: %v %q", err, sender.texts)
	}
	for i, text := range []string{"/search", "/status", "/status 0123456789abcdef0123456789abcdef"} {
		s.last = map[string]time.Time{}
		e := event
		e.ID, e.Text = fmt.Sprint("usage", i), text
		if err := s.Accept(context.Background(), e); err != nil || len(sender.texts) != 2+i {
			t.Fatalf("%s not answered: %v %q", text, err, sender.texts)
		}
	}
}

func TestAddressedPlainTextSearches(t *testing.T) {
	for _, addressed := range []bool{false, true} {
		s, event := setup(t, &testProvider{}, &testSender{}, testRenderer{})
		event.Text, event.Addressed = "Go context cancellation", addressed
		if err := s.Accept(context.Background(), event); err != nil {
			t.Fatal(err)
		}
		want := 0
		if addressed {
			want = 1
		}
		if s.Store.Pending() != want {
			t.Fatalf("addressed=%t queued %d", addressed, s.Store.Pending())
		}
	}
	for _, tt := range []struct {
		event platform.Event
		ok    bool
	}{
		{platform.Event{Text: "<@!1> SpringData简介", Addressed: true}, true},
		{platform.Event{Text: "/unknown x", Addressed: true}, false},
		{platform.Event{Text: "  ", Addressed: true}, false},
		{platform.Event{Text: "/deep x", Addressed: true}, true},
	} {
		if cmd, ok := platform.ParseRequest(tt.event); ok != tt.ok || (ok && cmd.Argument == "") {
			t.Fatalf("unexpected parse %+v: %+v %t", tt.event, cmd, ok)
		}
	}
}

type panicRenderer struct{}

func (panicRenderer) Render(context.Context, string, research.Output, string) ([]render.Artifact, error) {
	panic("renderer bug")
}

func TestTaskPanicFailsOnlyThatTask(t *testing.T) {
	s, event := setup(t, &testProvider{}, &testSender{}, testRenderer{})
	s.Runtime.Renderer = panicRenderer{}
	if err := s.Accept(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	rec, ok, err := s.Store.Claim()
	if err != nil || !ok {
		t.Fatal(err)
	}
	s.processSafely(context.Background(), rec)
	stored, err := s.Store.Get(rec.ID)
	if err != nil || stored.State != "failed" || stored.Error != "internal error" {
		t.Fatalf("panic not contained: %+v %v", stored, err)
	}
}
