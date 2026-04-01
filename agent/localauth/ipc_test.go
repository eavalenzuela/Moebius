//nolint:gosec // test file with hardcoded test credentials
package localauth

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/eavalenzuela/Moebius/agent/ipc"
)

// mockAuth implements Authenticator for testing.
type mockAuth struct {
	users map[string]string // username → password
}

func (m *mockAuth) Authenticate(username, password string) error {
	if pw, ok := m.users[username]; ok && pw == password {
		return nil
	}
	return fmt.Errorf("invalid credentials")
}

func TestIPCAuthLogin(t *testing.T) {
	router := ipc.NewRouter()
	sessions := NewSessionManager()
	auth := &mockAuth{users: map[string]string{"admin": "secret"}}
	RegisterIPC(router, auth, sessions)

	t.Run("successful login", func(t *testing.T) {
		params, _ := json.Marshal(LoginParams{Username: "admin", Password: "secret"})
		resp := router.Dispatch(context.Background(), &ipc.Request{
			ID: "1", Method: "auth.login", Params: params,
		})
		if resp.Error != nil {
			t.Fatalf("unexpected error: %v", resp.Error)
		}
		var result LoginResult
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			t.Fatalf("unmarshal result: %v", err)
		}
		if result.Token == "" {
			t.Error("expected non-empty token")
		}
	})

	t.Run("bad credentials", func(t *testing.T) {
		params, _ := json.Marshal(LoginParams{Username: "admin", Password: "wrong"})
		resp := router.Dispatch(context.Background(), &ipc.Request{
			ID: "2", Method: "auth.login", Params: params,
		})
		if resp.Error == nil {
			t.Fatal("expected error for bad credentials")
		}
		if resp.Error.Code != ipc.CodeUnauthorized {
			t.Errorf("Code = %d, want %d", resp.Error.Code, ipc.CodeUnauthorized)
		}
	})

	t.Run("missing fields", func(t *testing.T) {
		params, _ := json.Marshal(LoginParams{Username: "admin"})
		resp := router.Dispatch(context.Background(), &ipc.Request{
			ID: "3", Method: "auth.login", Params: params,
		})
		if resp.Error == nil {
			t.Fatal("expected error for missing password")
		}
		if resp.Error.Code != ipc.CodeInvalidParams {
			t.Errorf("Code = %d, want %d", resp.Error.Code, ipc.CodeInvalidParams)
		}
	})
}

func TestIPCAuthValidate(t *testing.T) {
	router := ipc.NewRouter()
	sessions := NewSessionManager()
	auth := &mockAuth{users: map[string]string{"admin": "secret"}}
	RegisterIPC(router, auth, sessions)

	// Login first.
	params, _ := json.Marshal(LoginParams{Username: "admin", Password: "secret"})
	loginResp := router.Dispatch(context.Background(), &ipc.Request{
		ID: "1", Method: "auth.login", Params: params,
	})
	var loginResult LoginResult
	_ = json.Unmarshal(loginResp.Result, &loginResult)

	t.Run("valid token", func(t *testing.T) {
		vp, _ := json.Marshal(ValidateParams{Token: loginResult.Token})
		resp := router.Dispatch(context.Background(), &ipc.Request{
			ID: "2", Method: "auth.validate", Params: vp,
		})
		if resp.Error != nil {
			t.Fatalf("unexpected error: %v", resp.Error)
		}
		var result ValidateResult
		_ = json.Unmarshal(resp.Result, &result)
		if !result.Valid {
			t.Error("expected valid=true")
		}
		if result.Username != "admin" {
			t.Errorf("Username = %q, want %q", result.Username, "admin")
		}
	})

	t.Run("invalid token", func(t *testing.T) {
		vp, _ := json.Marshal(ValidateParams{Token: "bogus"})
		resp := router.Dispatch(context.Background(), &ipc.Request{
			ID: "3", Method: "auth.validate", Params: vp,
		})
		var result ValidateResult
		_ = json.Unmarshal(resp.Result, &result)
		if result.Valid {
			t.Error("expected valid=false for bogus token")
		}
	})
}

func TestIPCAuthLogout(t *testing.T) {
	router := ipc.NewRouter()
	sessions := NewSessionManager()
	auth := &mockAuth{users: map[string]string{"admin": "secret"}}
	RegisterIPC(router, auth, sessions)

	// Login.
	params, _ := json.Marshal(LoginParams{Username: "admin", Password: "secret"})
	loginResp := router.Dispatch(context.Background(), &ipc.Request{
		ID: "1", Method: "auth.login", Params: params,
	})
	var loginResult LoginResult
	_ = json.Unmarshal(loginResp.Result, &loginResult)

	// Logout.
	lp, _ := json.Marshal(ValidateParams{Token: loginResult.Token})
	resp := router.Dispatch(context.Background(), &ipc.Request{
		ID: "2", Method: "auth.logout", Params: lp,
	})
	if resp.Error != nil {
		t.Fatalf("logout error: %v", resp.Error)
	}

	// Validate should now fail.
	vp, _ := json.Marshal(ValidateParams{Token: loginResult.Token})
	vResp := router.Dispatch(context.Background(), &ipc.Request{
		ID: "3", Method: "auth.validate", Params: vp,
	})
	var result ValidateResult
	_ = json.Unmarshal(vResp.Result, &result)
	if result.Valid {
		t.Error("expected valid=false after logout")
	}
}
