package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseRequiresMatchingVersionTag(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("requires bash")
	}
	script, err := filepath.Abs("release-meta.sh")
	if err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	command := func(name string, args ...string) ([]byte, error) {
		cmd := exec.Command(name, args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		return cmd.CombinedOutput()
	}
	git := func(args ...string) string {
		t.Helper()
		out, err := command("git", args...)
		if err != nil {
			t.Fatalf("git: %v %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	commit := func() {
		git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "fixture")
	}
	commit()
	revision := git("rev-parse", "HEAD")
	git("tag", "v1.2.3")
	git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "tag.gpgSign=false", "tag", "-a", "v1.2.4-rc.1", "-m", "candidate")
	for _, tc := range []struct{ tag, version, prerelease string }{{"v1.2.3", "1.2.3", "false"}, {"v1.2.4-rc.1", "1.2.4-rc.1", "true"}} {
		out, err := command("bash", script, "tag", tc.tag)
		want := "tag=" + tc.tag + "\nversion=" + tc.version + "\nrevision=" + revision + "\nprerelease=" + tc.prerelease + "\n"
		if err != nil || string(out) != want {
			t.Fatalf("metadata for %s: %v %s", tc.tag, err, out)
		}
	}
	for _, args := range [][]string{{"branch", "main"}, {"tag", "v01.2.3"}, {"tag", "v1.2"}, {"tag", "v1.2.3$(touch marker)"}, {"tag", "v9.9.9"}} {
		if out, err := command("bash", append([]string{script}, args...)...); err == nil {
			t.Fatalf("invalid release accepted: %v %s", args, out)
		}
	}
	commit()
	if out, err := command("bash", script, "tag", "v1.2.3"); err == nil || !strings.Contains(string(out), "does not match") {
		t.Fatalf("different commit passed release gate: %v %s", err, out)
	}
}
