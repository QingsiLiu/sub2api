package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/releaselifecycle"
	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
)

func TestGeiliReleaseReadinessAndPrivateMutations(t *testing.T) {
	c := &ReleaseControl{controller: releaselifecycle.New(true, false), dataDir: t.TempDir(), slot: "blue"}
	defer c.Close()
	get := func(method, path string) int {
		r := httptest.NewRecorder()
		c.Handler().ServeHTTP(r, httptest.NewRequest(method, path, nil))
		return r.Code
	}
	if get("GET", "/release/ready") != 503 {
		t.Fatal("startup reported ready")
	}
	c.controller.MarkReady()
	if get("GET", "/release/ready") != 200 {
		t.Fatal("healthy standby cannot warm up")
	}
	c.checks = []func(context.Context) error{func(context.Context) error { return errors.New("redis unavailable") }}
	if get("GET", "/release/ready") != 503 {
		t.Fatal("unhealthy dependency accepted")
	}
	if get("GET", "/release/drain") != http.StatusMethodNotAllowed {
		t.Fatal("GET mutated state")
	}
	if get("POST", "/release/drain") != 200 || !c.controller.Status().Drained {
		t.Fatal("idempotent drain failed")
	}
}

func TestGeiliBackgroundOwnerWaitsForRunningJob(t *testing.T) {
	dir := t.TempDir()
	a := &ReleaseControl{controller: releaselifecycle.New(true, false), dataDir: dir, slot: "blue"}
	b := &ReleaseControl{controller: releaselifecycle.New(true, false), dataDir: dir, slot: "green"}
	defer a.Close()
	defer b.Close()
	if err := a.resume(); err != nil {
		t.Fatal(err)
	}
	job, ok := a.controller.BeginBackground("paid-probe")
	if !ok {
		t.Fatal("owner not active")
	}
	a.controller.Drain()
	a.pause()
	if err := b.resume(); err == nil {
		t.Fatal("peer stole ownership from a running job")
	}
	job()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if err := b.resume(); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("owner was never released")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if owner, err := os.ReadFile(filepath.Join(dir, ".geili-background-owner")); err != nil || string(owner) != "green" {
		t.Fatal("restart owner not persisted")
	}
	if !a.controller.Status().Drained {
		t.Fatal("old process not drained")
	}
}

func TestGeiliReleasePreservesRealWebsocket(t *testing.T) {
	controller := releaselifecycle.New(true, true)
	router := gin.New()
	router.Use(releaseLifecycleForGeili(controller))
	entered, release, ended := make(chan struct{}), make(chan struct{}), make(chan struct{})
	router.GET("/ws", func(c *gin.Context) {
		conn, err := websocket.Accept(c.Writer, c.Request, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		defer close(ended)
		close(entered)
		<-release
		_ = conn.Write(context.Background(), websocket.MessageText, []byte("terminal"))
	})
	server := httptest.NewServer(router)
	defer server.Close()
	conn, _, err := websocket.Dial(context.Background(), "ws"+server.URL[4:]+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	<-entered
	controller.Drain()
	if controller.Status().Active["websocket"] != 1 || controller.Status().Drained {
		t.Fatal("real hijacked handler escaped drain")
	}
	close(release)
	_, data, err := conn.Read(context.Background())
	if err != nil || string(data) != "terminal" {
		t.Fatalf("websocket truncated: %s %v", data, err)
	}
	<-ended
	if err := controller.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestGeiliReleasePreservesSlowUpload(t *testing.T) {
	controller := releaselifecycle.New(true, true)
	router := gin.New()
	router.Use(releaseLifecycleForGeili(controller))
	entered := make(chan struct{})
	router.POST("/upload", func(c *gin.Context) {
		close(entered)
		data, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.Status(400)
			return
		}
		c.String(200, string(data))
	})
	server := httptest.NewServer(router)
	defer server.Close()
	reader, writer := io.Pipe()
	result := make(chan error, 1)
	go func() {
		response, err := http.Post(server.URL+"/upload", "text/plain", reader)
		if err != nil {
			result <- err
			return
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err == nil && (response.StatusCode != 200 || string(body) != "complete upload") {
			err = errors.New("upload was truncated")
		}
		result <- err
	}()
	<-entered
	controller.Drain()
	if controller.Status().Drained {
		t.Fatal("upload body was ignored")
	}
	_, _ = io.WriteString(writer, "complete upload")
	_ = writer.Close()
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}
