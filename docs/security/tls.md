# TLS 구성

게이트웨이로 오가는 것은 API 키, 대시보드 세션, 로그인 비밀번호, 프롬프트와 응답 본문이다. 평문으로 두면 같은 사내망의 누구나 볼 수 있다. 운영에서는 아래 두 배치 중 하나로 반드시 암호화한다.

| 배치 | 언제 | 설정 |
|---|---|---|
| **A. 게이트웨이가 직접 종단** | 게이트웨이를 바로 열 때(앞단 없음) | `GATEWAY_TLS_CERT_FILE`, `GATEWAY_TLS_KEY_FILE` |
| **B. 앞단(nginx 등)이 종단** | 대시보드 화면을 같은 주소로 내거나 여러 대를 묶을 때 | 앞단 TLS + `GATEWAY_TRUSTED_PROXIES` |

둘 다 하지 않으면 게이트웨이는 기동 로그에 경고를 남기고 평문으로 받는다.

## 1. 인증서 준비(폐쇄망 사내 CA)

인증서의 SAN(Subject Alternative Name)에 **클라이언트가 실제로 여는 주소**(도메인 또는 IP)가 들어가야 한다. CN 만 있는 인증서는 요즘 클라이언트가 거부한다.

사내 CA 가 없으면 시험·소규모 운영용으로 직접 만든다(`openssl` 3.x. macOS 기본 `openssl` 은 LibreSSL 이라 일부 옵션이 없다. `brew install openssl@3` 후 `/opt/homebrew/opt/openssl@3/bin/openssl` 을 쓴다):

```sh
# 1) 사내 CA(10년). ca.key 는 오프라인에 보관한다
openssl req -x509 -new -nodes -newkey ec -pkeyopt ec_paramgen_curve:P-256 \
  -keyout ca.key -out ca.crt -days 3650 -subj "/CN=Internal Gateway CA"

# 2) 게이트웨이 서버 인증서(1년). SAN 에 여는 주소를 모두 넣는다
openssl req -new -nodes -newkey ec -pkeyopt ec_paramgen_curve:P-256 \
  -keyout tls.key -out tls.csr -subj "/CN=gateway.internal"
openssl x509 -req -in tls.csr -CA ca.crt -CAkey ca.key -CAcreateserial -days 365 -out tls.crt \
  -extfile <(printf "subjectAltName=DNS:gateway.internal,IP:203.0.113.10\nextendedKeyUsage=serverAuth")
```

클라이언트(데스크탑 앱, 브라우저, `curl`, 사내 서비스)는 `ca.crt` 를 신뢰 저장소에 넣어야 한다. 넣지 않으면 "인증서를 신뢰할 수 없다"로 접속이 끊긴다.

## 2. 배치 A: 게이트웨이가 직접 종단

```sh
GATEWAY_TLS_CERT_FILE=/certs/tls.crt   # 중간 CA 가 있으면 서버 인증서 뒤에 이어 붙인 파일
GATEWAY_TLS_KEY_FILE=/certs/tls.key
GATEWAY_PUBLIC_URL=https://gateway.internal:4701
```

- 두 값은 함께 정한다. 하나만 있으면 기동하지 않는다(평문으로 떠서 운영자를 속이지 않게).
- TLS 1.2 이상만 받는다. 1.0·1.1 은 거절한다. 암호 묶음은 Go 기본값(1.3 고정, 1.2 는 안전한 것만)이다.
- 응답에 `Strict-Transport-Security: max-age=31536000` 을 붙인다. `includeSubDomains` 는 붙이지 않는다(같은 도메인의 다른 사내 서비스까지 묶지 않게).
- `GATEWAY_PUBLIC_URL` 을 비우면 `https://localhost:포트` 가 기본이다. https 가 아니면 기동 로그에 경고가 남는다. 로그인 뒤 이동 주소와 SSO 되돌림 주소가 이 값이다.
- 기동 때 인증서를 읽지 못하면 기동하지 않는다.

Docker compose 에서는 인증서 폴더를 읽기 전용으로 붙인다(`docker-compose.yml` 의 `llm-gateway` 서비스):

