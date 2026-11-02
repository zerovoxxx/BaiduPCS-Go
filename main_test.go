package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsconfig"
)

func TestCLIHelper(t *testing.T) {
	if os.Getenv("BAIDUPCS_P1_TEST_CHILD") != "1" {
		return
	}
	separator := 0
	for i, arg := range os.Args {
		if arg == "--" {
			separator = i
			break
		}
	}
	os.Args = append([]string{"BaiduPCS-Go"}, os.Args[separator+1:]...)
	before, _ := os.Getwd()
	main()
	after, _ := os.Getwd()
	if os.Getenv("BAIDUPCS_P1_EXPECT_LEGACY") == "1" {
		if pcsconfig.Config.MaxParallel != 7 {
			fmt.Fprintln(os.Stderr, "legacy config not loaded")
			os.Exit(3)
		}
		fmt.Println("LEGACY_CONFIG_OK")
	}
	if before != after {
		fmt.Fprintln(os.Stderr, "unexpected working directory change")
		os.Exit(4)
	}
	os.Exit(0)
}
func cliChild(t *testing.T, directory, legacy string, expectLegacy bool, args ...string) ([]byte, error) {
	t.Helper()
	argv := append([]string{"-test.run=^TestCLIHelper$", "--"}, args...)
	command := exec.Command(os.Args[0], argv...)
	command.Dir = directory
	command.Env = append(os.Environ(), "BAIDUPCS_P1_TEST_CHILD=1", "BAIDUPCS_GO_CONFIG_DIR="+legacy, "BAIDUPCS_P1_EXPECT_LEGACY=0")
	if expectLegacy {
		command.Env = append(command.Env, "BAIDUPCS_P1_EXPECT_LEGACY=1")
	}
	return command.CombinedOutput()
}
func TestServerHelpDoesNotOpenCLIConfiguration(t *testing.T) {
	directory, legacy := t.TempDir(), filepath.Join(t.TempDir(), "legacy")
	if err := os.Mkdir(legacy, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(legacy, pcsconfig.ConfigName)
	before := []byte("invalid CLI JSON sentinel")
	if err := os.WriteFile(path, before, 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"server", "--help"}, {"--verbose", "server", "--help"}, {"help", "server"}} {
		output, err := cliChild(t, directory, legacy, false, args...)
		if err != nil || !bytes.Contains(output, []byte("data-dir")) {
			t.Fatalf("server help touched legacy config: %v %s", err, output)
		}
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("server help modified CLI config")
	}
	missing := filepath.Join(t.TempDir(), "must-not-create")
	if output, err := cliChild(t, directory, missing, false, "server", "--help"); err != nil {
		t.Fatalf("%v %s", err, output)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("server package initialization created CLI directory")
	}
}
func TestLegacyCLIStillInitializesAndUsesItsConfiguration(t *testing.T) {
	directory, legacy := t.TempDir(), t.TempDir()
	path := filepath.Join(legacy, pcsconfig.ConfigName)
	if err := os.WriteFile(path, []byte(`{"baidu_active_uid":0,"baidu_user_list":[],"max_parallel":7,"no_check":false}`), 0600); err != nil {
		t.Fatal(err)
	}
	output, err := cliChild(t, directory, legacy, true, "--version")
	if err != nil || !strings.Contains(string(output), Version) || !strings.Contains(string(output), "LEGACY_CONFIG_OK") {
		t.Fatalf("legacy invocation changed: %v %s", err, output)
	}
	if err = os.WriteFile(path, []byte("invalid CLI JSON sentinel"), 0600); err != nil {
		t.Fatal(err)
	}
	output, err = cliChild(t, directory, legacy, false, "--version")
	if err == nil || !strings.Contains(string(output), "FATAL ERROR: config file error") {
		t.Fatalf("legacy invalid-config behavior changed: %v %s", err, output)
	}
}
