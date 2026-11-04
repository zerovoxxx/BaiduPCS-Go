package baidu

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/account"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
)

func TestP2TokenOnlyValidationCannotClaimCookieIdentity(t *testing.T) {
	server, pool := trustedServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/2.0/pcs/quota" || r.URL.Query().Get("access_token") != "TOKEN_ONLY_EXAMPLE" || r.Header.Get("Cookie") != "" {
			t.Error("token validation used an unverified cookie/identity path")
			http.Error(w, "unexpected identity request", 400)
			return
		}
		io.WriteString(w, `{"quota":1024,"used":0}`)
	}))
	factory := connectedFactory(t, server, pool)
	defer factory.Close()
	identity, err := factory.ValidateAccount(context.Background(), configuration.NewSnapshot(1, DefaultConfiguration()), account.Credentials{AccountID: "token_account", Revision: 1, AccessToken: "TOKEN_ONLY_EXAMPLE"})
	if err != nil {
		t.Fatal(err)
	}
	if identity.Verified || identity.UID != nil || identity.UK != nil || identity.Username != nil {
		t.Fatal("PCS token validation invented an identity")
	}
}
func TestP2BodyIdleTimeoutReleasesConnection(t *testing.T) {
	exited := make(chan struct{})
	server, pool := trustedServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(exited)
		w.Header().Set("Content-Length", "100")
		io.WriteString(w, "a")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	factory := connectedFactory(t, server, pool)
	defer factory.Close()
	session, err := factory.New(context.Background(), configuration.NewSnapshot(1, DefaultConfiguration()), account.Credentials{AccountID: "idle_account", Revision: 1, Cookies: map[string]string{"BDUSS": "IDLE_ONLY_EXAMPLE"}})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	session.HTTP.Transport.(*boundTransport).idleTimeout = 100 * time.Millisecond
	session.HTTP.SetTimeout(0)
	response, err := session.HTTP.Get("https://pcs.baidu.com/slow")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	_, err = io.ReadAll(response.Body)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("body idle timeout did not cancel the stream", err)
	}
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("timed out body left an upstream connection running")
	}
}
