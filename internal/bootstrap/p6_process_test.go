package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestP6ReceptionChildHTTP(t *testing.T) {
	config := os.Getenv("BAIDUPCS_P6_CHILD_CONFIG")
	if config == "" {
		t.Skip("only the SIGKILL parent starts this child")
	}
	runtime, e := New(context.Background(), Options{ConfigFile: config})
	if e != nil {
		t.Fatal("child assembly failed")
	}
	defer runtime.Close()
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(os.Getenv("BAIDUPCS_P6_CHILD_READY"), []byte("http://"+listener.Addr().String()), 0600); e != nil {
		t.Fatal(e)
	}
	if e = runtime.Serve(context.Background(), listener); e != nil {
		t.Fatal(e)
	}
}
func TestP6ReceptionSIGKILLHTTP(t *testing.T) {
	options, _ := testOptions(t)
	raw, _ := os.ReadFile(options.ConfigFile)
	var config startup
	json.Unmarshal(raw, &config)
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	start := func(tag string) (*exec.Cmd, string) {
		t.Helper()
		ready := filepath.Join(t.TempDir(), tag)
		cmd := exec.Command(exe, "-test.run=^TestP6ReceptionChildHTTP$", "-test.timeout=60s")
		cmd.Env = append(os.Environ(), "BAIDUPCS_P6_CHILD_CONFIG="+options.ConfigFile, "BAIDUPCS_P6_CHILD_READY="+ready)
		var output bytes.Buffer
		cmd.Stdout = &output
		cmd.Stderr = &output
		if e := cmd.Start(); e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() {
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
		})
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			b, e := os.ReadFile(ready)
			if e == nil {
				return cmd, string(b)
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("child did not become ready")
		return nil, ""
	}
	call := func(method, address, body string, offset string) map[string]any {
		t.Helper()
		req, e := http.NewRequest(method, address, strings.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		req.Header.Set("Authorization", "Bearer "+testToken)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if offset != "" {
			req.Header.Set("Content-Type", "application/octet-stream")
			req.Header.Set("Upload-Offset", offset)
		}
		resp, e := client.Do(req)
		if e != nil {
			t.Fatal("child request failed")
		}
		defer resp.Body.Close()
		var out struct{ Data map[string]any }
		if e = json.NewDecoder(resp.Body).Decode(&out); e != nil || resp.StatusCode >= 300 {
			t.Fatal("child response failed", resp.StatusCode)
		}
		return out.Data
	}
	first, address := start("first")
	created := call("POST", address+"/api/v1/staging-upload-sessions", `{"filename":"killed","size_bytes":"5"}`, "")
	id := created["session_id"].(string)
	route := "/api/v1/staging-upload-sessions/" + id
	reader, writer := io.Pipe()
	req, _ := http.NewRequest("PATCH", address+route+"/content", reader)
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Upload-Offset", "0")
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		resp, _ := client.Do(req)
		if resp != nil {
			resp.Body.Close()
		}
	}()
	writer.Write([]byte("tail"))
	deadline := time.Now().Add(8 * time.Second)
	observed := false
	for time.Now().Before(deadline) {
		entries, _ := os.ReadDir(filepath.Join(config.DataDir, "staging"))
		if len(entries) == 1 {
			info, _ := entries[0].Info()
			if info != nil && info.Size() == 4 {
				observed = true
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !observed {
		t.Fatal("partial block never reached file")
	}
	if e = first.Process.Kill(); e != nil {
		t.Fatal(e)
	}
	_ = first.Wait()
	writer.Close()
	<-requestDone
	second, restarted := start("second")
	defer func() { _ = second.Process.Kill(); _ = second.Wait() }()
	after := call("GET", restarted+route, "", "")
	if after["offset_bytes"] != "0" || after["staged_file_id"] != nil {
		t.Fatal("uncommitted offset published after SIGKILL")
	}
	call("PATCH", restarted+route+"/content", "hello", "0")
	done := call("POST", restarted+route+"/complete", "", "")
	stage := done["staged_file_id"].(string)
	b, e := os.ReadFile(filepath.Join(config.DataDir, "staging", stage))
	if e != nil || string(b) != "hello" {
		t.Fatal("recovered bytes differ")
	}
}
