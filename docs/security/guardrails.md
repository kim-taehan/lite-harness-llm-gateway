# 가드레일(내용 필터·개인정보 가리기)

LLM 으로 가는 질문과 돌아오는 답을 게이트웨이가 정규식·금지어로 검사해 **막거나(BLOCK) 가린다(MASK)**. litellm 의 `litellm_content_filter` 가드레일과 같은 설정 모양이라 대시보드 **Guardrails** 화면(Add Guardrail → LiteLLM Content Filter)을 그대로 쓴다. 외부 서버가 필요 없다.

## 동작

| 동작 | 결과 |
|---|---|
| BLOCK | 업스트림에 보내지 않고 **400**. 본문은 litellm 과 같은 모양이다: `error.message` = `Content blocked: kr_rrn pattern detected`, `error.provider_specific_fields` = `{error, pattern, guardrail_name, guardrail_mode}` (금지어면 `keyword`, `description`). **걸린 글의 내용은 응답·로그·사용량 어디에도 남기지 않고** 패턴 이름만 쓴다 |
| MASK | 걸린 부분을 `[EMAIL_REDACTED]` 처럼 바꿔 요청을 계속 처리한다. 이름은 패턴 이름의 대문자다. 금지어는 `[KEYWORD_REDACTED]`. 형식은 `pattern_redaction_format`(`{pattern_name}` 자리표시)·`keyword_redaction_tag` 로 바꿀 수 있다 |

BLOCK 은 가리기 전의 글로 **먼저 모두** 본다(앞의 가리기 규칙이 뒤의 차단 대상을 지우지 못하게). 정규식은 Go RE2 라 실행 시간이 글 길이에 비례한다(폭주하는 정규식이 없다).

## 검사 시점과 대상

| mode | 검사 |
|---|---|
| `pre_call` | 업스트림에 보내기 전 요청. 채팅 `messages`(문자열·`text` 조각), Responses `input`·`instructions`, Embeddings `input` |
| `during_call` | `pre_call` 과 같게 처리한다(요청을 먼저 검사) |
| `post_call` | **스트림이 아닌** 응답의 모델이 쓴 글(채팅 `choices[].message.content`, Responses `output[].content[].text`). 막으면 400 이고 **토큰은 이미 썼으므로 사용량에는 남는다**(`error_code=guardrail_blocked`) |

받지 않는 시점: `logging_only`, MCP, realtime(대응하는 기능이 없다). 만들 때 400 이다.

## 한계(알고 쓸 것)

- **스트리밍 응답은 검사하지 않는다.** 조각난 글자를 이어 붙여 검사해야 해서 지연·복잡도가 커 1차에서 뺐다. 모델이 쓴 개인정보를 막아야 하면 스트림을 쓰지 않게 앞단에서 정한다. 질문 검사는 스트림 요청에도 적용된다.
- **정규식·금지어만 잡는다.** 사람 이름·주소처럼 형식이 없는 개인정보는 못 잡는다(Presidio 같은 언어 모델 기반 도구가 필요하다. 연동하지 않았다).
- 도구 호출(`tool_calls`)의 인자와 이미지는 검사하지 않는다. 시스템 메시지는 검사한다.
- 위험 주제 카테고리(`categories`)는 받지 않는다(영어 키워드 목록이라 사내에 맞지 않는다). 만들 때 400 이다.
- 파일·base64 안에 숨긴 글, 글자를 쪼개 쓰거나 변형한 우회는 막지 못한다. 가드레일은 실수를 줄이는 장치이지 악의적인 우회를 막는 보안 경계가 아니다.

## 기본 패턴

| 분류 | 이름 | 비고 |
|---|---|---|
| 한국 개인정보 | `kr_rrn`(주민·외국인등록번호), `kr_phone`, `kr_passport`, `kr_business_number`, `kr_driver_license` | 주민번호는 생년월일(월 1-12·일 1-31)·성별 자리(1-8), 사업자번호는 검증 숫자를 확인해 오탐을 줄인다 |
| 공통 | `email`, `credit_card`(Luhn 검사), `ipv4`, `url` | `email`·`ipv4`·`url` 은 litellm 과 같은 정규식이다 |
| 비밀값 | `openai_api_key`(`sk-…`, 이 게이트웨이의 가상 키 모양), `private_key`(PEM 블록), `aws_access_key`, `aws_secret_key`, `github_token`, `slack_token`, `generic_api_key` | |

