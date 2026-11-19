package ws_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type SessionResp struct {
	Token  string `json:"token"`
	Viewer struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
	} `json:"viewer"`
}

func getSessionToken(t *testing.T, name string) (string, string) {
	resp, err := http.Post("http://localhost:8080/api/v1/session", "application/json", strings.NewReader(fmt.Sprintf(`{"display_name":"%s","city":"Mumbai"}`, name)))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	bodyBytes, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var sess SessionResp
	err = json.Unmarshal(bodyBytes, &sess)
	require.NoError(t, err)
	require.NotEmpty(t, sess.Token)
	return sess.Token, sess.Viewer.ID
}

func TestWebSocketRealtimeEndToEnd(t *testing.T) {
	token, viewerID := getSessionToken(t, "Aarav Test")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	wsURL := fmt.Sprintf("ws://localhost:8080/ws?event=cc-0826&token=%s", token)
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	require.NoError(t, err)
	defer conn.Close(websocket.StatusNormalClosure, "test finished")

	// 1. Receive welcome message
	_, welcomeBytes, err := conn.Read(ctx)
	require.NoError(t, err)

	var welcomeMsg map[string]any
	err = json.Unmarshal(welcomeBytes, &welcomeMsg)
	require.NoError(t, err)
	assert.Equal(t, "welcome", welcomeMsg["t"])
	assert.Equal(t, "crowdcast-api-01", welcomeMsg["node"])
	assert.Equal(t, "cc-0826", welcomeMsg["event_id"])
	assert.Equal(t, viewerID, welcomeMsg["viewer_id"])

	// 2. Send chat message
	chatMsg := map[string]any{
		"t":    "chat",
		"text": "Hello from realtime test!",
	}
	chatBytes, _ := json.Marshal(chatMsg)
	err = conn.Write(ctx, websocket.MessageText, chatBytes)
	require.NoError(t, err)

	// 3. Read incoming messages until chat is received
	foundChat := false
	for i := 0; i < 5; i++ {
		readCtx, rCancel := context.WithTimeout(ctx, 3*time.Second)
		_, msgBytes, err := conn.Read(readCtx)
		rCancel()
		require.NoError(t, err)

		var msg map[string]any
		_ = json.Unmarshal(msgBytes, &msg)

		if msg["t"] == "chat" {
			foundChat = true
			msgObj, ok := msg["message"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, "Hello from realtime test!", msgObj["text"])
			assert.Equal(t, "Aarav Test", msgObj["name"])
			break
		}
	}
	assert.True(t, foundChat, "expected chat message broadcast")

	// 4. Send ping, verify pong
	pingMsg := []byte(`{"t":"ping"}`)
	err = conn.Write(ctx, websocket.MessageText, pingMsg)
	require.NoError(t, err)

	foundPong := false
	for i := 0; i < 5; i++ {
		readCtx, rCancel := context.WithTimeout(ctx, 3*time.Second)
		_, msgBytes, err := conn.Read(readCtx)
		rCancel()
		require.NoError(t, err)

		var msg map[string]any
		_ = json.Unmarshal(msgBytes, &msg)
		if msg["t"] == "pong" {
			foundPong = true
			assert.NotNil(t, msg["timestamp"])
			break
		}
	}
	assert.True(t, foundPong, "expected pong response")
}

