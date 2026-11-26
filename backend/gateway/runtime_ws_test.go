package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bejix/upstream-ops/backend/crypto"
	"github.com/bejix/upstream-ops/backend/storage"
	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
)

func setupWSGatewayServer(t *testing.T, upstreamHandler http.HandlerFunc) (wsURL string, apiKey string) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	up := httptest.NewServer(upstreamHandler)
	t.Cleanup(up.Close)

	db := openGatewayTestDB(t)
	groupsRepo := storage.NewGatewayGroups(db)
	keysRepo := storage.NewGatewayKeys(db)
	routesRepo := storage.NewGatewayRoutes(db)
	providersRepo := storage.NewGatewayProviders(db)
	cipher, err := crypto.NewCipher("test-secret-ws-gateway")
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}

	g := &storage.GatewayGroup{
		Name:              "ws-test-group",
		Status:            storage.GatewayGroupStatusActive,
		RateSortDirection: "asc",
	}
	if err := groupsRepo.Create(g); err != nil {
		t.Fatalf("create group: %v", err)
	}
	pCipher, err := cipher.Encrypt("sk-upstream-ws")
	if err != nil {
		t.Fatalf("encrypt provider key: %v", err)
	}
	p := &storage.GatewayProvider{
		Name:             "ws-upstream",
		BaseURL:          up.URL,
		APIKeyCipher:     pCipher,
		APIKeyHint:       "sk-u…ws",
		UpstreamProtocol: "openai_responses",
		Enabled:          true,
	}
	if err := providersRepo.Create(p); err != nil {
		t.Fatalf("create provider: %v", err)
	}
	if err := routesRepo.SaveForGroup(g.ID, []storage.GatewayRoute{{
		GatewayGroupID:    g.ID,
		Position:          0,
		SourceKind:        storage.GatewayRouteSourceProvider,
		GatewayProviderID: p.ID,
		UpstreamProtocol:  "openai_responses",
		Weight:            1,
		Enabled:           true,
		RateConvertMode:   "custom",
		RateConvertValue:  1,
	}}); err != nil {
		t.Fatalf("save routes: %v", err)
	}

	clientKey := "sk-ws-gateway-test-key"
	kCipher, err := cipher.Encrypt(clientKey)
	if err != nil {
		t.Fatalf("encrypt client key: %v", err)
	}
	if err := keysRepo.Create(&storage.GatewayKey{
		GroupID:   g.ID,
		Name:      "ws-client",
		KeyHash:   HashAPIKey(clientKey),
		KeyPrefix: KeyPrefix(clientKey),
		KeyCipher: kCipher,
		Status:    storage.GatewayKeyStatusActive,
	}); err != nil {
		t.Fatalf("create key: %v", err)
	}

	svc := NewService(groupsRepo, keysRepo, routesRepo, nil, nil, nil, nil, cipher, nil)
	svc.SetProviders(providersRepo)

	r := gin.New()
	RegisterPublicWithoutRoot(r, svc)
	gw := httptest.NewServer(r)
	t.Cleanup(gw.Close)

	return "ws" + strings.TrimPrefix(gw.URL, "http") + "/v1/responses", clientKey
}

func TestHandleResponsesWebSocketSecurityAndTimeouts(t *testing.T) {
	t.Run("rejects cross-origin browser request and allows native client without Origin", func(t *testing.T) {
		wsURL, apiKey := setupWSGatewayServer(t, func(w http.ResponseWriter, r *http.Request) {
			upConn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer upConn.CloseNow()
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			if _, _, err := upConn.Read(ctx); err != nil {
				return
			}
			_ = upConn.Write(ctx, websocket.MessageText, []byte(`{"type":"response.completed","response":{"status":"completed"}}`))
		})

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		// 1. 伪造跨源 Origin 必须被拒绝（非 101 握手成功）。
		crossHeaders := http.Header{
			"Authorization": []string{"Bearer " + apiKey},
			"Origin":        []string{"https://evil.example"},
		}
		connCross, respCross, errCross := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: crossHeaders})
		if errCross == nil {
			connCross.CloseNow()
			t.Fatalf("expected cross-origin WebSocket upgrade to fail")
		}
		if respCross != nil && respCross.StatusCode != http.StatusForbidden {
			t.Fatalf("expected 403 on cross-origin upgrade, got %d", respCross.StatusCode)
		}

		// 2. 不带 Origin 的原生 SDK 客户端应正常升级并收发消息。
		nativeHeaders := http.Header{
			"Authorization": []string{"Bearer " + apiKey},
		}
		conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: nativeHeaders})
		if err != nil {
			t.Fatalf("native WS dial failed: %v", err)
		}
		defer conn.CloseNow()
		if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"response.create","model":"gpt-4o"}`)); err != nil {
			t.Fatalf("write response.create: %v", err)
		}
		_, payload, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read response.completed: %v", err)
		}
		if !strings.Contains(string(payload), `"response.completed"`) {
			t.Fatalf("unexpected payload: %s", string(payload))
		}
	})

	t.Run("enforces first frame timeout when client stalls", func(t *testing.T) {
		oldFirst := wsFirstFrameTimeout
		wsFirstFrameTimeout = 60 * time.Millisecond
		t.Cleanup(func() { wsFirstFrameTimeout = oldFirst })

		wsURL, apiKey := setupWSGatewayServer(t, func(w http.ResponseWriter, r *http.Request) {
			t.Errorf("upstream should not be dialed before first client frame")
			http.Error(w, "unexpected", http.StatusBadGateway)
		})

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
			HTTPHeader: http.Header{"Authorization": []string{"Bearer " + apiKey}},
		})
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer conn.CloseNow()

		started := time.Now()
		_, _, readErr := conn.Read(ctx)
		if readErr == nil {
			t.Fatalf("expected connection closed after first frame timeout")
		}
		if time.Since(started) > time.Second {
			t.Fatalf("first frame timeout took too long: %s", time.Since(started))
		}
	})

	t.Run("enforces upstream handshake timeout", func(t *testing.T) {
		oldHandshake := wsHandshakeTimeout
		wsHandshakeTimeout = 60 * time.Millisecond
		t.Cleanup(func() { wsHandshakeTimeout = oldHandshake })

		wsURL, apiKey := setupWSGatewayServer(t, func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
			case <-time.After(2 * time.Second):
			}
		})

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
			HTTPHeader: http.Header{"Authorization": []string{"Bearer " + apiKey}},
		})
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer conn.CloseNow()

		if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"response.create","model":"gpt-4o"}`)); err != nil {
			t.Fatalf("write: %v", err)
		}
		_, msg, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("expected response.failed error frame, got err=%v", err)
		}
		var evt struct {
			Type     string `json:"type"`
			Response struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			} `json:"response"`
		}
		if err := json.Unmarshal(msg, &evt); err != nil {
			t.Fatalf("decode error frame: %v (%s)", err, string(msg))
		}
		if evt.Type != "response.failed" || evt.Response.Error.Code != "upstream_error" {
			t.Fatalf("unexpected error frame: %s", string(msg))
		}
	})
}
