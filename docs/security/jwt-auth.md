# Keycloak 토큰(JWT)으로 API 직접 호출

가상 키(`sk-…`) 없이 `Authorization: Bearer <JWT>` 로 API 를 부른다. 서명은 사내 Keycloak 의 공개키(JWKS)로 검증하고, 토큰의 그룹으로 팀·관리자를 정한다. litellm 은 이 기능을 enterprise 로 막아 두었는데 우리는 막지 않는다. 기존 가상 키·로그인 키 방식은 그대로 동작한다.

## 켜기

환경 변수 `GATEWAY_JWT_ISSUER` 가 있어야 켜진다. 없으면 JWT 모양 토큰은 가상 키 형식 오류(401)로 거절한다.

| 환경 변수 | 뜻 |
|---|---|
| `GATEWAY_JWT_ISSUER` | 토큰의 `iss` 와 **정확히** 같아야 한다. 예: `http://keycloak:8080/realms/corp` |
| `GATEWAY_JWT_AUDIENCE` | 필수. 토큰의 `aud` 에 이 값이 있어야 한다 |
| `GATEWAY_JWT_JWKS_URL` | 선택. 비우면 발급자의 `/.well-known/openid-configuration` 의 `jwks_uri` 를 쓴다 |
| `GATEWAY_GROUP_MAP`·`GATEWAY_GROUPS_CLAIM` | 그룹 → 팀·관리자 표와 그룹 클레임 이름. SSO 로그인과 같이 쓴다(`docs/security/group-mapping.md`). 처음 쓰던 `GATEWAY_JWT_GROUP_MAP`·`GATEWAY_JWT_GROUPS_CLAIM` 도 읽는다 |

설정이 틀리면 기동하지 않는다: 대상 없음, 발급자 없이 다른 JWT 값만 있음(그룹 표 오류는 `docs/security/group-mapping.md`). 기동 로그에 `jwt: true` 가 남는다. Keycloak 이 느리거나 죽어 있어도 기동은 막지 않는다(공개키는 처음 쓸 때 가져온다).

### Keycloak 쪽 설정
- 클라이언트에 **audience 매퍼**를 둬서 액세스 토큰의 `aud` 에 `GATEWAY_JWT_AUDIENCE` 값이 들어가게 한다. 기본 설정은 `aud` 에 `account` 만 넣는다.
- 그룹을 쓰려면 **Group Membership 매퍼**로 `groups` 클레임을 넣는다. 그룹 이름의 앞 `/` 는 매퍼의 "Full group path" 를 끄면 없어진다. 표의 이름은 토큰에 든 문자열과 똑같아야 한다.
- 이메일·`email_verified`·`name`·`preferred_username` 이 토큰에 들어가도록 `email`·`profile` 범위를 쓴다.
- **액세스 토큰 수명을 짧게(5분 안팎) 둔다.** 아래 "한계"의 이유다.

## 검증하는 것

- 서명: **RS256·ES256 만** 받는다. `none`·HS256 등은 거절한다(공개키를 비밀로 쓰는 알고리즘 혼동 공격).
- 발급자(`iss`)와 대상(`aud`)이 설정과 같다. 만료(`exp`)가 없는 토큰은 거절한다. `exp`·`nbf` 는 30초 시계 오차만 허용한다.
- 공개키는 설정한 발급자의 JWKS 에서만 가져온다. 토큰 헤더의 `jku`·`x5u`·`jwk` 는 읽지 않고, `crit` 헤더가 있으면 거절한다. 2048비트 미만 RSA 키와 P-256 이 아닌 EC 키는 무시한다.
- 공개키는 10분 캐시한다. 모르는 `kid` 가 와도 30초에 한 번만 다시 가져와서(키 회전은 따라가고, 모르는 `kid` 로 Keycloak 을 두드리지는 못한다), Keycloak 이 죽어도 마지막 키로 1시간까지 버틴다. 그 뒤에는 503 이다(검증하지 못하면 통과시키지 않는다).
- 거절 사유는 같은 종류를 1분에 한 번만 로그로 남기고 토큰 내용은 남기지 않는다. 클라이언트에는 `invalid or expired JWT.`(401)만 보인다.