func TestMultiClientRealtimeBroadcast(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Connect Client 1
	token1, _ := getSessionToken(t, "Viewer One")
	conn1, _, err := websocket.Dial(ctx, fmt.Sprintf("ws://localhost:8080/ws?event=cc-0826&token=%s", token1), nil)
	require.NoError(t, err)
	defer conn1.Close(websocket.StatusNormalClosure, "done")
	_, _, _ = conn1.Read(ctx) // consume welcome

	// Connect Client 2
	token2, _ := getSessionToken(t, "Viewer Two")
	conn2, _, err := websocket.Dial(ctx, fmt.Sprintf("ws://localhost:8080/ws?event=cc-0826&token=%s", token2), nil)
	require.NoError(t, err)
	defer conn2.Close(websocket.StatusNormalClosure, "done")
	_, _, _ = conn2.Read(ctx) // consume welcome

	// Connect Client 3
	token3, _ := getSessionToken(t, "Viewer Three")
	conn3, _, err := websocket.Dial(ctx, fmt.Sprintf("ws://localhost:8080/ws?event=cc-0826&token=%s", token3), nil)
	require.NoError(t, err)
	defer conn3.Close(websocket.StatusNormalClosure, "done")
	_, _, _ = conn3.Read(ctx) // consume welcome

	// Client 1 sends a chat message
	chatMsg := []byte(`{"t":"chat","text":"Fanout test across 3 clients!"}`)
	err = conn1.Write(ctx, websocket.MessageText, chatMsg)
	require.NoError(t, err)

	// Verify Client 2 and Client 3 both receive the broadcast
	readChat := func(conn *websocket.Conn) string {
		for i := 0; i < 5; i++ {
			rCtx, rCancel := context.WithTimeout(ctx, 3*time.Second)
			_, msgBytes, err := conn.Read(rCtx)
			rCancel()
			if err != nil {
				return ""
			}
			var m map[string]any
			_ = json.Unmarshal(msgBytes, &m)
			if m["t"] == "chat" {
				if msgObj, ok := m["message"].(map[string]any); ok {
					return msgObj["text"].(string)
				}
			}
		}
		return ""
	}

	assert.Equal(t, "Fanout test across 3 clients!", readChat(conn2))
	assert.Equal(t, "Fanout test across 3 clients!", readChat(conn3))
}

func TestWebSocketReactionsAndPolls(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	token, _ := getSessionToken(t, "Interaction Tester")
	conn, _, err := websocket.Dial(ctx, fmt.Sprintf("ws://localhost:8080/ws?event=cc-0826&token=%s", token), nil)
	require.NoError(t, err)
	defer conn.Close(websocket.StatusNormalClosure, "done")

	// Consume welcome
	_, _, err = conn.Read(ctx)
	require.NoError(t, err)

	// Send reaction over WS
	reactMsg := []byte(`{"t":"reaction","emoji":"🎉"}`)
	err = conn.Write(ctx, websocket.MessageText, reactMsg)
	require.NoError(t, err)

	// Check for reaction_batch within flusher window (250ms)
	foundReaction := false
	for i := 0; i < 10; i++ {
		rCtx, rCancel := context.WithTimeout(ctx, 2*time.Second)
		_, msgBytes, err := conn.Read(rCtx)
		rCancel()
		if err != nil {
			break
		}
		var m map[string]any
		_ = json.Unmarshal(msgBytes, &m)
		if m["t"] == "reaction_batch" {
			foundReaction = true
			reactions, ok := m["reactions"].(map[string]any)
			require.True(t, ok)
			assert.Contains(t, reactions, "🎉")
			assert.NotZero(t, m["total"])
			break
		}
	}
	assert.True(t, foundReaction, "expected reaction_batch message")

	// Send vote over WS
	voteMsg := []byte(`{"t":"vote","poll":"poll-01","option":2}`)
	err = conn.Write(ctx, websocket.MessageText, voteMsg)
	require.NoError(t, err)

	// Check for poll_update
	foundPollUpdate := false
	for i := 0; i < 10; i++ {
		rCtx, rCancel := context.WithTimeout(ctx, 2*time.Second)
		_, msgBytes, err := conn.Read(rCtx)
		rCancel()
		if err != nil {
			t.Logf("poll read error: %v", err)
			break
		}
		t.Logf("poll read msg: %s", string(msgBytes))
		var m map[string]any
		_ = json.Unmarshal(msgBytes, &m)
		if m["t"] == "poll_update" {
			foundPollUpdate = true
			assert.Equal(t, "poll-01", m["poll_id"])
			assert.NotNil(t, m["votes"])
			assert.NotNil(t, m["percentages"])
			break
		}
	}
	assert.True(t, foundPollUpdate, "expected poll_update message")
}
