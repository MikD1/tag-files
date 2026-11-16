package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func dockerFixture(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte("#!/bin/sh\n"+body), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestDockerReturnsContainerIDWithoutPullProgress(t *testing.T) {
	dockerFixture(t, `
test "$1" = create || exit 2
printf "Unable to find image 'chrislusf/seaweedfs:4.53' locally\n" >&2
printf 'fixture-container-id\n'
`)
	if got := Docker(t, "", "create"); got != "fixture-container-id" {
		t.Fatalf("container ID includes Docker diagnostics: %q", got)
	}
}

func TestDockerLogsIncludeContainerStderr(t *testing.T) {
	dockerFixture(t, `
test "$1" = logs || exit 2
printf 'fixture stdout log\n'
printf 'fixture stderr log\n' >&2
`)
	output := Docker(t, "", "logs", "fixture-container-id")
	for _, detail := range []string{"fixture stdout log", "fixture stderr log"} {
		if !strings.Contains(output, detail) {
			t.Fatalf("container logs lost %q: %s", detail, output)
		}
	}
}

func TestDockerFailurePreservesCommandDiagnostics(t *testing.T) {
	const fixtureEnv = "TAG_FILES_DOCKER_FAILURE_FIXTURE"
	if os.Getenv(fixtureEnv) == "1" {
		dockerFixture(t, `
test "$1" = create || exit 2
printf 'fixture stdout detail\n'
printf 'fixture stderr detail\n' >&2
exit 1
`)
		Docker(t, "", "create")
		return
	}
	command := exec.Command(os.Args[0], "-test.run=^TestDockerFailurePreservesCommandDiagnostics$")
	command.Env = append(os.Environ(), fixtureEnv+"=1")
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("failed Docker command was accepted")
	}
	for _, detail := range []string{"docker create", "fixture stdout detail", "fixture stderr detail"} {
		if !strings.Contains(string(output), detail) {
			t.Fatalf("Docker failure lost %q: %s", detail, output)
		}
	}
}
