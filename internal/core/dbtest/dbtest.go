// Package dbtest 는 실제 PostgreSQL 로 저장소·API 를 시험하는 도우미다. 시험에서만 쓴다.
//
// GATEWAY_TEST_DATABASE_URL 이 있으면 그 서버를 쓰고, 없으면 docker 로 postgres:16-alpine 을 띄운다.
// Docker 도 없으면 건너뛰지 않고 실패한다(CI 에서 DB 시험이 조용히 빠지지 않게. 설계 11.4).
// 시험마다 새 데이터베이스를 만들고 마이그레이션을 적용하므로 시험끼리 데이터가 섞이지 않는다.
package dbtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"llm-gateway/internal/core"
)

var serverURL string

// Main 은 시험 패키지의 TestMain 에서 부른다. 필요하면 PostgreSQL 컨테이너를 띄우고, 시험이 끝나면 내린다
func Main(m *testing.M) int {
	if url := os.Getenv("GATEWAY_TEST_DATABASE_URL"); url != "" {
		serverURL = url
		return m.Run()
	}
	stop, err := startContainer()
	if err != nil {
		fmt.Fprintln(os.Stderr, "dbtest: PostgreSQL 을 띄우지 못했다:", err)
		return 1
	}
	defer stop()
	return m.Run()
}

// Open 은 이 시험만 쓰는 새 데이터베이스를 만들고 마이그레이션을 적용한 풀을 돌려준다
func Open(t *testing.T) (*core.DB, *pgxpool.Pool) {
	t.Helper()
	if serverURL == "" {
		t.Fatal("dbtest.Main 을 TestMain 에서 부르지 않았다")
	}
	ctx := t.Context()
	admin, err := pgxpool.New(ctx, serverURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	name := "t_" + randomHex(8)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, withDatabase(serverURL, name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		cleanup, err := pgxpool.New(context.Background(), serverURL)
		if err == nil {
			_, _ = cleanup.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
			cleanup.Close()
		}
	})
	if err := core.Migrate(ctx, pool, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	return core.NewDB(pool), pool
}

func startContainer() (func(), error) {
	docker, err := dockerBinary()
	if err != nil {
		return nil, err
	}
	out, err := exec.Command(docker, "run", "-d", "--rm", "-e", "POSTGRES_PASSWORD=test", "-p", "127.0.0.1::5432", "postgres:16-alpine").Output()
	if err != nil {
		return nil, fmt.Errorf("docker run: %w", err)
	}
	id := strings.TrimSpace(string(out))
	stop := func() { _ = exec.Command(docker, "rm", "-f", id).Run() }
	portOut, err := exec.Command(docker, "port", id, "5432/tcp").Output()
	if err != nil {
		stop()
		return nil, fmt.Errorf("docker port: %w", err)
	}
	hostPort := strings.TrimSpace(strings.Split(string(portOut), "\n")[0])
	serverURL = "postgres://postgres:test@" + hostPort + "/postgres?sslmode=disable"
	if err := waitReady(serverURL, 30*time.Second); err != nil {
		stop()
		return nil, err
	}
	return stop, nil
}

func waitReady(url string, limit time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		pool, err := pgxpool.New(ctx, url)
		if err == nil {
			err = pool.Ping(ctx)
			pool.Close()
			if err == nil {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("PostgreSQL 이 %s 안에 준비되지 않았다: %w", limit, err)
		case <-ticker.C:
		}
	}
}

// Docker Desktop(macOS)은 docker 를 PATH 에 넣지 않을 때가 있다
func dockerBinary() (string, error) {
	if path, err := exec.LookPath("docker"); err == nil {
		return path, nil
	}
	const desktop = "/Applications/Docker.app/Contents/Resources/bin/docker"
	if _, err := os.Stat(desktop); err == nil {
		return desktop, nil
	}
	return "", fmt.Errorf("docker 를 찾지 못했다. Docker 를 설치하거나 GATEWAY_TEST_DATABASE_URL 을 준다")
}

func withDatabase(raw, name string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.Path = "/" + name
	return u.String()
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
