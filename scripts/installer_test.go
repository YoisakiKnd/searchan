package scripts

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLinuxInstallerVerifiesTypstArchiveBeforeActivation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash/POSIX installation")
	}
	for _, tool := range []string{"sha256sum", "xz", "tar"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip("archive test requires " + tool)
		}
	}
	root := t.TempDir()
	arch, err := exec.Command("uname", "-m").Output()
	if err != nil {
		t.Fatal(err)
	}
	member := "typst-x86_64-unknown-linux-musl"
	switch strings.TrimSpace(string(arch)) {
	case "arm64", "aarch64":
		member = "typst-aarch64-unknown-linux-musl"
	case "x86_64":
	default:
		t.Skip("unsupported installer architecture")
	}
	if err := os.Mkdir(filepath.Join(root, member), 0700); err != nil {
		t.Fatal(err)
	}
	typst := []byte("#!/bin/sh\nexit 0 # archive fixture\n")
	if err := os.WriteFile(filepath.Join(root, member, "typst"), typst, 0700); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(root, "typst.tar.xz")
	if out, err := exec.Command("tar", "-cJf", archive, "-C", root, member).CombinedOutput(); err != nil {
		t.Fatalf("create archive: %v %s", err, out)
	}
	content, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "searchan")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	args := []string{"install-linux.sh", "--destdir", root, "--binary", binary, "--typst-archive", archive, "--version", "archive-test", "--typst-sha256"}
	if out, err := exec.Command("bash", append(args, strings.Repeat("0", 64))...).CombinedOutput(); err == nil || !strings.Contains(string(out), "checksum mismatch") {
		t.Fatalf("bad archive checksum was not rejected: %v %s", err, out)
	}
	current := filepath.Join(root, "opt/searchan/current")
	if _, err := os.Lstat(current); !os.IsNotExist(err) {
		t.Fatal("bad checksum activated a release")
	}
	sha := fmt.Sprintf("%X", sha256.Sum256(content)) // Uppercase expected hashes work on Bash 3 too.
	if out, err := exec.Command("bash", append(args, sha)...).CombinedOutput(); err != nil {
		t.Fatalf("verified archive install: %v %s", err, out)
	}
	installed, err := os.ReadFile(filepath.Join(current, "typst"))
	if err != nil || string(installed) != string(typst) {
		t.Fatal("installer did not extract the expected executable", err)
	}
}

// Staging exercises the actual installer without root or touching host services.
func TestLinuxInstallerUpgradeRollbackAndFailedActivation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash/POSIX installation")
	}
	root := t.TempDir()
	dest := filepath.Join(root, "system")
	if err := os.Mkdir(dest, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(path, content string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
	}
	binary := filepath.Join(root, "searchan")
	typst := filepath.Join(root, "typst")
	config := filepath.Join(root, "initial.env")
	marker := filepath.Join(root, "must-not-execute")
	write(binary, "#!/bin/sh\nexit 0 # release one\n", 0700)
	write(typst, "#!/bin/sh\nexit 0\n", 0700)
	write(config, "SECRET=private-fixture\nNEVER=$(touch "+marker+")\n", 0600)
	installer, err := filepath.Abs("install-linux.sh")
	if err != nil {
		t.Fatal(err)
	}
	invoke := func(env []string, success bool, args ...string) string {
		t.Helper()
		cmd := exec.Command("bash", append([]string{installer, "--destdir", dest}, args...)...)
		cmd.Env = append(os.Environ(), env...)
		out, err := cmd.CombinedOutput()
		if (err == nil) != success {
			t.Fatalf("installer success=%v: %v\n%s", success, err, out)
		}
		if strings.Contains(string(out), "private-fixture") {
			t.Fatal("installer printed credentials")
		}
		return string(out)
	}
	current := filepath.Join(dest, "opt/searchan/current")
	assertVersion := func(want string) {
		t.Helper()
		got, err := os.Readlink(current)
		if err != nil || got != "releases/"+want {
			t.Fatalf("activation=%s, err=%v; want %s", got, err, want)
		}
	}
	args := []string{"--binary", binary, "--typst", typst, "--version", "v1"}
	invoke(nil, true, append(args, "--env-file", config)...)
	assertVersion("v1")
	dataFile := filepath.Join(dest, "var/lib/searchan/keep.json")
	write(dataFile, "durable-data", 0600)
	invoke(nil, true, args...) // Identical release is idempotent.
	write(binary, "#!/bin/sh\nexit 0 # release two\n", 0700)
	invoke(nil, false, args...) // Cannot replace immutable v1.
	args[len(args)-1] = "v2"
	invoke(nil, true, args...)
	assertVersion("v2")
	storedConfig := filepath.Join(dest, "etc/searchan.env")
	before, _ := os.ReadFile(storedConfig)
	if string(before) != "SECRET=private-fixture\nNEVER=$(touch "+marker+")\n" {
		t.Fatal("upgrade changed existing config")
	}
	if info, err := os.Stat(storedConfig); err != nil || info.Mode().Perm() != 0640 {
		t.Fatal("config permissions", err)
	}
	invoke(nil, true, "--rollback", "v1")
	assertVersion("v1")
	invoke(nil, false, "--rollback", "../outside")
	invoke(nil, false, append(args, "--env-file", config)...)

	// Fail only the next activation rename. The EXIT trap must restore config/current.
	fakePath := filepath.Join(root, "fake-bin")
	if err := os.Mkdir(fakePath, 0700); err != nil {
		t.Fatal(err)
	}
	realMV, err := exec.LookPath("mv")
	if err != nil {
		t.Fatal(err)
	}
	failFlag := filepath.Join(root, "fail-next-rename")
	write(failFlag, "", 0600)
	write(filepath.Join(fakePath, "mv"), `#!/bin/sh
for task_last; do :; done
case "$task_last" in
  */current) if [ -f "$TEST_FAIL_FLAG" ]; then rm "$TEST_FAIL_FLAG"; exit 42; fi;;
esac
exec "$TEST_REAL_MV" "$@"
`, 0700)
	write(config, "SECRET=replacement\n", 0600)
	invoke([]string{"PATH=" + fakePath + string(os.PathListSeparator) + os.Getenv("PATH"), "TEST_REAL_MV=" + realMV, "TEST_FAIL_FLAG=" + failFlag}, false, append(args, "--env-file", config, "--replace-env")...)
	assertVersion("v1")
	after, _ := os.ReadFile(storedConfig)
	if string(before) != string(after) {
		t.Fatal("failed activation did not restore config")
	}
	data, err := os.ReadFile(dataFile)
	if err != nil || string(data) != "durable-data" {
		t.Fatal("installation changed data", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("installer evaluated environment file")
	}
	if _, err := os.Stat(filepath.Join(dest, "opt/searchan/.install-lock")); !os.IsNotExist(err) {
		t.Fatal("staging lock leaked")
	}
}
