package app

import (
	"net/http"
	"testing"
	"time"

	"searchan/model"
)

func TestModelConfigurationAndIsolatedHTTPBudget(t *testing.T) {
	for _, api := range []string{"chat_completions", "responses", "anthropic"} {
		env := map[string]string{"MODEL_ENDPOINT": "https://gateway.example/custom", "MODEL_NAME": "fixture", "MODEL_API": api, "MODEL_RETRIES": "0", "SEARCHAN_MODEL_TIMEOUT": "45s"}
		r, err := New(func(k string) string { return env[k] })
		if err != nil {
			t.Fatal(err)
		}
		m := r.Model.(*model.Client)
		if m.API != api || m.MaxRetries != 0 || len(r.Clients) != 2 || r.Clients[0] == m.Client || m.Client.Timeout != 45*time.Second || m.Client.Transport.(*http.Transport).ResponseHeaderTimeout != 0 {
			t.Fatalf("model settings not wired: %+v", m)
		}
		r.Close()
	}
	for key, value := range map[string]string{"MODEL_API": "wrong", "MODEL_AUTH": "wrong", "MODEL_RETRIES": "2", "MODEL_THINKING": "wrong", "MODEL_REASONING_EFFORT": "wrong", "MODEL_TOKEN_PARAMETER": "wrong", "MODEL_THINKING_BUDGET": "-1"} {
		env := map[string]string{"MODEL_ENDPOINT": "https://gateway.example/custom", "MODEL_NAME": "fixture", key: value}
		if r, err := New(func(k string) string { return env[k] }); err == nil {
			r.Close()
			t.Fatalf("invalid %s accepted", key)
		}
	}
}
