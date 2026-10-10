// Package app wires the reusable components into CLI and webhook services.
package app

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"searchan/internal/httpx"
	"searchan/model"
	"searchan/platform"
	"searchan/reader"
	"searchan/render"
	"searchan/research"
	"searchan/search"
)

type Config struct {
	DataDir                    string
	Listen                     string
	Providers                  []string
	Workers                    int
	QueueSize                  int
	Timeout                    time.Duration
	ResearchTimeout            time.Duration
	MaxSearchCalls             int
	MaxPages                   int
	SearchConcurrency          int
	ReadConcurrency            int
	MaxEvidence                int
	PlanWithModel              bool
	VerifyWithModel            bool
	SearchTimeout              time.Duration
	ReadTimeout                time.Duration
	PlanningTimeout            time.Duration
	ModelTimeout               time.Duration
	ShutdownTimeout            time.Duration
	AllowPublic                bool
	AllowedUsers, AllowedChats map[string]bool
	MaxUserTasks, MaxChatTasks int
}

type Runtime struct {
	Config   Config
	Engine   *research.Engine
	Model    model.Completer
	Renderer render.Renderer
	Typst    *render.Typst
	Senders  map[string]platform.Sender
	Telegram *platform.Telegram
	QQ       *platform.QQ
	OneBot   *platform.OneBot
	Cache    *reader.Cache
	Clients  []*http.Client
}

func intEnv(getenv func(string) string, key string, def int) (int, error) {
	s := getenv(key)
	if s == "" {
		return def, nil
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("invalid %s", key)
	}
	return v, nil
}
func boolEnv(getenv func(string) string, key string, def bool) (bool, error) {
	s := getenv(key)
	if s == "" {
		return def, nil
	}
	v, err := strconv.ParseBool(s)
	if err != nil {
		return false, fmt.Errorf("invalid %s", key)
	}
	return v, nil
}
func durationEnv(getenv func(string) string, key string, def time.Duration) (time.Duration, error) {
	s := getenv(key)
	if s == "" {
		return def, nil
	}
	v, err := time.ParseDuration(s)
	if err != nil || v <= 0 {
		return 0, fmt.Errorf("invalid %s", key)
	}
	return v, nil
}

func Load(getenv func(string) string) (Config, error) {
	c := Config{DataDir: getenv("SEARCHAN_DATA_DIR"), Listen: getenv("SEARCHAN_LISTEN")}
	if c.DataDir == "" {
		c.DataDir = "data"
	}
	if c.Listen == "" {
		c.Listen = "127.0.0.1:8090"
	}
	if _, _, err := net.SplitHostPort(c.Listen); err != nil {
		return c, errors.New("invalid SEARCHAN_LISTEN")
	}
	names := getenv("SEARCHAN_PROVIDERS")
	if names == "" {
		names = "ddg"
	}
	seen := map[string]bool{}
	for _, name := range strings.Split(names, ",") {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" || seen[name] {
			return c, errors.New("empty or duplicate search provider")
		}
		seen[name] = true
		c.Providers = append(c.Providers, name)
	}
	var err error
	for _, entry := range []struct {
		key      string
		target   *int
		def      int
		min, max int
	}{
		{"SEARCHAN_WORKERS", &c.Workers, 2, 1, 8}, {"SEARCHAN_QUEUE_SIZE", &c.QueueSize, 32, 1, 256},
		{"SEARCHAN_SEARCH_CALLS", &c.MaxSearchCalls, 6, 1, 24}, {"SEARCHAN_MAX_PAGES", &c.MaxPages, 8, 1, 24},
		{"SEARCHAN_SEARCH_CONCURRENCY", &c.SearchConcurrency, 3, 1, 8}, {"SEARCHAN_READ_CONCURRENCY", &c.ReadConcurrency, 3, 1, 8},
		{"SEARCHAN_MAX_EVIDENCE", &c.MaxEvidence, 12, 1, 24},
		{"SEARCHAN_MAX_USER_TASKS", &c.MaxUserTasks, 2, 1, 32}, {"SEARCHAN_MAX_CHAT_TASKS", &c.MaxChatTasks, 4, 1, 64},
	} {
		*entry.target, err = intEnv(getenv, entry.key, entry.def)
		if err != nil {
			return c, err
		}
		if *entry.target < entry.min || *entry.target > entry.max {
			return c, fmt.Errorf("%s out of range", entry.key)
		}
	}
	c.Timeout, err = durationEnv(getenv, "SEARCHAN_TASK_TIMEOUT", 90*time.Second)
	if err != nil {
		return c, err
	}
	c.ResearchTimeout, err = durationEnv(getenv, "SEARCHAN_RESEARCH_TIMEOUT", 45*time.Second)
	if err != nil {
		return c, err
	}
	if c.ResearchTimeout >= c.Timeout {
		return c, errors.New("research timeout must leave time for rendering and delivery")
	}
	c.PlanWithModel, err = boolEnv(getenv, "SEARCHAN_MODEL_PLAN", true)
	if err != nil {
		return c, err
	}
	for _, entry := range []struct {
		key      string
		value    *time.Duration
		fallback time.Duration
	}{
		{"SEARCHAN_SEARCH_TIMEOUT", &c.SearchTimeout, 8 * time.Second}, {"SEARCHAN_READ_TIMEOUT", &c.ReadTimeout, 10 * time.Second},
		{"SEARCHAN_PLANNING_TIMEOUT", &c.PlanningTimeout, 4 * time.Second}, {"SEARCHAN_MODEL_TIMEOUT", &c.ModelTimeout, 18 * time.Second},
	} {
		*entry.value, err = durationEnv(getenv, entry.key, entry.fallback)
		if err != nil {
			return c, err
		}
	}
	c.VerifyWithModel, err = boolEnv(getenv, "SEARCHAN_MODEL_VERIFY", true)
	if err != nil {
		return c, err
	}
	c.ShutdownTimeout, err = durationEnv(getenv, "SEARCHAN_SHUTDOWN_TIMEOUT", 30*time.Second)
	if err != nil {
		return c, err
	}
	if c.ShutdownTimeout > 2*time.Minute {
		return c, errors.New("shutdown timeout must not exceed 2m")
	}
	c.AllowPublic, err = boolEnv(getenv, "SEARCHAN_PUBLIC", false)
	if err != nil {
		return c, err
	}
	c.AllowedUsers, err = accessList(getenv("SEARCHAN_ALLOWED_USERS"))
	if err != nil {
		return c, err
	}
	c.AllowedChats, err = accessList(getenv("SEARCHAN_ALLOWED_CHATS"))
	return c, err
}

