package auth

import (
	"testing"
	"time"
)

func TestLoginAttempts_PairAndIPLimitsAndSweep(t *testing.T) {
	now := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	a := newLoginAttempts(func() time.Time { return now })

	for i := 0; i < maxFailedLogins; i++ {
		a.fail("kim", "1.1.1.1")
	}
	if !a.blocked("KIM", "1.1.1.1") {
		t.Error("같은 아이디·IP 로 10번 틀리면(대소문자 무시) 막혀야 한다")
	}
	if a.blocked("kim", "2.2.2.2") || a.blocked("lee", "1.1.1.1") {
		t.Error("다른 IP·다른 아이디는 막히지 않아야 한다(남이 계정을 잠그지 못하게)")
	}
	a.succeed("kim", "1.1.1.1")
	if a.blocked("kim", "1.1.1.1") {
		t.Error("성공하면 그 아이디·IP 의 실패는 지워진다")
	}

	// 아이디를 바꿔 가며 같은 IP 로 찔러 보는 것은 IP 총량으로 막는다
	for i := 0; i < maxFailedLoginsPerIP; i++ {
		a.fail("guess-"+string(rune('a'+i%26))+string(rune('a'+i/26)), "3.3.3.3")
	}
	if !a.blocked("brand-new-id", "3.3.3.3") {
		t.Error("같은 IP 의 실패가 총량을 넘으면 처음 보는 아이디도 막혀야 한다")
	}

	// 창이 지나면 풀리고, 한 번 쓰고 버린 항목은 맵에서 사라진다
	now = now.Add(failedLoginWindow + time.Second)
	if a.blocked("brand-new-id", "3.3.3.3") {
		t.Error("창이 지나면 풀려야 한다")
	}
	a.fail("someone", "4.4.4.4") // 이 호출이 오래된 항목을 쓸어 낸다
	if len(a.failures) != 2 {
		t.Errorf("오래된 실패 기록이 남았다: %d 개", len(a.failures))
	}
}