```yaml
    volumes:
      - ./certs:/certs:ro
    environment:
      GATEWAY_TLS_CERT_FILE: /certs/tls.crt
      GATEWAY_TLS_KEY_FILE: /certs/tls.key
```

컨테이너는 사용자 65534 로 돈다. 키 파일을 그 사용자가 읽을 수 있게 둔다(예: `chmod 640 tls.key` 후 그룹을 맞추거나 `644`).

### 인증서 갱신(재기동 없음)

같은 경로의 파일을 새 인증서로 바꾸면 게이트웨이가 10초 안에 알아채고 **다음 접속부터** 새 인증서를 쓴다. 이미 열린 연결은 그대로다. 새 파일을 읽지 못하면(쓰는 중이거나 키와 짝이 안 맞음) 이전 인증서를 계속 쓰고 오류 로그를 남긴다. 파일을 바꿀 때는 키와 인증서를 함께 바꾼다.

## 3. 배치 B: 앞단이 종단

앞단이 TLS 를 끝내고 게이트웨이에는 사내 평문으로 넘긴다. 게이트웨이와 앞단 사이 구간은 같은 서버(또는 격리된 망)여야 한다.

```nginx
server {
    listen 443 ssl;
    server_name gateway.internal;
    ssl_certificate     /etc/nginx/certs/tls.crt;
    ssl_certificate_key /etc/nginx/certs/tls.key;
    ssl_protocols TLSv1.2 TLSv1.3;
    add_header Strict-Transport-Security "max-age=31536000" always;
    client_max_body_size 32m;

    location / {
        proxy_pass http://127.0.0.1:4000;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;   # 게이트웨이가 보안 쿠키를 심는 데 쓴다
        proxy_buffering off;                          # LLM 스트림을 받은 만큼 바로 넘긴다
        proxy_read_timeout 300s;                      # 로컬 모델은 첫 토큰이 느리다
    }
}
server {
    listen 80;
    return 301 https://$host$request_uri;
}
```

게이트웨이 설정:

```sh
GATEWAY_TRUSTED_PROXIES=127.0.0.1        # 앞단 주소(IP 또는 CIDR)
GATEWAY_PUBLIC_URL=https://gateway.internal
```

- `GATEWAY_TRUSTED_PROXIES` 를 정해야 게이트웨이가 앞단이 붙인 `X-Forwarded-For`(로그인 시도 제한·IP 허용 목록·감사 기록의 접속 주소)와 `X-Forwarded-Proto`(대시보드 쿠키의 `Secure`)를 믿는다. 신뢰하지 않는 상대가 보낸 이 헤더는 무시한다.
- 게이트웨이 포트(4000)는 앞단만 닿게 막는다. 밖에서 평문 포트로 바로 들어오면 앞단의 TLS 를 우회한다.

## 4. 확인

```sh
# 인증서와 버전(배치 A 면 게이트웨이 주소, B 면 앞단 주소)
openssl s_client -connect gateway.internal:4701 -servername gateway.internal -CAfile ca.crt </dev/null 2>/dev/null | grep -E "Verify return|Protocol"
# TLS 1.1 은 서버가 거절해야 한다("alert protocol version"). OpenSSL 3 클라이언트는 기본 보안 수준에서 스스로 1.1 을 막으므로
# (그때는 "no protocols available" 이 나오고 서버까지 가지 않는다) 보안 수준을 낮춰 서버의 거절을 본다
openssl s_client -connect gateway.internal:4701 -tls1_1 -cipher 'DEFAULT@SECLEVEL=0' </dev/null 2>&1 | grep -iE "alert protocol version"
# 헬스 체크와 HSTS
curl --cacert ca.crt -si https://gateway.internal:4701/health/liveliness | grep -iE "^HTTP|strict-transport"
```

## 5. 남은 것

- 게이트웨이에서 업스트림(Ollama 등)으로 가는 구간은 이 문서의 범위 밖이다. 지금은 사내 평문 주소를 가정한다. 업스트림이 https 이면 이미지(FROM scratch)에 사내 CA 를 넣어야 한다.
- 클라이언트 인증서(mTLS)는 없다.