func accessList(raw string) (map[string]bool, error) {
	result := map[string]bool{}
	if strings.TrimSpace(raw) == "" {
		return result, nil
	}
	for _, value := range strings.Split(raw, ",") {
		value = strings.TrimSpace(value)
		platform, id, ok := strings.Cut(value, ":")
		if !ok || (platform != "telegram" && platform != "qq") || id == "" || len(id) > 128 || strings.ContainsAny(id, " \r\n\t:") || len(result) >= 256 {
			return nil, errors.New("access lists require up to 256 platform:id entries")
		}
		result[value] = true
	}
	return result, nil
}
func (c Config) Allowed(target platform.Target) bool {
	return c.AllowPublic || c.AllowedUsers[target.Platform+":"+target.UserID] || c.AllowedChats[target.Platform+":"+target.ChatID]
}

func (r *Runtime) validateAccess() error {
	if r.Config.AllowPublic {
		return nil
	}
	for name := range r.Senders {
		configured := false
		for _, list := range []map[string]bool{r.Config.AllowedUsers, r.Config.AllowedChats} {
			for entry := range list {
				if strings.HasPrefix(entry, name+":") {
					configured = true
					break
				}
			}
		}
		if !configured {
			return fmt.Errorf("configure access for %s with SEARCHAN_ALLOWED_USERS/CHATS, or explicitly set SEARCHAN_PUBLIC=true", name)
		}
	}
	return nil
}