직접 쓴 정규식(`pattern`)과 금지어도 된다. 직접 쓴 정규식과 금지어는 litellm 처럼 대소문자를 가리지 않고, 금지어는 **단어 경계 없이 부분 문자열**로 찾으며(`ass` 가 `class` 에도 걸린다) `*` 는 글자 0~1개다. 규칙은 가드레일 하나에 200개, 정규식은 1000자, 금지어는 200자까지다.

## 어떻게 적용되나

- **`default_on: true`**: 모든 요청에 건다(전사 정책).
- **`default_on: false`**: 이름이 지정된 요청에만 건다. 지정하는 곳은 셋이다. 요청 본문 `guardrails`(문자열 목록 또는 `{이름: 설정}`, `metadata.guardrails` 도 읽는다), 키의 `metadata.guardrails`, 팀의 `metadata.guardrails`. 대시보드에서 키·팀을 만들 때 `guardrails` 를 고르면 metadata 로 저장된다. 요청 본문의 `guardrails` 는 업스트림에 보내지 않는다.
- **끄기**: 키·팀의 `disable_global_guardrails: true`(전사 정책 전부), `opted_out_global_guardrails: ["이름"]`(그 이름만). 키 값이 팀 값보다 우선이다. **관리자만 정할 수 있다.** 키 소유자·팀 admin 이 자기 키·팀 metadata 에 적어 정책을 벗어나지 못하게 만들기·고치기에서 403 으로 막는다(이미 있는 값을 그대로 두거나 없애는 것은 된다). 요청 본문으로는 끌 수 없다.
- 키·팀에 이름을 붙이는 방식은 **키 소유자·팀 admin 이 metadata 를 고쳐 그 이름을 뺄 수 있다.** 빠지면 안 되는 정책은 `default_on: true` 로 둔다.
- 저장된 설정이 깨져 규칙으로 만들 수 없으면 요청을 통과시키지 않고 500 으로 답한다(검사를 건너뛰지 않는다).
- 다른 인스턴스에서 바꾼 규칙은 최대 5초 뒤에, 같은 인스턴스에서 바꾼 것은 바로 반영된다. 키·팀의 설정 변경은 바로 반영된다.

## 관리

- 대시보드 Guardrails 화면, 또는 `POST /guardrails`(본문 `{"guardrail": {guardrail_name, litellm_params, guardrail_info}}`), `GET /v2/guardrails/list`, `GET /guardrails/{id}/info`, `PATCH`·`PUT`·`DELETE /guardrails/{id}`. `litellm_params` 는 `{guardrail: "litellm_content_filter", mode, default_on, patterns[], blocked_words[]}` 이다. PATCH 는 보낸 키만 덮어쓰고(null 이면 그 키를 지운다) 저장 전에 검사한다.
- 만들기·고치기·지우기는 **관리자만**, 모두 감사 기록(`LiteLLM_GuardrailsTable`, 이전 값 포함)에 남는다. 목록·상세는 읽기 전용 관리자 이상만 규칙을 본다(사내 금지어·정규식이 들어 있다). 일반 사용자의 목록은 빈 배열이다.
- 막은 횟수는 `/metrics` 의 `gateway_guardrail_blocked_total{guardrail,mode}` 다. 대시보드의 가드레일 사용 현황·로그 화면은 빈 응답이다.
- litellm 에서 키·팀 가드레일은 유료(enterprise) 기능이지만 우리는 막지 않는다.

## 예: 개인정보는 가리고 주민번호는 막기

```json
POST /guardrails
{"guardrail": {"guardrail_name": "pii", "litellm_params": {
  "guardrail": "litellm_content_filter", "mode": "pre_call", "default_on": true,
  "patterns": [
    {"pattern_type": "prebuilt", "pattern_name": "email",    "name": "email",    "action": "MASK"},
    {"pattern_type": "prebuilt", "pattern_name": "kr_phone", "name": "kr_phone", "action": "MASK"},
    {"pattern_type": "prebuilt", "pattern_name": "kr_rrn",   "name": "kr_rrn",   "action": "BLOCK"}]},
  "guardrail_info": {}}}
```
