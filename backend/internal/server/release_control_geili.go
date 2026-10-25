package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/releaselifecycle"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
	"golang.org/x/sys/unix"
)

// ReleaseControl is deliberately separate from the public router. The only
// listener is the container's loopback; operators access it with docker exec.
type ReleaseControl struct {
	server     *http.Server
	checks     []func(context.Context) error
	poolMax    int
	poolIdle   int
	poolAux    int
	ownerMu    sync.Mutex
	ownerLock  *os.File
	dataDir    string
	slot       string
	controller *releaselifecycle.Controller
	token      string
}

func ProvideReleaseControl(cfg *config.Config, db *sql.DB, rdb *redis.Client, settlement *service.UsageSettlementWorker, gatewayCache service.GatewayCache) (*ReleaseControl, error) {
	for _, name := range []string{"GEILI_RELEASE_CONTROL"} {
		if v := os.Getenv(name); v != "" && v != "true" && v != "false" {
			return nil, fmt.Errorf("%s must be true or false", name)
		}
	}
	c := &ReleaseControl{poolMax: cfg.Database.MaxOpenConns, poolIdle: cfg.Database.MaxIdleConns}
	if cache, ok := gatewayCache.(interface{ EncryptedRoutePoolStatsGeili() sql.DBStats }); ok {
		c.poolAux = cache.EncryptedRoutePoolStatsGeili().MaxOpenConnections
	}
	if !c.lifecycle().Enabled() {
		return c, nil
	}
	c.dataDir, c.slot = cfg.Pricing.DataDir, os.Getenv("GEILI_RELEASE_SLOT")
	if !regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`).MatchString(c.slot) {
		return nil, errors.New("GEILI_RELEASE_SLOT is required for release control")
	}
	c.checks = []func(context.Context) error{
		db.PingContext,
		func(ctx context.Context) error { return rdb.Ping(ctx).Err() },
		func(ctx context.Context) error {
			h := settlement.Health(ctx)
			if !h.Running || h.IngressError != "" || h.StatsError != "" {
				return errors.New("durable settlement not ready")
			}
			return nil
		},
	}
	return c, nil
}

func (c *ReleaseControl) Start() error {
	if !c.lifecycle().Enabled() {
		return nil
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:9091")
	if err != nil {
		return err
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		_ = listener.Close()
		return err
	}
	c.token = hex.EncodeToString(secret)
	if err := os.WriteFile("/tmp/.geili-release-token", []byte(c.token), 0600); err != nil {
		_ = listener.Close()
		return err
	}
	c.server = &http.Server{Handler: c.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = c.server.Serve(listener) }()
	// Recover the active background owner on a container/host restart. Standby
	// containers never start work merely because their process was restarted.
	if owner, err := os.ReadFile(filepath.Join(c.dataDir, ".geili-background-owner")); err == nil && string(owner) == c.slot {
		if err := c.resume(); err != nil {
			c.Close()
			return err
		}
	}
	return nil
}

func (c *ReleaseControl) Close() {
	if c.server != nil {
		_ = c.server.Close()
	}
	c.ownerMu.Lock()
	defer c.ownerMu.Unlock()
	c.unlockOwner()
}

func (c *ReleaseControl) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /release/status", func(w http.ResponseWriter, r *http.Request) { c.writeStatus(w, http.StatusOK) })
	mux.HandleFunc("GET /release/ready", func(w http.ResponseWriter, r *http.Request) {
		if !c.lifecycle().Status().Ready {
			c.writeStatus(w, http.StatusServiceUnavailable)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		for _, check := range c.checks {
			if err := check(ctx); err != nil {
				c.writeStatus(w, http.StatusServiceUnavailable)
				return
			}
		}
		c.writeStatus(w, http.StatusOK)
	})
	mux.HandleFunc("POST /release/drain", func(w http.ResponseWriter, r *http.Request) {
		c.lifecycle().Drain()
		c.pause()
		c.writeStatus(w, http.StatusOK)
	})
	mux.HandleFunc("POST /release/resume", func(w http.ResponseWriter, r *http.Request) {
		if err := c.resume(); err != nil {
			http.Error(w, "background owner is busy", http.StatusConflict)
			return
		}
		c.writeStatus(w, http.StatusOK)
	})
	mux.HandleFunc("POST /release/pause", func(w http.ResponseWriter, r *http.Request) {
		c.lifecycle().PauseBackground()
		c.pause()
		c.writeStatus(w, http.StatusOK)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Loopback alone is insufficient when an upstream issues an HTTP redirect.
		if c.token == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+c.token)) != 1 {
			http.Error(w, "release control authentication required", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (c *ReleaseControl) resume() error {
	c.ownerMu.Lock()
	defer c.ownerMu.Unlock()
	if c.ownerLock == nil {
		lock, err := os.OpenFile(filepath.Join(c.dataDir, ".geili-background.lock"), os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return err
		}
		if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
			_ = lock.Close()
			return err
		}
		c.ownerLock = lock
	}
	owner := filepath.Join(c.dataDir, ".geili-background-owner")
	tmp, err := os.CreateTemp(c.dataDir, ".release-owner-")
	if err != nil {
		c.unlockOwner()
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.WriteString(c.slot)
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), owner)
	}
	if err != nil {
		c.unlockOwner()
		return err
	}
	c.lifecycle().Resume()
	return nil
}

func (c *ReleaseControl) pause() {
	go func() {
		for {
			c.ownerMu.Lock()
			status := c.lifecycle().Status()
			if status.BackgroundActive {
				c.ownerMu.Unlock()
				return
			} // rollback resumed this owner
			if status.BackgroundTotal == 0 {
				if c.ownerLock != nil {
					owner := filepath.Join(c.dataDir, ".geili-background-owner")
					if value, err := os.ReadFile(owner); err == nil && string(value) == c.slot {
						_ = os.Remove(owner)
					}
					c.unlockOwner()
				}
				c.ownerMu.Unlock()
				return
			}
			c.ownerMu.Unlock()
			time.Sleep(100 * time.Millisecond)
		}
	}()
}

func (c *ReleaseControl) unlockOwner() {
	if c.ownerLock != nil {
		_ = unix.Flock(int(c.ownerLock.Fd()), unix.LOCK_UN)
		_ = c.ownerLock.Close()
		c.ownerLock = nil
	}
}

func (c *ReleaseControl) writeStatus(w http.ResponseWriter, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(struct {
		releaselifecycle.Status
		Protocol      int    `json:"protocol"`
		PID           int    `json:"pid"`
		RequestPrefix string `json:"request_prefix"`
		PoolMax       int    `json:"database_max_open_conns"`
		PoolIdle      int    `json:"database_max_idle_conns"`
		PoolAux       int    `json:"database_auxiliary_max_open_conns"`
		PoolTotal     int    `json:"database_total_max_open_conns"`
	}{c.lifecycle().Status(), 2, os.Getpid(), service.RequestIDPrefix(), c.poolMax, c.poolIdle, c.poolAux, c.poolMax + c.poolAux})
}

func (c *ReleaseControl) lifecycle() *releaselifecycle.Controller {
	if c.controller != nil {
		return c.controller
	}
	return releaselifecycle.Default
}

// RunReleaseControlCommand keeps the private token out of argv, inspect, logs,
// and the operator's response. It does not initialize the application or DB.
func RunReleaseControlCommand(action string) error {
	method := http.MethodGet
	switch action {
	case "status", "ready":
	case "pause", "drain", "resume":
		method = http.MethodPost
	default:
		return errors.New("unknown release control action")
	}
	token, err := os.ReadFile("/tmp/.geili-release-token")
	if err != nil {
		return errors.New("release control is not running")
	}
	request, err := http.NewRequest(method, "http://127.0.0.1:9091/release/"+action, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+string(token))
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return errors.New("release control unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("release control HTTP %d", response.StatusCode)
	}
	_, err = io.Copy(os.Stdout, io.LimitReader(response.Body, 1<<20))
	return err
}
