package pcscommand

import (
	"bytes"
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsconfig"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcslogin"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
	"github.com/urfave/cli"
)

func commandApp(t *testing.T, login qrLoginFunc) (*cli.App, *bytes.Buffer, *pcsconfig.PCSConfig) {
	t.Helper()
	config := pcsconfig.NewConfig(filepath.Join(t.TempDir(), pcsconfig.ConfigName))
	if err := config.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { config.Close() })
	var out bytes.Buffer
	app := cli.NewApp()
	app.Writer = &out
	app.ErrWriter = &out
	app.ExitErrHandler = func(*cli.Context, error) {}
	app.Commands = []cli.Command{qrLoginCommand(config, login)}
	return app, &out, config
}

func TestQRCommandCleansImagesAndReturnsExitCodes(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		exit int
	}{
		{"cancel", context.Canceled, 130}, {"timeout", context.DeadlineExceeded, 124}, {"protocol", pcslogin.ErrCredentials, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			imagePath := filepath.Join(t.TempDir(), "qr.png")
			app, out, config := commandApp(t, func(ctx context.Context, opts pcslogin.Options) (*pcslogin.Session, error) {
				if err := opts.OnQRCode([]byte("test-image")); err != nil {
					return nil, err
				}
				return nil, tc.err
			})
			err := app.Run([]string{"pcs", "qrlogin", "--no-qr", "--qr-file", imagePath})
			code, ok := err.(cli.ExitCoder)
			if !ok || code.ExitCode() != tc.exit {
				t.Fatalf("wrong exit: %v", err)
			}
			if _, err := os.Stat(imagePath); !os.IsNotExist(err) {
				t.Fatal("image containing challenge not cleaned")
			}
			if config.NumLogins() != 0 || strings.Contains(out.String(), "登录成功") {
				t.Fatal("failed authentication saved account or printed success")
			}
		})
	}
}

func TestQRCommandNeverOverwritesExistingImage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keep.png")
	original := []byte("keep-existing-file")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	app, _, _ := commandApp(t, func(ctx context.Context, opts pcslogin.Options) (*pcslogin.Session, error) {
		return nil, opts.OnQRCode([]byte("new-file"))
	})
	if app.Run([]string{"pcs", "qrlogin", "--qr-file", path}) == nil {
		t.Fatal("existing image overwritten")
	}
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(data, original) {
		t.Fatal("existing user file deleted or modified")
	}
}

func TestQRCommandSavesOnlyAfterVerifiedSession(t *testing.T) {
	imagePath := filepath.Join(t.TempDir(), "qr.png")
	app, out, config := commandApp(t, func(ctx context.Context, opts pcslogin.Options) (*pcslogin.Session, error) {
		if err := opts.OnQRCode([]byte("test-image")); err != nil {
			return nil, err
		}
		return &pcslogin.Session{UID: 42, Name: "command-test", Cookies: []requester.CookieEntry{
			{Name: "BDUSS", Value: "bduss-test", Domain: "baidu.com", Path: "/"},
			{Name: "BAIDUID", Value: "baiduid-test", Domain: "baidu.com", Path: "/"},
			{Name: "STOKEN", Value: "pan-stoken-test", Domain: "pan.baidu.com", Path: "/", Secure: true},
		}}, nil
	})
	if err := app.Run([]string{"pcs", "qrlogin", "--no-qr", "--qr-file", imagePath}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "扫码登录成功") || config.ActiveUser().UID != 42 {
		t.Fatal("verified account not committed")
	}
	for _, secret := range []string{"bduss-test", "pan-stoken-test", "baiduid-test"} {
		if strings.Contains(out.String(), secret) {
			t.Fatal("credential printed by command")
		}
	}
	if _, err := os.Stat(imagePath); !os.IsNotExist(err) {
		t.Fatal("image not removed after success")
	}
	if err := config.Reload(); err != nil || config.ActiveUser().UID != 42 {
		t.Fatal("account unavailable after reload")
	}
}

func TestQRCommandRejectsInvalidTimeoutWithoutStartingAuth(t *testing.T) {
	app, _, _ := commandApp(t, func(context.Context, pcslogin.Options) (*pcslogin.Session, error) {
		t.Fatal("auth started with invalid arguments")
		return nil, errors.New("unreachable")
	})
	for _, arg := range []string{"0", "-1", "86401"} {
		err := app.Run([]string{"pcs", "qrlogin", "--timeout", arg})
		code, ok := err.(cli.ExitCoder)
		if !ok || code.ExitCode() != 2 {
			t.Fatalf("bad timeout accepted: %s", arg)
		}
	}
}

func TestQRFlagParseErrorsReturnUsageExitCode(t *testing.T) {
	app, _, _ := commandApp(t, func(context.Context, pcslogin.Options) (*pcslogin.Session, error) {
		t.Fatal("auth started with malformed flags")
		return nil, nil
	})
	for _, args := range [][]string{{"--timeout", "not-seconds"}, {"--timeout"}, {"--unknown-qr-option"}} {
		err := app.Run(append([]string{"pcs", "qrlogin"}, args...))
		code, ok := err.(cli.ExitCoder)
		if !ok || code.ExitCode() != 2 {
			t.Fatalf("malformed flags did not return 2: %v", err)
		}
	}
}

func TestQRCommandDoesNotReportSuccessWhenSavingFails(t *testing.T) {
	var config *pcsconfig.PCSConfig
	path := filepath.Join(t.TempDir(), "qr.png")
	app, out, c := commandApp(t, func(ctx context.Context, opts pcslogin.Options) (*pcslogin.Session, error) {
		if err := opts.OnQRCode([]byte("test-image")); err != nil {
			return nil, err
		}
		config.BaiduUserList = pcsconfig.BaiduUserList{{BaiduBase: pcsconfig.BaiduBase{UID: 42, Name: "old-test"}, Age: math.NaN()}}
		return &pcslogin.Session{UID: 42, Name: "new-test", Cookies: []requester.CookieEntry{
			{Name: "BDUSS", Value: "bduss-test", Domain: "baidu.com", Path: "/"},
			{Name: "BAIDUID", Value: "baiduid-test", Domain: "baidu.com", Path: "/"},
			{Name: "STOKEN", Value: "pan-test", Domain: "pan.baidu.com", Path: "/", Secure: true},
		}}, nil
	})
	config = c
	err := app.Run([]string{"pcs", "qrlogin", "--no-qr", "--qr-file", path})
	code, ok := err.(cli.ExitCoder)
	if !ok || code.ExitCode() != 1 || strings.Contains(out.String(), "登录成功") {
		t.Fatal("saving failure was reported as success")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("QR image survived failed save")
	}
}