## 사용자·팀·권한

| 항목 | 동작 |
|---|---|
| 사용자 | 토큰의 이메일(없으면 `preferred_username`)이 사용자 아이디다. 처음 온 사용자는 **SSO 로그인과 같은 규칙으로 자동 생성**한다(기본 역할·모델은 대시보드 Default User Settings). 이미 다른 SSO 주체(`sub`)에 묶인 이메일은 403, 관리자가 미리 만든 계정은 Keycloak 이 이메일을 확인하지 않았으면(`email_verified=false`) 403 이다 |
| 팀 | 토큰의 그룹 중 표에 있는 것이 주는 팀 가운데 **그룹 이름 순으로 처음 있는 팀**이 이 요청의 팀이다. 팀의 허용 모델·예산·한도·조직 한도·가드레일 설정을 그대로 받는다. 매핑된 팀이 아직 없으면 건너뛴다(로그 경고). 막힌 팀이면 요청을 거절한다. 사용자는 그 팀의 팀원(role `user`)으로 추가된다(대시보드·사용량에 보이도록) |
| 팀이 없을 때 | 사용자의 허용 모델을 쓴다(로그인 키와 같다) |
| 관리자 | **매핑에 `admin: true` 로 적은 그룹이 토큰에 있을 때만** 이 요청이 `proxy_admin` 이다. 사용자 행의 역할은 바뀌지 않는다. 그룹이 빠진 다음 토큰부터 바로 관리자가 아니다 |
| 한도 | 가상 키가 없어 **키 단위 한도는 걸리지 않는다**. 사용자·팀·조직·프로젝트 한도(예산·RPM·TPM)가 걸린다. 사용량 기록에는 사용자·팀이 남는다 |
| 팀 소속의 기준 | **토큰이 기준이다.** Keycloak 에서 그룹을 빼면 그 사용자의 다음 토큰(수명이 짧다)부터 팀 제한이 없어진다. 이미 만들어진 팀원 기록(`team_member`)은 지우지 않는다. 관리자가 대시보드에서 손으로 넣은 팀원 기록과 섞이기 때문이다 |

## 한계(알고 쓸 것)

- **토큰 하나는 만료까지 유효하다.** 게이트웨이가 Keycloak 에 요청마다 묻지 않는다(느려지고 Keycloak 장애가 전체 장애가 된다). 퇴사자를 즉시 끊어야 하면 액세스 토큰 수명을 짧게 두고, 급하면 게이트웨이에서 그 사용자가 속한 팀을 막는다(팀 차단은 요청마다 바로 반영된다).
- **사용자 행을 지워도 Keycloak 토큰이 유효하면 다음 요청에서 다시 만들어진다.** 막으려면 Keycloak 에서 사용자를 비활성화한다.
- **키 단위 한도·키 단위 모델 제한은 없다.** 필요하면 가상 키를 쓴다.
- 한 요청에는 팀이 하나다. 토큰이 여러 팀의 그룹을 가지면 위의 순서로 정한 한 팀을 쓴다.
- 그룹 이름 표는 환경 변수이고 대시보드에서 바꿀 수 없다(바꾸려면 재기동).

## 확인

```sh
# Keycloak 에서 토큰을 받는다(예: password grant 를 켠 시험용 클라이언트)
TOKEN=$(curl -s "$KEYCLOAK/realms/corp/protocol/openid-connect/token" \
  -d grant_type=password -d client_id=llm-gateway -d username=… -d password=… | jq -r .access_token)
curl -s "$GATEWAY/v1/models" -H "Authorization: Bearer $TOKEN"
```
토큰의 `iss`·`aud`·`groups` 가 위 설정과 맞는지 모르겠으면 payload(두 번째 `.` 앞뒤)를 base64 로 풀어 본다.