func New(getenv func(string) string) (*Runtime, error) {
	cfg, err := Load(getenv)
	if err != nil {
		return nil, err
	}
	serviceClient := httpx.NewClient(30 * time.Second)
	var providers []search.Provider
	for _, name := range cfg.Providers {
		prefix := strings.ToUpper(name)
		pc := search.Config{APIKey: getenv(prefix + "_API_KEY"), Endpoint: getenv(prefix + "_ENDPOINT"), Client: serviceClient}
		if name == "searxng" {
			for _, engine := range strings.Split(getenv("SEARXNG_ENGINES"), ",") {
				if engine = strings.TrimSpace(engine); engine != "" {
					pc.Engines = append(pc.Engines, engine)
				}
			}
		}
		p, err := search.NewProvider(name, pc)
		if err != nil {
			return nil, err
		}
		providers = append(providers, p)
	}
	var lm model.Completer
	clients := []*http.Client{serviceClient}
	if endpoint := getenv("MODEL_ENDPOINT"); endpoint != "" {
		if getenv("MODEL_NAME") == "" {
			return nil, errors.New("MODEL_NAME is required with MODEL_ENDPOINT")
		}
		maxTokens, err := intEnv(getenv, "MODEL_MAX_TOKENS", 2400)
		if err != nil || maxTokens < 256 || maxTokens > 8192 {
			return nil, errors.New("MODEL_MAX_TOKENS must be 256-8192")
		}
		jsonMode, err := boolEnv(getenv, "MODEL_JSON_MODE", true)
		if err != nil {
			return nil, err
		}
		retries, err := intEnv(getenv, "MODEL_RETRIES", 1)
		if err != nil || retries < 0 || retries > 1 {
			return nil, errors.New("MODEL_RETRIES must be 0 or 1")
		}
		thinkingBudget, err := intEnv(getenv, "MODEL_THINKING_BUDGET", 0)
		if err != nil {
			return nil, err
		}
		completion := &model.Client{
			Endpoint: endpoint, APIKey: getenv("MODEL_API_KEY"), Model: getenv("MODEL_NAME"),
			API: getenv("MODEL_API"), Auth: getenv("MODEL_AUTH"), AnthropicVersion: getenv("MODEL_ANTHROPIC_VERSION"),
			MaxTokens: maxTokens, TokenParameter: getenv("MODEL_TOKEN_PARAMETER"), JSONMode: jsonMode,
			Thinking: getenv("MODEL_THINKING"), ThinkingBudget: thinkingBudget,
			ReasoningEffort: getenv("MODEL_REASONING_EFFORT"), MaxRetries: retries,
		}
		if err := completion.Validate(); err != nil {
			return nil, err
		}
		modelClient := model.NewHTTPClient(cfg.ModelTimeout)
		clients = append(clients, modelClient)
		completion.Client = modelClient
		lm = completion
	}
	direct := reader.NewHTTP(nil)
	cached := &reader.Cache{Dir: cfg.DataDir + "/cache/pages", TTL: 30 * time.Minute, Next: direct}
	var fallback reader.Reader
	if enabled, err := boolEnv(getenv, "JINA_READER_ENABLED", false); err != nil {
		return nil, err
	} else if enabled {
		if getenv("JINA_API_KEY") == "" {
			return nil, errors.New("JINA_API_KEY required when JINA_READER_ENABLED=true")
		}
		fallback = &reader.Jina{APIKey: getenv("JINA_API_KEY"), Endpoint: getenv("JINA_READER_ENDPOINT"), Client: serviceClient}
	}
	engine, err := research.New(research.Config{Providers: providers, Reader: cached, FallbackReader: fallback, Model: lm, PlanWithModel: cfg.PlanWithModel, VerifyWithModel: cfg.VerifyWithModel, MaxSearchCalls: cfg.MaxSearchCalls, MaxPages: cfg.MaxPages, ReadConcurrency: cfg.ReadConcurrency, SearchConcurrency: cfg.SearchConcurrency, MaxEvidence: cfg.MaxEvidence, SearchTimeout: cfg.SearchTimeout, ReadTimeout: cfg.ReadTimeout, PlanningTimeout: cfg.PlanningTimeout, ModelTimeout: cfg.ModelTimeout})
	if err != nil {
		return nil, err
	}
	renderer := render.New(getenv("TYPST_BIN"), 1)
	if fonts := getenv("TYPST_FONT_PATHS"); fonts != "" {
		renderer.FontPaths = strings.Split(fonts, string(os.PathListSeparator))
	}
	runtime := &Runtime{Config: cfg, Engine: engine, Model: lm, Renderer: renderer, Typst: renderer, Cache: cached, Senders: map[string]platform.Sender{}, Clients: clients}
	if token := getenv("TELEGRAM_BOT_TOKEN"); token != "" {
		runtime.Telegram = &platform.Telegram{Token: token, WebhookSecret: getenv("TELEGRAM_WEBHOOK_SECRET"), Username: strings.TrimPrefix(getenv("TELEGRAM_BOT_USERNAME"), "@"), Endpoint: getenv("TELEGRAM_API_ENDPOINT"), Client: serviceClient}
		runtime.Senders["telegram"] = platform.RetrySender{Next: runtime.Telegram}
	}
	qqBackend := getenv("QQ_BACKEND")
	if qqBackend == "" {
		qqBackend = "official"
	}
	if qqBackend != "official" && qqBackend != "onebot" {
		return nil, errors.New("QQ_BACKEND must be official or onebot")
	}
	if qqBackend == "onebot" {
		runtime.OneBot = &platform.OneBot{Endpoint: getenv("ONEBOT_ENDPOINT"), AccessToken: getenv("ONEBOT_ACCESS_TOKEN"), WebhookSecret: getenv("ONEBOT_WEBHOOK_SECRET"), SelfID: getenv("ONEBOT_SELF_ID"), Client: serviceClient}
		if err := runtime.OneBot.Validate(); err != nil {
			return nil, err
		}
		runtime.Senders["qq"] = platform.RetrySender{Next: runtime.OneBot}
	} else if id := getenv("QQ_APP_ID"); id != "" {
		if getenv("QQ_APP_SECRET") == "" {
			return nil, errors.New("QQ_APP_SECRET is required")
		}
		runtime.QQ = &platform.QQ{AppID: id, Secret: getenv("QQ_APP_SECRET"), Endpoint: getenv("QQ_API_ENDPOINT"), TokenEndpoint: getenv("QQ_TOKEN_ENDPOINT"), Client: serviceClient}
		runtime.Senders["qq"] = runtime.QQ
	}
	return runtime, nil
}
func (r *Runtime) Close() {
	for _, c := range r.Clients {
		c.CloseIdleConnections()
	}
}
