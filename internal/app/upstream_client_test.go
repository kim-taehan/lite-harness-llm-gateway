package app

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// 업스트림 호출은 동시 요청이 많아도 연결을 재사용해야 한다. 유휴 연결을 몇 개만 보관하면(Go 기본은 호스트당 2개) 요청마다
// 새로 연결하고 닫힌 연결이 TIME_WAIT 로 쌓여, 초당 수백 건에서 "cannot assign requested address"(임시 포트 고갈)로 503 이 난다.
// 부하 시험(2026-11-27)에서 실제로 났다
func TestUpstreamClient_ReusesConnectionsUnderConcurrency(t *testing.T) {
	var newConns atomic.Int64
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(30 * time.Millisecond) // 요청들이 겹치게 한다
		_, _ = io.WriteString(w, "ok")
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			newConns.Add(1)
		}
	}
	server.Start()
	t.Cleanup(server.Close)
	client := upstreamClient()
	t.Cleanup(client.CloseIdleConnections)

	wave := func() {
		var wg sync.WaitGroup
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				resp, err := client.Post(server.URL, "application/json", strings.NewReader("{}"))
				if err != nil {
					t.Error(err)
					return
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			}()
		}
		wg.Wait()
	}

	wave() // 동시 100개라 연결도 100개쯤 맺힌다
	before := newConns.Load()
	wave()
	if extra := newConns.Load() - before; extra > 10 {
		t.Errorf("두 번째 100개 요청에 새 연결이 %d 개 생겼다. 유휴 연결을 재사용하지 못한다(10개 이하여야 한다)", extra)
	}
}
