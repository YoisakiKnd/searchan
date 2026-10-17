package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestServiceSmokeCannotReconfigureNonCIRunner(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("native fixture smoke requires Python 3")
	}
	for _, optimized := range []bool{false, true} {
		out := filepath.Join(t.TempDir(), "acceptance")
		args := []string{"service-smoke.py", "--systemd", "--out", out}
		if optimized {
			args = append([]string{"-O"}, args...)
		}
		cmd := exec.Command(python, args...)
		cmd.Env = append(os.Environ(), "GITHUB_ACTIONS=false")
		data, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(data), "systemd mode requires the disposable CI runner") {
			t.Fatalf("native smoke did not reject a non-CI installation before service/config access: %v %s", err, data)
		}
		if _, err := os.Stat(out); !os.IsNotExist(err) {
			t.Fatal("non-CI guard created test state before rejecting the operation", err)
		}
	}
}
